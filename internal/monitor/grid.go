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

	// Topology, from STATSZ only - VARZ carries none of it. See RouteLink.
	//
	// RouteLinksKnown is what separates "this server has no routes" from "we
	// cannot say": it is false for a row from :8222, and for a server old
	// enough not to name its route peers. Only a known empty list may be read
	// as a missing route.
	RouteLinks      []RouteLink   `json:"route_links,omitempty"`
	RouteLinksKnown bool          `json:"route_links_known,omitempty"`
	GatewayLinks    []GatewayLink `json:"gateway_links,omitempty"`

	// Every JetStream server names the meta leader; only the leader lists the
	// peers and how far behind each one is.
	MetaLeader string     `json:"meta_leader,omitempty"`
	MetaSize   int        `json:"meta_size,omitempty"`
	MetaPeers  []MetaPeer `json:"meta_peers,omitempty"`
}

// RouteLink is one server's routes to one peer, summed over every route
// connection to it.
//
// STATSZ lists route *connections*, and since 2.10 a pair of servers keeps a
// pool of them - three by default, plus one per pinned account - so a
// three-node cluster reports 8. Grouping by the peer's name turns that back
// into the thing a person means by "a route".
type RouteLink struct {
	Peer     string `json:"peer"`
	Conns    int    `json:"conns"`
	InMsgs   int64  `json:"in_msgs"`
	OutMsgs  int64  `json:"out_msgs"`
	InBytes  int64  `json:"in_bytes"`
	OutBytes int64  `json:"out_bytes"`
	// Bytes queued to the peer and not yet written: a route that cannot keep
	// up shows here first.
	Pending int `json:"pending"`

	linkRates
}

// GatewayLink is one server's outbound gateway connection to another
// cluster, with the inbound connections from it.
type GatewayLink struct {
	Gateway  string `json:"gateway"`
	InMsgs   int64  `json:"in_msgs"`
	OutMsgs  int64  `json:"out_msgs"`
	InBytes  int64  `json:"in_bytes"`
	OutBytes int64  `json:"out_bytes"`
	Inbound  int    `json:"inbound"`

	linkRates
}

// linkRates are per second, between two STATSZ samples, on the reporting
// server's clock - the same rules as the row's own rates.
type linkRates struct {
	InMsgsRate   *float64 `json:"in_msgs_rate,omitempty"`
	OutMsgsRate  *float64 `json:"out_msgs_rate,omitempty"`
	InBytesRate  *float64 `json:"in_bytes_rate,omitempty"`
	OutBytesRate *float64 `json:"out_bytes_rate,omitempty"`
}

// MetaPeer is one JetStream meta-group member as the leader sees it.
type MetaPeer struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Offline bool   `json:"offline,omitempty"`
	Lag     uint64 `json:"lag,omitempty"`
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
	// Per server, the previous sample of each route and gateway link, keyed
	// "r:<peer>" or "g:<gateway>".
	lastLinks map[string]map[string]sample
}

func newGrid() *grid {
	return &grid{
		rows:      map[string]ServerRow{},
		last:      map[string]sample{},
		lastLinks: map[string]map[string]sample{},
	}
}

// routeLinks groups a server's route connections by peer. The second result
// is false when any connection does not name its peer - a server from before
// route names were reported - because a list with an anonymous entry cannot
// say which peer is missing.
func routeLinks(routes []*server.RouteStat) ([]RouteLink, bool) {
	known := true
	byPeer := map[string]*RouteLink{}
	var order []string
	for _, r := range routes {
		if r == nil {
			continue
		}
		if r.Name == "" {
			known = false
			continue
		}
		l, ok := byPeer[r.Name]
		if !ok {
			l = &RouteLink{Peer: r.Name}
			byPeer[r.Name] = l
			order = append(order, r.Name)
		}
		l.Conns++
		l.InMsgs += r.Received.Msgs
		l.OutMsgs += r.Sent.Msgs
		l.InBytes += r.Received.Bytes
		l.OutBytes += r.Sent.Bytes
		l.Pending += r.Pending
	}
	sort.Strings(order)
	out := make([]RouteLink, 0, len(order))
	for _, p := range order {
		out = append(out, *byPeer[p])
	}
	return out, known
}

