package services

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

// These run against a real nats-server in this process - no ports exposed
// beyond loopback, no mocks. The behaviour under test is mostly the server's:
// what "no responders" looks like, what a refused publish looks like, when an
// unsubscribe takes effect. A fake would encode our guesses about all three.

func runServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	if opts == nil {
		opts = &server.Options{}
	}
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.NoLog = true
	opts.NoSigs = true

	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(s.Shutdown)
	return s
}

func connect(t *testing.T, s *server.Server, opts ...nats.Option) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(s.ClientURL(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// dataConn stands in for natsconn.Manager. The connection can be swapped,
// which is how a test reconnects somewhere else.
type dataConn struct {
	mu sync.Mutex
	nc *nats.Conn
}

func (d *dataConn) Conn() (*nats.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nc, nil
}

func (d *dataConn) set(nc *nats.Conn) {
	d.mu.Lock()
	d.nc = nc
	d.mu.Unlock()
}

// addService starts one instance on its own connection, as a separate process
// would be, with a single "ping" endpoint.
func addService(t *testing.T, s *server.Server, name string, meta map[string]string) (micro.Service, *nats.Conn) {
	t.Helper()
	nc := connect(t, s)
	svc, err := micro.AddService(nc, micro.Config{
		Name:     name,
		Version:  "1.0.0",
		Metadata: meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.AddEndpoint("ping", micro.HandlerFunc(func(r micro.Request) { _ = r.Respond([]byte("pong")) }),
		micro.WithEndpointSubject(name+".ping"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop() })
	return svc, nc
}

// stop stops an instance and waits until the server has dropped its interest.
// Flush returns after the server has processed everything sent before it,
// including the unsubscribes, so a census straight afterwards cannot reach it.
func stop(t *testing.T, svc micro.Service, nc *nats.Conn) {
	t.Helper()
	if err := svc.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

func newCatalog(t *testing.T, s *server.Server) (*Catalog, *dataConn) {
	t.Helper()
	d := &dataConn{nc: connect(t, s)}
	return New(d), d
}

func shrink(t *testing.T, v *time.Duration, to time.Duration) {
	t.Helper()
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

func ids(list []Instance) []string {
	out := make([]string, 0, len(list))
	for _, in := range list {
		out = append(out, in.ID)
	}
	return out
}

// One census, two halves: INFO for what each instance is, STATS for how it is
// doing, joined on the instance ID.
func TestCensusJoinsInfoAndStats(t *testing.T) {
	s := runServer(t, nil)
	b, _ := addService(t, s, "alpha", map[string]string{"code": "b"})
	a, _ := addService(t, s, "alpha", map[string]string{"code": "a"})
	beta, _ := addService(t, s, "beta", nil)
	cat, d := newCatalog(t, s)

	if _, err := d.nc.Request("beta.ping", nil, time.Second); err != nil {
		t.Fatal(err)
	}

	snap, err := cat.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if snap.At == nil {
		t.Fatal("a census that ran has no time")
	}
	if snap.StatsError != "" || snap.Skipped != 0 {
		t.Fatalf("stats error %q, skipped %d", snap.StatsError, snap.Skipped)
	}

	// Name, then metadata - not the random ID - so code=a comes first.
	want := []string{a.Info().ID, b.Info().ID, beta.Info().ID}
	if got := ids(snap.Instances); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}

	for _, in := range snap.Instances {
		var info micro.Info
		if err := json.Unmarshal(in.Info, &info); err != nil || len(info.Endpoints) != 1 {
			t.Fatalf("%s: info did not come through: %v %s", in.ID, err, in.Info)
		}
		if in.Stats == nil {
			t.Fatalf("%s: no stats", in.ID)
		}
	}

	var stats micro.Stats
	if err := json.Unmarshal(snap.Instances[2].Stats, &stats); err != nil {
		t.Fatal(err)
	}
	if n := stats.Endpoints[0].NumRequests; n != 1 {
		t.Fatalf("beta ping requests = %d, want 1", n)
	}
}

// With nobody subscribed the server answers "no responders" at once. That is a
// complete answer - an empty account - and must neither error nor sit out the
// timeout.
func TestEmptyAccountIsAnAnswerNotATimeout(t *testing.T) {
	s := runServer(t, nil)
	cat, _ := newCatalog(t, s)

	start := time.Now()
	snap, err := cat.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > censusTimeout/2 {
		t.Fatalf("an empty census took %s - it waited for the timeout", elapsed)
	}
	if snap.At == nil || len(snap.Instances) != 0 || snap.StatsError != "" {
		t.Fatalf("got %+v", snap)
	}
}

// Silence is not emptiness. A publish the server refuses produces no "no
// responders", only a timeout, and the user needs to be told which it was.
func TestRefusedCensusSaysWhy(t *testing.T) {
	shrink(t, &censusTimeout, 300*time.Millisecond)
	s := runServer(t, &server.Options{
		Users: []*server.User{
			{Username: "svc", Password: "svc"},
			{
				Username: "viewer", Password: "viewer",
				Permissions: &server.Permissions{
					Publish: &server.SubjectPermission{Deny: []string{"$SRV.>"}},
				},
			},
		},
	})
	nc := connect(t, s, nats.UserInfo("svc", "svc"))
	if _, err := micro.AddService(nc, micro.Config{Name: "alpha", Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	// The refusal arrives as an async error, which nats.go prints when no
	// handler is set. It is the expected outcome here, not test noise to read.
	quiet := nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {})
	cat := New(&dataConn{nc: connect(t, s, nats.UserInfo("viewer", "viewer"), quiet)})

	_, err := cat.Refresh()
	if err == nil || !strings.Contains(err.Error(), "$SRV") {
		t.Fatalf("err = %v, want one naming $SRV", err)
	}
}

// A restart is the common case: micro gives every process a new ID, so the
// old one stops answering and a new one starts. The old row is shown as gone
// for one census and then dropped.
func TestAStoppedInstanceIsGoneOnceThenDropped(t *testing.T) {
	s := runServer(t, nil)
	old, oldNC := addService(t, s, "alpha", nil)
	cat, _ := newCatalog(t, s)

	if _, err := cat.Refresh(); err != nil {
		t.Fatal(err)
	}

	stop(t, old, oldNC)
	replacement, _ := addService(t, s, "alpha", nil)

	snap, err := cat.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Instances) != 2 {
		t.Fatalf("instances = %v, want the replacement and the gone one", ids(snap.Instances))
	}
	for _, in := range snap.Instances {
		wantGone := in.ID == old.Info().ID
		if in.Gone != wantGone {
			t.Fatalf("%s gone = %v, want %v", in.ID, in.Gone, wantGone)
		}
	}

	snap, err = cat.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(snap.Instances); len(got) != 1 || got[0] != replacement.Info().ID {
		t.Fatalf("instances = %v, want only %s", got, replacement.Info().ID)
	}
}

// Asked directly, a stopped instance answers "no responders". That is the
// instance's state, not a failure of the request.
func TestInstanceStatsMarksAStoppedInstanceGone(t *testing.T) {
	s := runServer(t, nil)
	svc, svcNC := addService(t, s, "alpha", nil)
	cat, _ := newCatalog(t, s)

	if _, err := cat.Refresh(); err != nil {
		t.Fatal(err)
	}
	id := svc.Info().ID

	in, err := cat.InstanceStats("alpha", id)
	if err != nil || in.Gone || in.Stats == nil {
		t.Fatalf("live instance: %+v, %v", in, err)
	}

	stop(t, svc, svcNC)
	in, err = cat.InstanceStats("alpha", id)
	if err != nil {
		t.Fatal(err)
	}
	if !in.Gone {
		t.Fatal("a stopped instance was not marked gone")
	}
	if snap := cat.Snapshot(); len(snap.Instances) != 1 || !snap.Instances[0].Gone {
		t.Fatalf("the census did not keep it: %+v", snap.Instances)
	}
}

// Both halves of the stats subject come from a URL path. A wildcard in either
// would turn a request for one instance into one for many.
func TestInstanceStatsRefusesSubjectSyntax(t *testing.T) {
	cat := New(&dataConn{})
	for _, tc := range [][2]string{{"alpha.>", "x"}, {"alpha", "x.y"}, {"*", "x"}, {"alpha", ""}} {
		if _, err := cat.InstanceStats(tc[0], tc[1]); err == nil {
			t.Errorf("InstanceStats(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

// A census is about one connection. Connected elsewhere, it lists services
// nats-desk can no longer see.
func TestAnotherConnectionDiscardsTheCensus(t *testing.T) {
	s := runServer(t, nil)
	addService(t, s, "alpha", nil)
	cat, d := newCatalog(t, s)

	if snap, err := cat.Refresh(); err != nil || len(snap.Instances) != 1 {
		t.Fatalf("%+v, %v", snap, err)
	}
	if snap := cat.Snapshot(); len(snap.Instances) != 1 {
		t.Fatal("the census did not survive a read on the same connection")
	}

	d.set(connect(t, s))
	snap := cat.Snapshot()
	if snap.At != nil || len(snap.Instances) != 0 {
		t.Fatalf("a census from another connection survived: %+v", snap)
	}
}

// Anything can subscribe to $SRV.INFO. A reply that is not a micro service is
// counted, and the rest of the census stands.
func TestAForeignReplyIsCountedNotFatal(t *testing.T) {
	s := runServer(t, nil)
	addService(t, s, "alpha", nil)
	rogue := connect(t, s)
	if _, err := rogue.Subscribe("$SRV.INFO", func(m *nats.Msg) { _ = m.Respond([]byte("hello")) }); err != nil {
		t.Fatal(err)
	}
	if err := rogue.Flush(); err != nil {
		t.Fatal(err)
	}
	cat, _ := newCatalog(t, s)

	snap, err := cat.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Instances) != 1 || snap.Skipped != 1 {
		t.Fatalf("instances %d, skipped %d - want 1 and 1", len(snap.Instances), snap.Skipped)
	}
}

// Past the cap the census reports a number instead of rows. An instance left
// out that way did answer, so it must not later be shown as gone.
func TestTheCapIsReportedAndIsNotAnAbsence(t *testing.T) {
	old := maxInstances
	maxInstances = 1
	t.Cleanup(func() { maxInstances = old })

	s := runServer(t, nil)
	addService(t, s, "alpha", nil)
	addService(t, s, "alpha", nil)
	cat, _ := newCatalog(t, s)

	for i := 0; i < 3; i++ {
		snap, err := cat.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		if snap.Truncated != 1 {
			t.Fatalf("census %d: truncated = %d, want 1", i, snap.Truncated)
		}
		for _, in := range snap.Instances {
			if in.Gone {
				t.Fatalf("census %d: %s marked gone though it answered", i, in.ID)
			}
		}
	}
}
