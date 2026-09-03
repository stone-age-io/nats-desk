package monitor

import (
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// ServerRow is one line of the cluster grid.
//
// The counters are the server's own totals since it started; the *Rate fields
// are what nats-desk works out between two samples. A rate is a pointer
// because "we have not seen a second sample yet" and "the rate is zero" are
// different facts, and a grid that shows 0 msg/s for a busy server it has only
// just met is lying.
type ServerRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Cluster string `json:"cluster,omitempty"`
	Version string `json:"version"`
	Domain  string `json:"domain,omitempty"`

	Start time.Time `json:"start"`
	Seen  time.Time `json:"seen"`

	Connections      int    `json:"connections"`
	TotalConnections uint64 `json:"total_connections"`
	Subscriptions    uint32 `json:"subscriptions"`
	Routes           int    `json:"routes"`
	Gateways         int    `json:"gateways"`
	SlowConsumers    int64  `json:"slow_consumers"`
	ActiveAccounts   int    `json:"active_accounts"`

	CPU   float64 `json:"cpu"`
	Cores int     `json:"cores"`
	Mem   int64   `json:"mem"`

	InMsgs   int64 `json:"in_msgs"`
	OutMsgs  int64 `json:"out_msgs"`
	InBytes  int64 `json:"in_bytes"`
	OutBytes int64 `json:"out_bytes"`

	InMsgsRate   *float64 `json:"in_msgs_rate,omitempty"`
	OutMsgsRate  *float64 `json:"out_msgs_rate,omitempty"`
	InBytesRate  *float64 `json:"in_bytes_rate,omitempty"`
	OutBytesRate *float64 `json:"out_bytes_rate,omitempty"`

	JetStream bool `json:"jetstream"`

	// How this row arrived: "statsz" for a pushed event, "ping" for a
	// scatter-gather we asked for, "http" for the :8222 endpoints.
	Source string `json:"source"`

	// Set when the server told us it is going away, so the grid can show it
	// as gone instead of just letting the row go stale.
	State string `json:"state,omitempty"`
}

type sample struct {
	at       time.Time
	inMsgs   int64
	outMsgs  int64
	inBytes  int64
	outBytes int64
}

// grid holds the current row per server and the previous sample each rate is
// measured against.
type grid struct {
	mu   sync.Mutex
	rows map[string]ServerRow
	last map[string]sample
}

func newGrid() *grid {
	return &grid{rows: map[string]ServerRow{}, last: map[string]sample{}}
}

// applyStatsz folds one STATSZ - pushed or requested - into the grid.
func (g *grid) applyStatsz(m *server.ServerStatsMsg, source string) {
	s := m.Stats
	row := ServerRow{
		ID:      m.Server.ID,
		Name:    m.Server.Name,
		Host:    m.Server.Host,
		Cluster: m.Server.Cluster,
		Version: m.Server.Version,
		Domain:  m.Server.Domain,

		Start: s.Start,
		Seen:  m.Server.Time,

		Connections:      s.Connections,
		TotalConnections: s.TotalConnections,
		Subscriptions:    s.NumSubs,
		Routes:           len(s.Routes),
		Gateways:         len(s.Gateways),
		SlowConsumers:    s.SlowConsumers,
		ActiveAccounts:   s.ActiveAccounts,

		CPU:   s.CPU,
		Cores: s.Cores,
		Mem:   s.Mem,

		InMsgs:   s.Received.Msgs,
		OutMsgs:  s.Sent.Msgs,
		InBytes:  s.Received.Bytes,
		OutBytes: s.Sent.Bytes,

		JetStream: m.Server.JetStream,
		Source:    source,
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.rate(&row)
	g.rows[row.ID] = row
}

// applyVarz folds one VARZ response into the grid. Varz carries the same
// counters under different names, plus a few things STATSZ does not have.
func (g *grid) applyVarz(v *server.Varz, source string) {
	row := ServerRow{
		ID:      v.ID,
		Name:    v.Name,
		Host:    v.Host,
		Cluster: v.Cluster.Name,
		Version: v.Version,

		Start: v.Start,
		Seen:  v.Now,

		Connections:      v.Connections,
		TotalConnections: v.TotalConnections,
		Subscriptions:    v.Subscriptions,
		Routes:           v.Routes,
		Gateways:         len(v.Gateway.Gateways),
		SlowConsumers:    v.SlowConsumers,

		CPU:   v.CPU,
		Cores: v.Cores,
		Mem:   v.Mem,

		InMsgs:   v.InMsgs,
		OutMsgs:  v.OutMsgs,
		InBytes:  v.InBytes,
		OutBytes: v.OutBytes,

		JetStream: v.JetStream.Config != nil,
		Source:    source,
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.rate(&row)
	g.rows[row.ID] = row
}

// rate fills in the per-second fields from the previous sample, and records
// this one. Caller holds the lock.
//
// The clock is the *server's* own timestamp, not ours: the interval we care
// about is the one the counters were accumulated over, and our clock may be
// skewed against it or against the other servers in the grid.
func (g *grid) rate(row *ServerRow) {
	prev, ok := g.last[row.ID]
	now := sample{
		at:       row.Seen,
		inMsgs:   row.InMsgs,
		outMsgs:  row.OutMsgs,
		inBytes:  row.InBytes,
		outBytes: row.OutBytes,
	}
	g.last[row.ID] = now

	if !ok {
		return
	}

	dt := now.at.Sub(prev.at).Seconds()
	if dt <= 0 {
		// The same sample twice, or a clock that went backwards. Either way
		// there is no interval to divide by.
		return
	}

	// A counter that went down means the server restarted, so the difference
	// is not a rate of anything. Skip this interval and measure from the next.
	if now.inMsgs < prev.inMsgs || now.outMsgs < prev.outMsgs {
		return
	}

	perSec := func(a, b int64) *float64 {
		v := float64(a-b) / dt
		return &v
	}
	row.InMsgsRate = perSec(now.inMsgs, prev.inMsgs)
	row.OutMsgsRate = perSec(now.outMsgs, prev.outMsgs)
	row.InBytesRate = perSec(now.inBytes, prev.inBytes)
	row.OutBytesRate = perSec(now.outBytes, prev.outBytes)
}

// prune drops rows that a census did not account for.
//
// A scatter-gather is a complete list of who is answering right now, so
// anything missing from it has stopped answering. This matters because a
// server that is killed rather than shut down never sends SHUTDOWN, and
// because a restarted server comes back with a *new* ID - NATS generates one
// per process - so its old row would otherwise sit in the grid forever.
//
// Only rows from the same family of sources are pruned: a $SYS census knows
// nothing about servers reached over :8222 and must not evict them.
func (g *grid) prune(seen map[string]bool, sources ...string) {
	from := map[string]bool{}
	for _, s := range sources {
		from[s] = true
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	for id, row := range g.rows {
		if seen[id] || !from[row.Source] {
			continue
		}
		delete(g.rows, id)
		delete(g.last, id)
	}
}

// mark records that a server announced it is shutting down or entering lame
// duck mode. The row stays, because a grid that silently loses a line does not
// tell you a server went away.
func (g *grid) mark(id, state string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	row, ok := g.rows[id]
	if !ok {
		return false
	}
	row.State = state
	g.rows[id] = row
	return true
}

// rowsSorted returns the grid, ordered by cluster then name so a row does not
// jump around between updates.
func (g *grid) rowsSorted() []ServerRow {
	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]ServerRow, 0, len(g.rows))
	for _, r := range g.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (g *grid) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rows = map[string]ServerRow{}
	g.last = map[string]sample{}
}
