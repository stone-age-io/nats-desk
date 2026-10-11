package services

import (
	"encoding/json"
	"errors"
	"strings"
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
	nc := connect(t, s)

	if _, err := nc.Request("beta.ping", nil, time.Second); err != nil {
		t.Fatal(err)
	}

	c, err := Discover(nc)
	if err != nil {
		t.Fatal(err)
	}
	if c.StatsError != "" || c.Skipped != 0 {
		t.Fatalf("stats error %q, skipped %d", c.StatsError, c.Skipped)
	}

	// Name, then metadata - not the random ID - so code=a comes first.
	want := []string{a.Info().ID, b.Info().ID, beta.Info().ID}
	if got := ids(c.Instances); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}

	for _, in := range c.Instances {
		var info micro.Info
		if err := json.Unmarshal(in.Info, &info); err != nil || len(info.Endpoints) != 1 {
			t.Fatalf("%s: info did not come through: %v %s", in.ID, err, in.Info)
		}
		if in.Stats == nil {
			t.Fatalf("%s: no stats", in.ID)
		}
	}

	var stats micro.Stats
	if err := json.Unmarshal(c.Instances[2].Stats, &stats); err != nil {
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

	start := time.Now()
	c, err := Discover(connect(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > censusTimeout/2 {
		t.Fatalf("an empty census took %s - it waited for the timeout", elapsed)
	}
	if len(c.Instances) != 0 || c.StatsError != "" {
		t.Fatalf("got %+v", c)
	}
}

// Silence is not emptiness. A publish the server refuses produces no "no
// responders", only a timeout, and the user needs to be told which it was.
func TestRefusedCensusSaysWhy(t *testing.T) {
	old := censusTimeout
	censusTimeout = 300 * time.Millisecond
	t.Cleanup(func() { censusTimeout = old })

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
	if _, err := micro.AddService(connect(t, s, nats.UserInfo("svc", "svc")), micro.Config{Name: "alpha", Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}

	// The refusal arrives as an async error, which nats.go prints when no
	// handler is set. It is the expected outcome here, not test noise to read.
	quiet := nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {})
	_, err := Discover(connect(t, s, nats.UserInfo("viewer", "viewer"), quiet))
	if err == nil || !strings.Contains(err.Error(), "$SRV") {
		t.Fatalf("err = %v, want one naming $SRV", err)
	}
}

// Asked directly, a stopped instance answers "no responders", which is
// reported as stopped rather than as a timeout.
func TestStatsOfAStoppedInstanceSaysStopped(t *testing.T) {
	s := runServer(t, nil)
	svc, svcNC := addService(t, s, "alpha", nil)
	nc := connect(t, s)
	id := svc.Info().ID

	raw, err := Stats(nc, "alpha", id)
	if err != nil {
		t.Fatal(err)
	}
	var stats micro.Stats
	if err := json.Unmarshal(raw, &stats); err != nil || stats.ID != id {
		t.Fatalf("stats = %s, %v", raw, err)
	}

	// Flush returns only after the server has processed everything sent
	// before it, the unsubscribes included.
	if err := svc.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := svcNC.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := Stats(nc, "alpha", id); !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v, want ErrStopped", err)
	}
}

// Both halves of the stats subject come from a URL path. A wildcard in either
// would turn a request for one instance into one for many.
func TestStatsRefusesSubjectSyntax(t *testing.T) {
	for _, tc := range [][2]string{{"alpha.>", "x"}, {"alpha", "x.y"}, {"*", "x"}, {"alpha", ""}} {
		if _, err := Stats(nil, tc[0], tc[1]); err == nil {
			t.Errorf("Stats(%q, %q) accepted", tc[0], tc[1])
		}
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

	c, err := Discover(connect(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Instances) != 1 || c.Skipped != 1 {
		t.Fatalf("instances %d, skipped %d - want 1 and 1", len(c.Instances), c.Skipped)
	}
}

// Past the cap the census reports a number instead of rows.
func TestTheCapIsReported(t *testing.T) {
	old := maxInstances
	maxInstances = 1
	t.Cleanup(func() { maxInstances = old })

	s := runServer(t, nil)
	addService(t, s, "alpha", nil)
	addService(t, s, "alpha", nil)

	c, err := Discover(connect(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Instances) != 1 || c.Truncated != 1 {
		t.Fatalf("instances %d, truncated %d - want 1 and 1", len(c.Instances), c.Truncated)
	}
}
