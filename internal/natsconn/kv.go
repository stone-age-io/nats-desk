package natsconn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrNoBucket is returned when a key operation runs with no bucket open.
var ErrNoBucket = errors.New("no bucket open")

// js returns a JetStream handle, created once per connection.
func (m *Manager) js() (jetstream.JetStream, error) {
	nc, err := m.conn()
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	j := m.jsHandle
	m.mu.RUnlock()
	if j != nil {
		return j, nil
	}

	j, err = jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("JetStream is not available on this server: %w", err)
	}
	m.mu.Lock()
	m.jsHandle = j
	m.mu.Unlock()
	return j, nil
}

func (m *Manager) kvHandle() (jetstream.KeyValue, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.kv == nil {
		return nil, ErrNoBucket
	}
	return m.kv, nil
}

// ============================================================================
// BUCKETS
// ============================================================================

func (m *Manager) KvBuckets(ctx context.Context) ([]string, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	// Always a slice so the UI renders "0 buckets" rather than choking on null.
	names := []string{}
	lister := j.KeyValueStoreNames(ctx)
	for name := range lister.Name() {
		names = append(names, name)
	}
	if err := lister.Error(); err != nil {
		return nil, fmt.Errorf("failed to list KV buckets: %w", err)
	}
	return names, nil
}

func (m *Manager) CreateKvBucket(ctx context.Context, cfg jetstream.KeyValueConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.CreateKeyValue(ctx, cfg); err != nil {
		if errors.Is(err, jetstream.ErrBucketExists) {
			return fmt.Errorf("bucket %q already exists", cfg.Bucket)
		}
		return fmt.Errorf("failed to create bucket: %w", err)
	}
	return nil
}

// UpdateKvBucket changes an existing bucket's configuration.
//
// The old browser client had to reach around the KV abstraction and edit the
// underlying KV_<bucket> stream by hand. nats.go exposes the operation
// directly, so that indirection is gone.
func (m *Manager) UpdateKvBucket(ctx context.Context, cfg jetstream.KeyValueConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.UpdateKeyValue(ctx, cfg); err != nil {
		return fmt.Errorf("failed to update bucket: %w", err)
	}
	return nil
}

func (m *Manager) OpenKvBucket(ctx context.Context, bucket string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	kv, err := j.KeyValue(ctx, bucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("bucket %q not found", bucket)
		}
		return fmt.Errorf("failed to open bucket %q: %w", bucket, err)
	}

	m.stopKvWatcher()
	m.mu.Lock()
	m.kv = kv
	m.mu.Unlock()
	return nil
}

// KvStatus reports the open bucket's configuration and size.
//
// The shape is the server's own KeyValueConfig field names plus a few
// read-only counters, so what the JSON editor shows is what the server
// actually stores - not a translation the UI invented.
func (m *Manager) KvStatus(ctx context.Context) (map[string]any, error) {
	kv, err := m.kvHandle()
	if err != nil {
		return nil, err
	}
	st, err := kv.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read bucket status: %w", err)
	}
	cfg := st.Config()
	return map[string]any{
		"bucket":         cfg.Bucket,
		"description":    cfg.Description,
		"history":        cfg.History,
		"ttl":            cfg.TTL,
		"max_bytes":      cfg.MaxBytes,
		"max_value_size": cfg.MaxValueSize,
		"storage":        cfg.Storage,
		"num_replicas":   cfg.Replicas,
		"compression":    cfg.Compression,

		// Read-only, shown but not editable.
		"values":     st.Values(),
		"bytes":      st.Bytes(),
		"compressed": st.IsCompressed(),
	}, nil
}

func (m *Manager) DestroyKvBucket(ctx context.Context, bucket string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if err := j.DeleteKeyValue(ctx, bucket); err != nil {
		return fmt.Errorf("failed to delete bucket %q: %w", bucket, err)
	}

	// Drop a handle to the bucket we just destroyed rather than leaving a
	// stale one that fails confusingly on the next key operation.
	m.mu.Lock()
	if m.kv != nil && m.kv.Bucket() == bucket {
		m.kv = nil
	}
	m.mu.Unlock()
	m.stopKvWatcher()
	return nil
}

// ============================================================================
// WATCH
// ============================================================================

// StopKvWatcher lets the UI release the watcher when it leaves the KV tab.
func (m *Manager) StopKvWatcher() { m.stopKvWatcher() }

