package natsconn

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// Against a real server, like the KV tests: what a request gets back - one
// reply or several, a 503 or silence - is the server's behaviour.

// reqRig starts a server, connects a Manager to it, and returns a second
// connection for the responders.
func reqRig(t *testing.T) (*Manager, *nats.Conn) {
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

	m := New(nopSink{})
	if err := m.ConnectWith(s.ClientURL(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Disconnect)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return m, nc
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

func bodies(g Gathered) string {
	var out []string
	for _, r := range g.Replies {
		out = append(out, string(r.Data))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestRequestTakesFirstReplyRequestAllTakesEvery(t *testing.T) {
	m, nc := reqRig(t)
	respond(t, nc, "svc.ping", 3, 0)

	if _, err := m.Request("svc.ping", nil, nil, time.Second); err != nil {
		t.Fatal(err)
	}

	g, err := m.RequestAll("svc.ping", nil, nil, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := bodies(g); got != "r0,r1,r2" || g.Truncated != 0 {
		t.Fatalf("got %q truncated %d, want all three", got, g.Truncated)
	}
}

// A responder that answers well after the rest is still gathered: there is no
// stall cut-off, only the timeout.
func TestRequestAllWaitsOutSlowResponders(t *testing.T) {
	m, nc := reqRig(t)
	respond(t, nc, "svc.ping", 2, 0)
	respond(t, nc, "svc.ping", 1, 500*time.Millisecond)

	start := time.Now()
	g, err := m.RequestAll("svc.ping", nil, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Replies) != 3 {
		t.Fatalf("got %d replies, want 3 including the slow one", len(g.Replies))
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("returned after %s; it must wait out the timeout", elapsed)
	}
}

func TestRequestAllNoResponders(t *testing.T) {
	m, _ := reqRig(t)

	start := time.Now()
	_, err := m.RequestAll("nobody.home", nil, nil, 2*time.Second)
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
	m, nc := reqRig(t)
	if _, err := nc.Subscribe("svc.mute", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	_, err := m.RequestAll("svc.mute", nil, nil, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want a timeout", err)
	}
}

func TestRequestAllCountsWhatItDoesNotKeep(t *testing.T) {
	old := maxGathered
	maxGathered = 2
	t.Cleanup(func() { maxGathered = old })

	m, nc := reqRig(t)
	respond(t, nc, "svc.ping", 5, 0)

	g, err := m.RequestAll("svc.ping", nil, nil, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Replies) != 2 || g.Truncated != 3 {
		t.Fatalf("got %d kept, %d truncated; want 2 and 3", len(g.Replies), g.Truncated)
	}
}
