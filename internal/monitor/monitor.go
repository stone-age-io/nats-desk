// Package monitor gives three independent views of a NATS deployment.
//
//   - the data connection, which every account gets for free
//   - a separate system-account connection, which sees the whole cluster
//   - the :8222 monitoring endpoints, which need no NATS credentials at all
//
// They are separate on purpose, and not interchangeable. A nats.Conn is bound
// to one account for its lifetime, so a system-account view is *mandatorily* a
// second connection - not a preference. Only $SYS fans out across a cluster in
// one request and pushes events as they happen; only :8222 works when nobody
// has provisioned a system user. Neither is reliably present, which is why
// both are here and each is configured on its own.
package monitor

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Sink is how monitoring reaches the browser. The WebSocket hub implements it.
type Sink interface {
	// MonitorServers pushes the whole grid whenever a row changes.
	MonitorServers(rows []ServerRow)
	// MonitorEvent pushes one cluster event as it happens.
	MonitorEvent(ev Event)
	// MonitorStatus pushes which sources are live.
	MonitorStatus(st Status)
}

// Event is one thing that happened in the cluster: a client connected, a
// server went into lame duck mode, an authentication failed.
type Event struct {
	Kind     string          `json:"kind"`
	At       time.Time       `json:"at"`
	Subject  string          `json:"subject"`
	Server   string          `json:"server,omitempty"`
	ServerID string          `json:"server_id,omitempty"`
	Cluster  string          `json:"cluster,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Status is what the sources panel shows.
type Status struct {
	Data bool       `json:"data"` // the app's own connection is up
	Sys  SysStatus  `json:"sys"`
	HTTP HTTPStatus `json:"http"`
}

type SysStatus struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	URL        string `json:"url,omitempty"`
	Servers    int    `json:"servers,omitempty"`
}

type HTTPStatus struct {
	Configured bool     `json:"configured"`
	Bases      []string `json:"bases,omitempty"`
}

// DataConn is the app's own NATS connection, borrowed for the account-scoped
// endpoints. Only the connection is needed, so only the connection is asked
// for - the monitor has no business reaching into the rest of the manager.
type DataConn interface {
	Conn() (*nats.Conn, error)
}

type Monitor struct {
	sink Sink
	log  *slog.Logger
	data DataConn
	grid *grid

	mu   sync.RWMutex
	sys  *sysConn
	http *httpSource
}

func New(data DataConn, sink Sink, log *slog.Logger) *Monitor {
	return &Monitor{data: data, sink: sink, log: log, grid: newGrid()}
}

func (m *Monitor) Status() Status {
	m.mu.RLock()
	sys, httpSrc := m.sys, m.http
	m.mu.RUnlock()

	st := Status{}
	if m.data != nil {
		_, err := m.data.Conn()
		st.Data = err == nil
	}
	if sys != nil {
		st.Sys = SysStatus{
			Configured: true,
			Connected:  !sys.nc.IsClosed() && sys.nc.IsConnected(),
			URL:        sys.url,
			Servers:    len(m.grid.rowsSorted()),
		}
	}
	if httpSrc != nil {
		st.HTTP = HTTPStatus{Configured: true, Bases: httpSrc.bases}
	}
	return st
}

// Servers returns the grid as it stands, without asking anyone anything.
func (m *Monitor) Servers() []ServerRow {
	return m.grid.rowsSorted()
}

// RefreshServers asks every server for its stats now, rather than waiting for
// the next pushed heartbeat. Uses $SYS when it is available and the :8222
// endpoints otherwise.
func (m *Monitor) RefreshServers() error {
	if s, err := m.sysSource(); err == nil {
		stats, err := s.live.Statz(server.StatszEventOptions{})
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(stats))
		for _, st := range stats {
			m.grid.applyStatsz(st, "ping")
			seen[st.Server.ID] = true
		}
		m.grid.prune(seen, "statsz", "ping")
		m.sink.MonitorServers(m.grid.rowsSorted())
		return nil
	}

	m.mu.RLock()
	hasHTTP := m.http != nil
	m.mu.RUnlock()
	if hasHTTP {
		_, err := m.FetchHTTP("varz", nil)
		return err
	}

	return ErrNoSource
}

// ErrNoSource means neither a system connection nor a monitoring URL is set up.
var ErrNoSource = errors.New("no monitoring source is configured")

// Endpoint runs one of the monitoring endpoints against every server.
//
// The reply is whatever the server sent, passed straight through. These are
// the server's own documented shapes and they gain fields with every release;
// re-modelling them here would mean the UI showing less than the server said.
func (m *Monitor) Endpoint(name string) (any, error) {
	s, err := m.sysSource()
	if err != nil {
		return nil, err
	}

	switch name {
	case "varz":
		return s.live.Varz(server.VarzEventOptions{})
	case "connz":
		return s.live.Connz(server.ConnzEventOptions{})
	case "jsz":
		return s.live.Jsz(server.JszEventOptions{})
	case "healthz":
		return s.live.Healthz(server.HealthzEventOptions{})
	case "routez":
		return s.live.Routez(server.RoutezEventOptions{})
	case "subsz":
		return s.live.Subsz(server.SubszEventOptions{})
	case "accountz":
		return s.live.Accountz(server.AccountzEventOptions{})
	case "leafz":
		return s.live.Leafz(server.LeafzEventOptions{})
	case "gatewayz":
		return s.live.Gatewayz(server.GatewayzEventOptions{})
	default:
		return nil, errors.New("unknown endpoint: " + name)
	}
}

func (m *Monitor) Close() {
	m.DisconnectSys()
}
