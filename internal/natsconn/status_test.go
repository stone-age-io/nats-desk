package natsconn

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// statusSink records the status reports a Manager makes.
type statusSink struct {
	nopSink
	mu     sync.Mutex
	states []string
}

func (s *statusSink) Status(state string, _ error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, state)
}

func (s *statusSink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.states)
}

func startServer(t *testing.T) *server.Server {
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
	return s
}

// A switch replaces one connection with another. Close reports on the old
// one asynchronously, and those reports must not reach the UI after the new
// connection is up - they would mark it disconnected.
func TestSwitchingDropsTheOldConnectionsReports(t *testing.T) {
	a, b := startServer(t), startServer(t)
	sink := &statusSink{}
	m := New(sink)
	t.Cleanup(m.Disconnect)

	if err := m.ConnectWith(a.ClientURL(), nil); err != nil {
		t.Fatal(err)
	}
	if err := m.ConnectWith(b.ClientURL(), nil); err != nil {
		t.Fatal(err)
	}
	// Long enough for the old connection's async callbacks to have run.
	time.Sleep(300 * time.Millisecond)

	if got := sink.got(); !slices.Equal(got, []string{"disconnected"}) {
		t.Fatalf("after a switch got %v, want exactly one disconnected, reported by Disconnect", got)
	}

	m.Disconnect()
	time.Sleep(300 * time.Millisecond)
	if got := sink.got(); !slices.Equal(got, []string{"disconnected", "disconnected"}) {
		t.Fatalf("after Disconnect got %v, want one more disconnected and no reconnecting", got)
	}
}

// The current connection's own reports still get through.
func TestTheCurrentConnectionStillReportsALostServer(t *testing.T) {
	s := startServer(t)
	sink := &statusSink{}
	m := New(sink)
	t.Cleanup(m.Disconnect)

	if err := m.ConnectWith(s.ClientURL(), []nats.Option{nats.ReconnectWait(50 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	s.Shutdown()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(sink.got(), "reconnecting") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("got %v, want reconnecting once the server went away", sink.got())
}
