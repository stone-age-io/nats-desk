package server

import (
	"testing"
	"time"
)

// No listener: the idle bookkeeping is only ever driven by the hub's hooks.
func idleServer() *Server {
	return &Server{idle: make(chan struct{})}
}

func idled(s *Server, within time.Duration) bool {
	select {
	case <-s.Idle():
		return true
	case <-time.After(within):
		return false
	}
}

// Quit-on-close is turned on from a window that is open at the time, so the
// new timeout has to apply to the disconnect that follows.
func TestSetIdleTimeoutAppliesToNextDisconnect(t *testing.T) {
	s := idleServer()
	s.ClientConnected()
	s.SetIdleTimeout(10 * time.Millisecond)
	s.ClientDisconnected()

	if !idled(s, time.Second) {
		t.Fatal("did not go idle after the last client left")
	}
}

// A reload drops the socket and opens a new one. That must not count as the
// window closing.
func TestReconnectWithinTimeoutKeepsRunning(t *testing.T) {
	s := idleServer()
	s.SetIdleTimeout(50 * time.Millisecond)
	s.ClientConnected()
	s.ClientDisconnected()
	s.ClientConnected()

	if idled(s, 150*time.Millisecond) {
		t.Fatal("went idle although a client came back")
	}
}

// Turning the setting off must win over a countdown it had already started.
func TestTurningOffCancelsCountdown(t *testing.T) {
	s := idleServer()
	s.SetIdleTimeout(50 * time.Millisecond)
	s.ClientConnected()
	s.ClientDisconnected()
	s.SetIdleTimeout(0)

	if idled(s, 150*time.Millisecond) {
		t.Fatal("went idle after the timeout was turned off")
	}
}

func TestNoTimeoutNeverIdles(t *testing.T) {
	s := idleServer()
	s.ClientConnected()
	s.ClientDisconnected()

	if idled(s, 50*time.Millisecond) {
		t.Fatal("went idle with no timeout set")
	}
}
