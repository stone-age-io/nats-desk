package natsconn

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Against a real server, like the KV tests: what a request gets back - one
// reply or several, a 503 or silence - is the server's behaviour.

// gatherSink records the replies RequestAll hands on, and signals each one as
// it comes, so a test can tell "handed on as it arrived" from "handed on when
// the timeout was up".
type gatherSink struct {
	nopSink

	mu      sync.Mutex
	ids     []string
	bodies  []string
	arrived chan struct{}
}

func newGatherSink() *gatherSink {
	return &gatherSink{arrived: make(chan struct{}, 1024)}
}

func (s *gatherSink) GatherReply(id, _ string, data []byte, _ nats.Header) {
	s.mu.Lock()
	s.ids = append(s.ids, id)
	s.bodies = append(s.bodies, string(data))
	s.mu.Unlock()
	s.arrived <- struct{}{}
}

// got is every body handed on, sorted, and fails the test if any of them was
// tagged with an ID other than want.
func (s *gatherSink) got(t *testing.T, want string) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.ids {
		if id != want {
			t.Fatalf("a reply was tagged %q, want %q", id, want)
		}
	}
	out := append([]string(nil), s.bodies...)
	sort.Strings(out)
	return strings.Join(out, ",")
}

// reqRig starts a server, connects a Manager to it, and returns the sink the
// Manager hands replies to and a second connection for the responders.
func reqRig(t *testing.T) (*Manager, *gatherSink, *nats.Conn) {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(s.Shutdown)

	sink := newGatherSink()
	m := New(sink)
	if err := m.ConnectWith(s.ClientURL(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Disconnect)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return m, sink, nc
}

// respond subscribes n responders to subject, each answering with its own
// name after delay.
func respond(t *testing.T, nc *nats.Conn, subject string, n int, delay time.Duration) {
	t.Helper()
	for i := range n {
		name := fmt.Sprintf("r%d", i)
		if _, err := nc.Subscribe(subject, func(m *nats.Msg) {
			time.Sleep(delay)
			_ = m.Respond([]byte(name))
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Return only once the server has the subscriptions, so the request
	// cannot reach it first.
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestTakesFirstReplyRequestAllTakesEvery(t *testing.T) {
	m, sink, nc := reqRig(t)
	respond(t, nc, "svc.ping", 3, 0)

	if _, err := m.Request("svc.ping", nil, nil, time.Second); err != nil {
		t.Fatal(err)
	}

	g, err := m.RequestAll("g1", "svc.ping", nil, nil, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := sink.got(t, "g1"); got != "r0,r1,r2" || g.Count != 3 || g.Truncated != 0 {
		t.Fatalf("got %q, count %d, truncated %d; want all three", got, g.Count, g.Truncated)
	}
}

// The point of handing replies to the sink rather than returning them: a fast
// reply is on its way to the browser long before a slow window closes.
func TestRequestAllHandsRepliesOnAsTheyArrive(t *testing.T) {
	m, sink, nc := reqRig(t)
	respond(t, nc, "svc.ping", 1, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := m.RequestAll("g1", "svc.ping", nil, nil, 2*time.Second); err != nil {
			t.Error(err)
		}
	}()

	select {
	case <-sink.arrived:
	case <-time.After(time.Second):
		t.Fatal("the reply was not handed on within a second of a two second window")
	}
	select {
	case <-done:
		t.Fatal("RequestAll returned early; it must still wait out the timeout")
	default:
	}
	<-done
}

// A responder that answers well after the rest is still gathered: there is no
// stall cut-off, only the timeout.
func TestRequestAllWaitsOutSlowResponders(t *testing.T) {
	m, sink, nc := reqRig(t)
	respond(t, nc, "svc.ping", 2, 0)
	respond(t, nc, "svc.ping", 1, 500*time.Millisecond)

	start := time.Now()
	g, err := m.RequestAll("g1", "svc.ping", nil, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if g.Count != 3 {
		t.Fatalf("got %d replies (%s), want 3 including the slow one", g.Count, sink.got(t, "g1"))
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("returned after %s; it must wait out the timeout", elapsed)
	}
}

func TestRequestAllNoResponders(t *testing.T) {
	m, _, _ := reqRig(t)

	start := time.Now()
	_, err := m.RequestAll("g1", "nobody.home", nil, nil, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "no responder") {
		t.Fatalf("got %v, want a no-responders error", err)
	}
	// The server says so at once; waiting out the timeout would mean the 503
	// was missed.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %s to report no responders", elapsed)
	}
}

// Subscribed but silent is a timeout, not "no responders".
func TestRequestAllSilenceIsATimeout(t *testing.T) {
	m, _, nc := reqRig(t)
	if _, err := nc.Subscribe("svc.mute", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	_, err := m.RequestAll("g1", "svc.mute", nil, nil, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want a timeout", err)
	}
}

func TestRequestAllCountsWhatItDoesNotKeep(t *testing.T) {
	old := maxGathered
	maxGathered = 2
	t.Cleanup(func() { maxGathered = old })

	m, sink, nc := reqRig(t)
	respond(t, nc, "svc.ping", 5, 0)

	g, err := m.RequestAll("g1", "svc.ping", nil, nil, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(sink.got(t, "g1"), ",")); n != 2 || g.Count != 2 || g.Truncated != 3 {
		t.Fatalf("handed on %d, counted %d, truncated %d; want 2, 2 and 3", n, g.Count, g.Truncated)
	}
}
