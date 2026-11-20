package monitor

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

type fakeSink struct {
	servers [][]ServerRow
	events  []Event
	status  []Status
}

func (f *fakeSink) MonitorServers(rows []ServerRow) { f.servers = append(f.servers, rows) }
func (f *fakeSink) MonitorEvent(ev Event)           { f.events = append(f.events, ev) }
func (f *fakeSink) MonitorStatus(st Status)         { f.status = append(f.status, st) }

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func statsz(id string, at time.Time, in, out int64) *server.ServerStatsMsg {
	m := &server.ServerStatsMsg{}
	m.Server.ID = id
	m.Server.Name = id
	m.Server.Time = at
	m.Stats.Received.Msgs = in
	m.Stats.Sent.Msgs = out
	return m
}

func row(t *testing.T, g *grid, id string) ServerRow {
	t.Helper()
	for _, r := range g.rowsSorted() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no row for %q", id)
	return ServerRow{}
}

// The counters are totals since the server started, so a rate only exists once
// there are two samples to subtract. Reporting 0/sec for a server we have just
// met would be a made-up number presented as a measurement.
func TestRateNeedsTwoSamples(t *testing.T) {
	g := newGrid()
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	g.applyStatsz(statsz("A", t0, 100, 200), "statsz")
	if r := row(t, g, "A"); r.InMsgsRate != nil || r.OutMsgsRate != nil {
		t.Fatalf("first sample produced a rate: in=%v out=%v", r.InMsgsRate, r.OutMsgsRate)
	}

	g.applyStatsz(statsz("A", t0.Add(10*time.Second), 160, 260), "statsz")
	r := row(t, g, "A")
	if r.InMsgsRate == nil || *r.InMsgsRate != 6 {
		t.Errorf("in rate = %v, want 6/sec", r.InMsgsRate)
	}
	if r.OutMsgsRate == nil || *r.OutMsgsRate != 6 {
		t.Errorf("out rate = %v, want 6/sec", r.OutMsgsRate)
	}
}

// The interval is measured with the server's own clock. Ours may be skewed
// against it, and in a cluster the rows come from several machines at once.
func TestRateUsesTheServerClock(t *testing.T) {
	g := newGrid()
	t0 := time.Now().Add(-72 * time.Hour) // nowhere near our clock

	g.applyStatsz(statsz("A", t0, 0, 0), "statsz")
	g.applyStatsz(statsz("A", t0.Add(4*time.Second), 40, 0), "statsz")

	r := row(t, g, "A")
	if r.InMsgsRate == nil || *r.InMsgsRate != 10 {
		t.Errorf("in rate = %v, want 10/sec measured over the server's own 4s", r.InMsgsRate)
	}
}

// A counter that went backwards means the process restarted. The difference is
// then not a rate of anything, and dividing it out would print a large
// negative number where the truth is "we do not know yet".
func TestRateSkipsARestartThenRecovers(t *testing.T) {
	g := newGrid()
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	g.applyStatsz(statsz("A", t0, 1000, 1000), "statsz")
	g.applyStatsz(statsz("A", t0.Add(10*time.Second), 5, 5), "statsz")
	if r := row(t, g, "A"); r.InMsgsRate != nil {
		t.Errorf("a restart produced a rate: %v", *r.InMsgsRate)
	}

	// The sample after the restart has a usable interval again.
	g.applyStatsz(statsz("A", t0.Add(20*time.Second), 25, 5), "statsz")
	if r := row(t, g, "A"); r.InMsgsRate == nil || *r.InMsgsRate != 2 {
		t.Errorf("in rate = %v, want 2/sec once measuring resumes", r.InMsgsRate)
	}
}

func route(peer string, in, out int64, pending int) *server.RouteStat {
	r := &server.RouteStat{Name: peer, Pending: pending}
	r.Received.Msgs, r.Sent.Msgs = in, out
	return r
}

// A pair of servers keeps a pool of route connections, three by default plus
// one per pinned account, and STATSZ lists each one. A link is the peer, so
// the pool has to come back as one entry with its counters summed - otherwise
// a three-node cluster reads as 8 routes.
func TestRouteLinksSumThePoolPerPeer(t *testing.T) {
	g := newGrid()
	m := statsz("A", time.Now(), 0, 0)
	m.Stats.Routes = []*server.RouteStat{
		route("n2", 1, 10, 0), route("n2", 2, 20, 100), route("n2", 3, 30, 0), route("n2", 4, 40, 0),
		route("n3", 5, 50, 7),
	}
	g.applyStatsz(m, "statsz")

	r := row(t, g, "A")
	if !r.RouteLinksKnown {
		t.Fatal("every route named its peer, yet the links are marked unknown")
	}
	if len(r.RouteLinks) != 2 {
		t.Fatalf("links = %+v, want one per peer", r.RouteLinks)
	}
	n2 := r.RouteLinks[0]
	if n2.Peer != "n2" || n2.Conns != 4 || n2.InMsgs != 10 || n2.OutMsgs != 100 || n2.Pending != 100 {
		t.Errorf("n2 = %+v, want 4 conns, 10 in, 100 out, 100 pending", n2)
	}
}

