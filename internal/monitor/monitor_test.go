package monitor

import (
	"encoding/json"
	"io"
	"log/slog"
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

// With nothing set up at all, the monitor has to say so rather than reporting
// an empty cluster - which would read as "your servers are all gone".
func TestRefreshWithoutASourceSaysSo(t *testing.T) {
	m := New(nil, &fakeSink{}, discard())
	if err := m.RefreshServers(); err != ErrNoSource {
		t.Errorf("err = %v, want ErrNoSource", err)
	}
}
