// Package natsconn owns the NATS connection.
//
// This is the piece that only exists because the client moved out of the
// browser: nats.go speaks the native protocol on 4222, so it reaches any
// server without the websocket listener a browser would have needed.
package natsconn

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrNotConnected is returned by every operation that needs a live
// connection. The API layer maps it to 409 so the UI can tell "you are not
// connected" apart from "the server said no".
var ErrNotConnected = errors.New("not connected")

const statsPollInterval = 2 * time.Second

// Sink receives everything the manager pushes out. The WebSocket hub
// implements it; nothing in this package knows about HTTP.
type Sink interface {
	Message(subID uint64, subject string, data []byte, headers nats.Header)
	Status(state string, err error)
	Stats(rttMillis float64)
	KvChange(key, operation string)
	TailMessage(msg TailMsg)
}

// TailMsg is one message from a live stream tail.
type TailMsg struct {
	Seq     uint64      `json:"seq"`
	Subject string      `json:"subject"`
	Data    []byte      `json:"-"`
	Time    time.Time   `json:"time"`
	Headers nats.Header `json:"headers,omitempty"`
}

type subscription struct {
	sub           *nats.Subscription
	subject       string
	excludeSystem bool
}

type Manager struct {
	sink Sink

	mu        sync.RWMutex
	nc        *nats.Conn
	subs      map[uint64]*subscription
	nextSubID uint64
	stopStats chan struct{}

	// JetStream state. One connection has at most one open KV bucket and one
	// live stream tail, matching what the UI can show at once.
	jsHandle  jetstream.JetStream
	kv        jetstream.KeyValue
	kvWatcher jetstream.KeyWatcher
	tail      jetstream.ConsumeContext
}

func New(sink Sink) *Manager {
	return &Manager{sink: sink, subs: map[uint64]*subscription{}}
}

// AuthOptions mirrors what the connection form collects.
type AuthOptions struct {
	CredsText string
	User      string
	Pass      string
	Token     string
}

// Connect replaces any existing connection.
func (m *Manager) Connect(url string, auth AuthOptions) error {
	m.Disconnect()

	opts := []nats.Option{
		nats.Name("nats-desk"),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			m.sink.Status("reconnecting", err)
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			m.sink.Status("connected", nil)
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			m.sink.Status("disconnected", nc.LastError())
		}),
	}

	authOpt, err := auth.option()
	if err != nil {
		return err
	}
	if authOpt != nil {
		opts = append(opts, authOpt)
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return friendlyConnectError(err)
	}

	m.mu.Lock()
	m.nc = nc
	m.nextSubID = 0
	m.stopStats = make(chan struct{})
	stop := m.stopStats
	m.mu.Unlock()

	go m.pollStats(nc, stop)
	// Deliberately no Status("connected") here: the caller of Connect gets
	// the answer synchronously, and pushing it as well made the UI toast a
	// "Reconnected" it had never disconnected from. Only the reconnect and
	// close handlers push status.
	return nil
}

// option turns the collected credentials into a nats.Option.
//
// Order matches the old client: a creds file wins, then a token, then
// user/password. Anything absent simply means an anonymous connection.
func (a AuthOptions) option() (nats.Option, error) {
	switch {
	case strings.TrimSpace(a.CredsText) != "":
		// The UI hands us the file's contents, not a path. nats.go's
		// UserCredentials only reads paths, so the JWT and seed are pulled
		// out here rather than staging a temp file - which would mean
		// writing a credential to disk to satisfy an API shape.
		raw := []byte(strings.ReplaceAll(a.CredsText, "\r\n", "\n"))
		userJWT, err := jwt.ParseDecoratedJWT(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: %w", err)
		}
		kp, err := jwt.ParseDecoratedNKey(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: no seed found: %w", err)
		}
		seed, err := kp.Seed()
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: %w", err)
		}
		return nats.UserJWTAndSeed(userJWT, string(seed)), nil

	case a.Token != "":
		return nats.Token(a.Token), nil

	case a.User != "":
		return nats.UserInfo(a.User, a.Pass), nil
	}
	return nil, nil
}

// friendlyConnectError turns nats.go's wording into something a UI can show.
func friendlyConnectError(err error) error {
	switch {
	case errors.Is(err, nats.ErrAuthorization):
		return errors.New("authentication failed - check credentials")
	case errors.Is(err, nats.ErrNoServers):
		return errors.New("cannot reach the NATS server - check the URL and that it is running")
	default:
		return fmt.Errorf("connection failed: %w", err)
	}
}

