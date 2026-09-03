package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/jsm.go/serverdata"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// A system-account connection is separate from the data connection by
// necessity, not preference: a nats.Conn is bound to one account for its
// lifetime, and the monitoring subjects live in the system account.
const (
	sysReqTimeout = 3 * time.Second
	sysConnName   = "nats-desk-monitor"
)

// sysEvents are the subjects that arrive without being asked for. Together
// they are a live cluster view with no polling at all: STATSZ every 10s from
// each server, and the rest the moment something happens.
var sysEvents = []struct {
	subject string
	kind    string
}{
	{"$SYS.SERVER.*.STATSZ", "statsz"},
	{"$SYS.SERVER.*.SHUTDOWN", "shutdown"},
	{"$SYS.SERVER.*.LAMEDUCK", "lameduck"},
	{"$SYS.SERVER.*.CLIENT.AUTH.ERR", "auth_error"},
	{"$SYS.ACCOUNT.*.CONNECT", "client_connect"},
	{"$SYS.ACCOUNT.*.DISCONNECT", "client_disconnect"},
}

type sysConn struct {
	mu   sync.RWMutex
	nc   *nats.Conn
	live *serverdata.Live
	subs []*nats.Subscription
	url  string
}

// connectSys opens the system-account connection and starts listening.
func (m *Monitor) ConnectSys(url string, opts []nats.Option) error {
	m.DisconnectSys()

	all := make([]nats.Option, 0, len(opts)+4)
	all = append(all, opts...)
	all = append(all,
		nats.Name(sysConnName),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			m.sink.MonitorStatus(m.Status())
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			m.sink.MonitorStatus(m.Status())
		}),
	)

	nc, err := nats.Connect(url, all...)
	if err != nil {
		return fmt.Errorf("system account connection failed: %w", err)
	}

	// waitFor tells the scatter-gather how many replies to expect so it can
	// return as soon as they are all in rather than sitting out the timeout.
	// The server tells us, so a cluster that grows is picked up on the next
	// connect rather than being permanently under-counted.
	waitFor, err := serverdata.CurrentActiveServers(context.Background(), nc, sysReqTimeout, api.NewDiscardLogger())
	if err != nil || waitFor < 1 {
		// Not fatal: 0 means "wait the full timeout", which still works. It
		// is what happens on a server that answers PING but reports no
		// active count.
		waitFor = 0
	}

	live, err := serverdata.NewLive(nc, m.request, waitFor)
	if err != nil {
		nc.Close()
		return err
	}

	s := &sysConn{nc: nc, live: live, url: url}
	if err := m.subscribeEvents(s); err != nil {
		nc.Close()
		return fmt.Errorf("system account connected but its events are not readable: %w", err)
	}

	m.mu.Lock()
	m.sys = s
	m.mu.Unlock()

	// Populate the grid at once rather than waiting up to ten seconds for the
	// first STATSZ heartbeat.
	if err := m.RefreshServers(); err != nil {
		m.log.Warn("initial server scatter failed", "err", err)
	}

	m.sink.MonitorStatus(m.Status())
	return nil
}

// request is the RequestFunc serverdata.Live calls for every endpoint.
//
// It builds a fresh inbox per request. The alternative - one long-lived inbox
// keyed per poll, as nats-surveyor does - matters at surveyor's cadence and
// scale; here the grid is pushed by STATSZ and a scatter happens only when
// someone asks, so a subscription per request is not worth the bookkeeping.
func (m *Monitor) request(req any, subj string, waitFor int, nc *nats.Conn) ([][]byte, error) {
	// DoReqAsync calls log.Debugf with no nil check, so a logger is required
	// even when nothing should be logged.
	return serverdata.DoReq(context.Background(), req, subj, waitFor, nc, sysReqTimeout, api.NewDiscardLogger())
}

func (m *Monitor) subscribeEvents(s *sysConn) error {
	for _, ev := range sysEvents {
		kind := ev.kind
		sub, err := s.nc.Subscribe(ev.subject, func(msg *nats.Msg) {
			m.handleEvent(kind, msg)
		})
		if err != nil {
			for _, done := range s.subs {
				_ = done.Unsubscribe()
			}
			return err
		}
		s.subs = append(s.subs, sub)
	}
	return s.nc.Flush()
}

// handleEvent turns one $SYS message into a grid update, a feed entry, or
// both.
func (m *Monitor) handleEvent(kind string, msg *nats.Msg) {
	if kind == "statsz" {
		var stats server.ServerStatsMsg
		if err := json.Unmarshal(msg.Data, &stats); err != nil {
			m.log.Warn("undecodable STATSZ", "subject", msg.Subject, "err", err)
			return
		}
		m.grid.applyStatsz(&stats, "statsz")
		m.sink.MonitorServers(m.grid.rowsSorted())
		return
	}

	ev := Event{Kind: kind, At: time.Now(), Subject: msg.Subject}

	// Every other event carries a ServerInfo, which is what names the server
	// the feed shows. The body goes through as the server wrote it: these are
	// its own event types and inventing a shape for them here would only lose
	// fields.
	var envelope struct {
		Server server.ServerInfo `json:"server"`
	}
	if err := json.Unmarshal(msg.Data, &envelope); err == nil {
		ev.Server = envelope.Server.Name
		ev.ServerID = envelope.Server.ID
		ev.Cluster = envelope.Server.Cluster
	}
	ev.Data = json.RawMessage(msg.Data)

	// A server announcing it is going away is grid news, not just feed news.
	switch kind {
	case "shutdown", "lameduck":
		if m.grid.mark(ev.ServerID, kind) {
			m.sink.MonitorServers(m.grid.rowsSorted())
		}
	}

	m.sink.MonitorEvent(ev)
}

func (m *Monitor) DisconnectSys() {
	m.mu.Lock()
	s := m.sys
	m.sys = nil
	m.mu.Unlock()

	if s == nil {
		return
	}
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.nc.Close()

	// The rows came from that connection; keeping them would show a cluster
	// nats-desk can no longer see.
	m.grid.reset()
	m.sink.MonitorServers(nil)
	m.sink.MonitorStatus(m.Status())
}

func (m *Monitor) sysSource() (*sysConn, error) {
	m.mu.RLock()
	s := m.sys
	m.mu.RUnlock()
	if s == nil || s.nc.IsClosed() {
		return nil, ErrNoSysConn
	}
	return s, nil
}

// ErrNoSysConn is returned when an operation needs the system-account
// connection and it has not been configured.
var ErrNoSysConn = errors.New("no system account connection")