// A route with no peer name - a server from before names were reported -
// means the list cannot say which peer is missing, so it must not claim to.
func TestAnUnnamedRouteMakesTheLinksUnknown(t *testing.T) {
	g := newGrid()
	m := statsz("A", time.Now(), 0, 0)
	m.Stats.Routes = []*server.RouteStat{route("n2", 0, 0, 0), route("", 0, 0, 0)}
	g.applyStatsz(m, "statsz")

	if r := row(t, g, "A"); r.RouteLinksKnown {
		t.Error("a list with an anonymous route was marked complete")
	}
}

// Link rates follow the row's rules: none on first sight, the server's clock
// for the interval, and none across a reset. A route connection that drops
// and comes back starts its counters again, which must not print as a large
// negative rate.
func TestRouteLinkRates(t *testing.T) {
	g := newGrid()
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration, out int64) {
		m := statsz("A", t0.Add(d), 0, 0)
		m.Stats.Routes = []*server.RouteStat{route("n2", 0, out, 0)}
		g.applyStatsz(m, "statsz")
	}
	link := func() RouteLink { return row(t, g, "A").RouteLinks[0] }

	at(0, 100)
	if link().OutMsgsRate != nil {
		t.Fatal("first sample produced a link rate")
	}
	at(10*time.Second, 600)
	if r := link().OutMsgsRate; r == nil || *r != 50 {
		t.Errorf("out rate = %v, want 50/sec", r)
	}
	at(20*time.Second, 30)
	if r := link().OutMsgsRate; r != nil {
		t.Errorf("a reset route produced a rate: %v", *r)
	}
}

func TestMetaLeaderAndPeers(t *testing.T) {
	g := newGrid()
	m := statsz("A", time.Now(), 0, 0)
	m.Stats.JetStream = &server.JetStreamVarz{Meta: &server.MetaClusterInfo{
		Leader: "n1",
		Size:   3,
		Replicas: []*server.PeerInfo{
			{Name: "n2", Current: true},
			{Name: "n3", Offline: true, Lag: 12},
		},
	}}
	g.applyStatsz(m, "statsz")

	r := row(t, g, "A")
	if r.MetaLeader != "n1" || r.MetaSize != 3 || len(r.MetaPeers) != 2 {
		t.Fatalf("meta = %q/%d/%+v", r.MetaLeader, r.MetaSize, r.MetaPeers)
	}
	if p := r.MetaPeers[1]; p.Name != "n3" || !p.Offline || p.Lag != 12 {
		t.Errorf("n3 = %+v, want offline with lag 12", p)
	}
}

func TestRateIgnoresARepeatedSample(t *testing.T) {
	g := newGrid()
	t0 := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	g.applyStatsz(statsz("A", t0, 10, 10), "statsz")
	g.applyStatsz(statsz("A", t0, 10, 10), "statsz")

	if r := row(t, g, "A"); r.InMsgsRate != nil {
		t.Errorf("dividing by a zero interval produced %v", *r.InMsgsRate)
	}
}

// A census is a complete list of who answered, so anything missing has gone
// away - but only for the sources that census covers. Evicting a server the
// :8222 side is reporting because a $SYS scatter did not mention it would blank
// out rows that are perfectly current.
func TestPruneOnlyTouchesTheCensusedSources(t *testing.T) {
	g := newGrid()
	now := time.Now()

	g.applyStatsz(statsz("live", now, 0, 0), "statsz")
	g.applyStatsz(statsz("gone", now, 0, 0), "statsz")
	g.applyVarz(&server.Varz{ID: "http-only", Name: "http-only", Now: now}, "http")

	g.prune(map[string]bool{"live": true}, "statsz", "ping")

	got := map[string]bool{}
	for _, r := range g.rowsSorted() {
		got[r.ID] = true
	}
	if !got["live"] {
		t.Error("the server that answered was pruned")
	}
	if got["gone"] {
		t.Error("the server that did not answer is still in the grid")
	}
	if !got["http-only"] {
		t.Error("a row from the HTTP source was pruned by a $SYS census")
	}
}

