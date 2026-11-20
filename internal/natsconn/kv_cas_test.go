package natsconn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// These run against a real nats-server, for the same reason the services
// tests do: what they check is mostly the server's behaviour - which error a
// stale revision produces, what a tombstone does to a create-only write - and
// a fake would only encode guesses about both.

type nopSink struct{}

func (nopSink) Message(uint64, string, string, []byte, nats.Header) {}
func (nopSink) Status(string, error)                                {}
func (nopSink) Stats(float64)                                       {}
func (nopSink) KvChange(string, string)                             {}
func (nopSink) TailMessage(TailMsg)                                 {}
func (nopSink) GatherReply(string, string, []byte, nats.Header)     {}

// kvManager connects a Manager to a fresh JetStream server and opens a bucket.
func kvManager(t *testing.T) *Manager {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		NoLog:     true,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
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

	ctx := context.Background()
	if err := m.CreateKvBucket(ctx, jetstream.KeyValueConfig{Bucket: "cas", History: 5}); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenKvBucket(ctx, "cas"); err != nil {
		t.Fatal(err)
	}
	return m
}

func rev(n uint64) *uint64 { return &n }

// put asserts a write succeeds and returns the revision it produced.
func put(t *testing.T, m *Manager, key, value string, last *uint64) uint64 {
	t.Helper()
	r, err := m.KvPut(context.Background(), key, []byte(value), last)
	if err != nil {
		t.Fatalf("put %q: %v", value, err)
	}
	return r
}

// conflict asserts a write is refused as a conflict and returns its detail.
func conflict(t *testing.T, m *Manager, key, value string, last *uint64) *KvConflictError {
	t.Helper()
	_, err := m.KvPut(context.Background(), key, []byte(value), last)
	var c *KvConflictError
	if !errors.As(err, &c) {
		t.Fatalf("put %q: want a conflict, got %v", value, err)
	}
	return c
}

func TestKvPutStaleRevisionIsRefused(t *testing.T) {
	m := kvManager(t)

	r1 := put(t, m, "k", "one", rev(0))
	r2 := put(t, m, "k", "two", rev(r1))

	// The editor that loaded r1 saves after someone else wrote r2.
	c := conflict(t, m, "k", "stale", rev(r1))
	if c.Expected != r1 || c.Current != r2 {
		t.Errorf("conflict = expected %d current %d, want %d and %d", c.Expected, c.Current, r1, r2)
	}

	// Its overwrite is conditional on r2, the revision it was shown.
	r3 := put(t, m, "k", "overwrite", rev(c.Current))
	if r3 <= r2 {
		t.Errorf("overwrite revision %d, want past %d", r3, r2)
	}

	e, err := m.KvGet(context.Background(), "k")
	if err != nil || e == nil || string(e.Value) != "overwrite" {
		t.Fatalf("value after overwrite = %v, %v", e, err)
	}
}

// A key typed in by name and saved without loading it first is a create, so
// it must not replace one that already exists.
func TestKvPutCreateRefusesExistingKey(t *testing.T) {
	m := kvManager(t)

	r1 := put(t, m, "k", "theirs", nil)

	c := conflict(t, m, "k", "mine", rev(0))
	if c.Expected != 0 || c.Current != r1 {
		t.Errorf("conflict = expected %d current %d, want 0 and %d", c.Expected, c.Current, r1)
	}
	if got := c.Error(); got != `key "k" already exists (revision 1)` {
		t.Errorf("message = %q", got)
	}
}

// A deleted key leaves a tombstone, so the server sees a newer revision than
// the one loaded - but there is nothing left to overwrite. The conflict says
// so with a current revision of 0, and a create then succeeds over the
// tombstone.
func TestKvPutAfterDelete(t *testing.T) {
	m := kvManager(t)
	ctx := context.Background()

	r1 := put(t, m, "k", "one", rev(0))
	if err := m.KvDelete(ctx, "k"); err != nil {
		t.Fatal(err)
	}

	c := conflict(t, m, "k", "stale", rev(r1))
	if c.Current != 0 {
		t.Errorf("current after delete = %d, want 0", c.Current)
	}

	put(t, m, "k", "recreated", rev(c.Current))

	e, err := m.KvGet(ctx, "k")
	if err != nil || e == nil || string(e.Value) != "recreated" {
		t.Fatalf("value after recreate = %v, %v", e, err)
	}
}

// Purge removes history as well, so this is the case where the key really is
// back to never having existed.
func TestKvPutAfterPurge(t *testing.T) {
	m := kvManager(t)
	ctx := context.Background()

	r1 := put(t, m, "k", "one", rev(0))
	if err := m.KvPurge(ctx, "k"); err != nil {
		t.Fatal(err)
	}

	if c := conflict(t, m, "k", "stale", rev(r1)); c.Current != 0 {
		t.Errorf("current after purge = %d, want 0", c.Current)
	}
	put(t, m, "k", "recreated", rev(0))
}

func TestKvPutUnconditional(t *testing.T) {
	m := kvManager(t)

	put(t, m, "k", "one", rev(0))
	put(t, m, "k", "two", nil)
	put(t, m, "k", "three", nil)
}