func (m *Manager) Disconnect() {
	m.stopKvWatcher()
	m.StopStreamTail()

	m.mu.Lock()
	nc := m.nc
	stop := m.stopStats
	m.nc = nil
	m.stopStats = nil
	m.subs = map[uint64]*subscription{}
	m.jsHandle = nil
	m.kv = nil
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if nc != nil {
		// Drain would wait for in-flight handlers; Close is what the UI
		// means by "disconnect now".
		nc.Close()
	}
}

func (m *Manager) conn() (*nats.Conn, error) {
	m.mu.RLock()
	nc := m.nc
	m.mu.RUnlock()
	if nc == nil || nc.IsClosed() {
		return nil, ErrNotConnected
	}
	return nc, nil
}

func (m *Manager) IsConnected() bool {
	_, err := m.conn()
	return err == nil
}

// ServerInfo is what the connection popover shows.
func (m *Manager) ServerInfo() (map[string]any, error) {
	nc, err := m.conn()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"server_id":   nc.ConnectedServerId(),
		"server_name": nc.ConnectedServerName(),
		"version":     nc.ConnectedServerVersion(),
		"cluster":     nc.ConnectedClusterName(),
		"url":         nc.ConnectedUrl(),
		"addr":        nc.ConnectedAddr(),
		"tls":         nc.TLSRequired(),
		"headers":     nc.HeadersSupported(),
		"max_payload": nc.MaxPayload(),
	}, nil
}

func (m *Manager) pollStats(nc *nats.Conn, stop <-chan struct{}) {
	t := time.NewTicker(statsPollInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if nc.IsClosed() {
				return
			}
			rtt, err := nc.RTT()
			if err != nil {
				continue
			}
			m.sink.Stats(float64(rtt.Microseconds()) / 1000.0)
		}
	}
}

// ============================================================================
// PUB / SUB
// ============================================================================

type SubResult struct {
	ID            uint64 `json:"id"`
	Subject       string `json:"subject"`
	ExcludeSystem bool   `json:"excludeSystem"`
	Size          int    `json:"size"`
}

// Subscribe registers a subscription and streams its messages to the sink.
func (m *Manager) Subscribe(subject string, excludeSystem bool) (SubResult, error) {
	nc, err := m.conn()
	if err != nil {
		return SubResult{}, err
	}

	// Honour the request only where it is meaningful, so the flag reported
	// back - and shown in the UI - is what is actually happening.
	exclude := excludeSystem && CanExcludeSystem(subject)

	m.mu.Lock()
	m.nextSubID++
	id := m.nextSubID
	m.mu.Unlock()

	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		if exclude && IsSystemSubject(msg.Subject) {
			return
		}
		m.sink.Message(id, msg.Subject, msg.Data, msg.Header)
	})
	if err != nil {
		return SubResult{}, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	m.mu.Lock()
	m.subs[id] = &subscription{sub: sub, subject: subject, excludeSystem: exclude}
	size := len(m.subs)
	m.mu.Unlock()

	return SubResult{ID: id, Subject: subject, ExcludeSystem: exclude, Size: size}, nil
}

func (m *Manager) Unsubscribe(id uint64) (SubResult, error) {
	m.mu.Lock()
	s, ok := m.subs[id]
	if ok {
		delete(m.subs, id)
	}
	size := len(m.subs)
	m.mu.Unlock()

	if !ok {
		return SubResult{Size: size}, nil
	}
	if err := s.sub.Unsubscribe(); err != nil {
		return SubResult{Size: size}, fmt.Errorf("failed to unsubscribe: %w", err)
	}
	return SubResult{ID: id, Subject: s.subject, ExcludeSystem: s.excludeSystem, Size: size}, nil
}

func (m *Manager) Publish(subject string, data []byte, headers nats.Header) error {
	nc, err := m.conn()
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: headers}
	if err := nc.PublishMsg(msg); err != nil {
		return fmt.Errorf("failed to publish: %w", err)
	}
	// Publish is buffered; without this the UI would report success before
	// the bytes have left the process, and a failing subject would surface
	// only much later as an async error.
	return nc.Flush()
}

type Reply struct {
	Subject string      `json:"subject"`
	Data    []byte      `json:"-"`
	Headers nats.Header `json:"-"`
}

func (m *Manager) Request(subject string, data []byte, headers nats.Header, timeout time.Duration) (*nats.Msg, error) {
	nc, err := m.conn()
	if err != nil {
		return nil, err
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: headers}
	reply, err := nc.RequestMsg(msg, timeout)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			return nil, fmt.Errorf("no responder is listening on %s", subject)
		}
		if errors.Is(err, nats.ErrTimeout) {
			return nil, fmt.Errorf("request timed out after %s", timeout)
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return reply, nil
}