func (m *Manager) stopKvWatcher() {
	m.mu.Lock()
	w := m.kvWatcher
	m.kvWatcher = nil
	m.mu.Unlock()
	if w != nil {
		_ = w.Stop()
	}
}

// WatchKvBucket streams key changes to the sink until the bucket changes or
// the connection drops.
func (m *Manager) WatchKvBucket(ctx context.Context) error {
	kv, err := m.kvHandle()
	if err != nil {
		return err
	}
	m.stopKvWatcher()

	// context.Background, not the request context: the watcher outlives the
	// HTTP request that started it.
	w, err := kv.WatchAll(context.Background())
	if err != nil {
		return fmt.Errorf("failed to watch bucket: %w", err)
	}

	m.mu.Lock()
	m.kvWatcher = w
	m.mu.Unlock()

	go func() {
		for e := range w.Updates() {
			// WatchAll sends a nil once the initial replay is done; it marks
			// the boundary between existing keys and live changes, and is not
			// an update in its own right.
			if e == nil {
				continue
			}
			m.sink.KvChange(e.Key(), kvOpName(e.Operation()))
		}
	}()
	return nil
}

// kvOpName returns the operation as it appears on the wire.
//
// KeyValueOp.String() gives Go-flavoured names ("KeyValueDeleteOp"); the
// values NATS actually puts in the KV-Operation header - and that the nats CLI
// and every other client show - are PUT, DEL and PURGE. The UI keys off those.
func kvOpName(op jetstream.KeyValueOp) string {
	switch op {
	case jetstream.KeyValuePut:
		return "PUT"
	case jetstream.KeyValueDelete:
		return "DEL"
	case jetstream.KeyValuePurge:
		return "PURGE"
	default:
		return "UNKNOWN"
	}
}

// ============================================================================
// KEYS
// ============================================================================

type KvEntry struct {
	Key       string    `json:"key"`
	Value     []byte    `json:"-"`
	Revision  uint64    `json:"revision"`
	Created   time.Time `json:"created"`
	Operation string    `json:"operation"`
}

func (m *Manager) KvKeys(ctx context.Context) ([]string, error) {
	kv, err := m.kvHandle()
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	return keys, nil
}

func (m *Manager) KvGet(ctx context.Context, key string) (*KvEntry, error) {
	kv, err := m.kvHandle()
	if err != nil {
		return nil, err
	}
	e, err := kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get key %q: %w", key, err)
	}
	return &KvEntry{
		Key:       e.Key(),
		Value:     e.Value(),
		Revision:  e.Revision(),
		Created:   e.Created(),
		Operation: kvOpName(e.Operation()),
	}, nil
}

func (m *Manager) KvHistory(ctx context.Context, key string) ([]*KvEntry, error) {
	kv, err := m.kvHandle()
	if err != nil {
		return nil, err
	}
	hist, err := kv.History(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return []*KvEntry{}, nil
		}
		return nil, fmt.Errorf("failed to get history for %q: %w", key, err)
	}

	// Newest first, matching how the UI lists revisions.
	out := make([]*KvEntry, 0, len(hist))
	for i := len(hist) - 1; i >= 0; i-- {
		e := hist[i]
		out = append(out, &KvEntry{
			Key:       e.Key(),
			Value:     e.Value(),
			Revision:  e.Revision(),
			Created:   e.Created(),
			Operation: kvOpName(e.Operation()),
		})
	}
	return out, nil
}

func (m *Manager) KvPut(ctx context.Context, key string, value []byte) (uint64, error) {
	kv, err := m.kvHandle()
	if err != nil {
		return 0, err
	}
	rev, err := kv.Put(ctx, key, value)
	if err != nil {
		return 0, fmt.Errorf("failed to put key %q: %w", key, err)
	}
	return rev, nil
}

func (m *Manager) KvDelete(ctx context.Context, key string) error {
	kv, err := m.kvHandle()
	if err != nil {
		return err
	}
	if err := kv.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete key %q: %w", key, err)
	}
	return nil
}

// KvPurge removes the key and its revision history. Delete only writes a
// tombstone and leaves history in place.
func (m *Manager) KvPurge(ctx context.Context, key string) error {
	kv, err := m.kvHandle()
	if err != nil {
		return err
	}
	if err := kv.Purge(ctx, key); err != nil {
		return fmt.Errorf("failed to purge key %q: %w", key, err)
	}
	return nil
}