// A server announcing that it is going away is grid news, not just feed news:
// its row has to say so rather than sitting there looking healthy until it
// goes stale.
func TestShutdownMarksTheRow(t *testing.T) {
	sink := &fakeSink{}
	m := New(nil, sink, discard())
	m.grid.applyStatsz(statsz("A", time.Now(), 0, 0), "statsz")

	body, err := json.Marshal(map[string]any{
		"server": map[string]any{"id": "A", "name": "n1", "cluster": "c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.handleEvent("shutdown", &nats.Msg{Subject: "$SYS.SERVER.A.SHUTDOWN", Data: body})

	if r := row(t, m.grid, "A"); r.State != "shutdown" {
		t.Errorf("row state = %q, want shutdown", r.State)
	}
	if len(sink.events) != 1 {
		t.Fatalf("pushed %d events, want 1", len(sink.events))
	}
	if ev := sink.events[0]; ev.Server != "n1" || ev.Cluster != "c1" {
		t.Errorf("event named the wrong server: %+v", ev)
	}
	if len(sink.servers) == 0 {
		t.Error("the grid change was not pushed")
	}
}

// A STATSZ is a grid update, not a line in the event feed - it arrives from
// every server every ten seconds and would drown everything worth reading.
func TestStatszDoesNotReachTheEventFeed(t *testing.T) {
	sink := &fakeSink{}
	m := New(nil, sink, discard())

	body, err := json.Marshal(statsz("A", time.Now(), 1, 2))
	if err != nil {
		t.Fatal(err)
	}
	m.handleEvent("statsz", &nats.Msg{Subject: "$SYS.SERVER.A.STATSZ", Data: body})

	if len(sink.events) != 0 {
		t.Errorf("STATSZ produced %d feed events, want 0", len(sink.events))
	}
	if len(sink.servers) != 1 {
		t.Errorf("pushed %d grid updates, want 1", len(sink.servers))
	}
}

func TestSetHTTPValidatesURLs(t *testing.T) {
	tests := []struct {
		name  string
		bases []string
		ok    bool
	}{
		{"plain", []string{"http://localhost:8222"}, true},
		{"tls", []string{"https://nats.example.net:8222"}, true},
		{"trailing slash is trimmed", []string{"http://localhost:8222/"}, true},
		{"blank entries are skipped", []string{"", "http://localhost:8222", "  "}, true},

		{"empty", nil, false},
		{"only blanks", []string{"", " "}, false},
		{"no scheme", []string{"localhost:8222"}, false},
		{"wrong scheme", []string{"nats://localhost:4222"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(nil, &fakeSink{}, discard())
			err := m.SetHTTP(tt.bases, HTTPOptions{})
			if tt.ok && err != nil {
				t.Fatalf("rejected a usable base: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("accepted an unusable base")
			}
		})
	}
}

func TestSetHTTPRejectsBadCA(t *testing.T) {
	m := New(nil, &fakeSink{}, discard())
	err := m.SetHTTP([]string{"https://localhost:8222"}, HTTPOptions{CA: "this is not a certificate"})
	if err == nil {
		t.Fatal("accepted a CA that is not PEM")
	}
}

// healthz is the one endpoint whose body does not name its server, and it is
// the one where "which server is unhealthy" matters most. Its answer has to be
// labelled with the server last seen at that base. connz names the server by
// ID only, so it must keep the name varz supplied rather than dropping it.
func TestHTTPAnswersNameTheirServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/varz":
			fmt.Fprint(w, `{"server_id":"NABC","server_name":"n1","now":"2026-11-19T12:00:00Z"}`)
		case "/connz":
			fmt.Fprint(w, `{"server_id":"NABC","num_connections":0}`)
		case "/healthz":
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
	defer srv.Close()

	m := New(nil, &fakeSink{}, discard())
	if err := m.SetHTTP([]string{srv.URL}, HTTPOptions{}); err != nil {
		t.Fatal(err)
	}

	ask := func(endpoint string) HTTPResult {
		t.Helper()
		out, err := m.FetchHTTP(endpoint, nil)
		if err != nil || len(out) != 1 {
			t.Fatalf("%s: %v, %d results", endpoint, err, len(out))
		}
		return out[0]
	}

	if r := ask("healthz"); r.ServerID != "" {
		t.Errorf("healthz before anything named the server: id = %q, want none", r.ServerID)
	}
	for _, endpoint := range []string{"varz", "connz", "healthz"} {
		if r := ask(endpoint); r.ServerID != "NABC" || r.ServerName != "n1" {
			t.Errorf("%s: server = %q/%q, want NABC/n1", endpoint, r.ServerID, r.ServerName)
		}
	}
}

// With nothing set up at all, the monitor has to say so rather than reporting
// an empty cluster - which would read as "your servers are all gone".
func TestRefreshWithoutASourceSaysSo(t *testing.T) {
	m := New(nil, &fakeSink{}, discard())
	if err := m.RefreshServers(); err != ErrNoSource {
		t.Errorf("err = %v, want ErrNoSource", err)
	}
}