func gatewayLinks(gws []*server.GatewayStat) []GatewayLink {
	byName := map[string]*GatewayLink{}
	var order []string
	for _, g := range gws {
		if g == nil || g.Name == "" {
			continue
		}
		l, ok := byName[g.Name]
		if !ok {
			l = &GatewayLink{Gateway: g.Name}
			byName[g.Name] = l
			order = append(order, g.Name)
		}
		l.InMsgs += g.Received.Msgs
		l.OutMsgs += g.Sent.Msgs
		l.InBytes += g.Received.Bytes
		l.OutBytes += g.Sent.Bytes
		l.Inbound += g.NumInbound
	}
	sort.Strings(order)
	out := make([]GatewayLink, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out
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

	row.RouteLinks, row.RouteLinksKnown = routeLinks(s.Routes)
	row.GatewayLinks = gatewayLinks(s.Gateways)
	if js := s.JetStream; js != nil && js.Meta != nil {
		row.MetaLeader = js.Meta.Leader
		row.MetaSize = js.Meta.Size
		for _, p := range js.Meta.Replicas {
			if p != nil {
				row.MetaPeers = append(row.MetaPeers, MetaPeer{
					Name: p.Name, Current: p.Current, Offline: p.Offline, Lag: p.Lag,
				})
			}
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.rate(&row)
	g.linkRates(&row)
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
	row.InMsgsRate, row.OutMsgsRate, row.InBytesRate, row.OutBytesRate = perSecond(prev, now)
}

// perSecond is the rate between two samples, or all nils when there is no
// honest one.
//
// The same sample twice, or a clock that went backwards, leaves no interval to
// divide by. A counter that went down means the server - or for a link, the
// connection - started again, so the difference is not a rate of anything:
// skip this interval and measure from the next.
func perSecond(prev, now sample) (inMsgs, outMsgs, inBytes, outBytes *float64) {
	dt := now.at.Sub(prev.at).Seconds()
	if dt <= 0 || now.inMsgs < prev.inMsgs || now.outMsgs < prev.outMsgs {
		return nil, nil, nil, nil
	}
	per := func(a, b int64) *float64 {
		v := float64(a-b) / dt
		return &v
	}
	return per(now.inMsgs, prev.inMsgs), per(now.outMsgs, prev.outMsgs),
		per(now.inBytes, prev.inBytes), per(now.outBytes, prev.outBytes)
}

// linkRates does for each route and gateway link what rate does for the row,
// against the same server timestamp. Caller holds the lock.
//
// A link seen for the first time has no rate yet, and a link that has gone is
// forgotten, so one that comes back starts again from no rate rather than
// from a stale sample.
func (g *grid) linkRates(row *ServerRow) {
	prev := g.lastLinks[row.ID]
	next := make(map[string]sample, len(row.RouteLinks)+len(row.GatewayLinks))

	measure := func(key string, inM, outM, inB, outB int64, r *linkRates) {
		now := sample{at: row.Seen, inMsgs: inM, outMsgs: outM, inBytes: inB, outBytes: outB}
		next[key] = now
		if p, ok := prev[key]; ok {
			r.InMsgsRate, r.OutMsgsRate, r.InBytesRate, r.OutBytesRate = perSecond(p, now)
		}
	}
	for i := range row.RouteLinks {
		l := &row.RouteLinks[i]
		measure("r:"+l.Peer, l.InMsgs, l.OutMsgs, l.InBytes, l.OutBytes, &l.linkRates)
	}
	for i := range row.GatewayLinks {
		l := &row.GatewayLinks[i]
		measure("g:"+l.Gateway, l.InMsgs, l.OutMsgs, l.InBytes, l.OutBytes, &l.linkRates)
	}
	g.lastLinks[row.ID] = next
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
		delete(g.lastLinks, id)
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
