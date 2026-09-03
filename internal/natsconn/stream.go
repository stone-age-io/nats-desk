package natsconn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Stream and consumer info are returned as the server's own wire types.
// They marshal to exactly the JSON the UI already reads - config.name,
// state.messages, config.durable_name and so on - so nothing here has to
// re-describe a shape the server already defines.

// ============================================================================
// STREAMS
// ============================================================================

func (m *Manager) Streams(ctx context.Context) ([]*jetstream.StreamInfo, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	out := []*jetstream.StreamInfo{}
	lister := j.ListStreams(ctx)
	for info := range lister.Info() {
		out = append(out, info)
	}
	if err := lister.Err(); err != nil {
		return nil, fmt.Errorf("failed to list streams: %w", err)
	}
	return out, nil
}

func (m *Manager) StreamInfo(ctx context.Context, name string) (*jetstream.StreamInfo, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	s, err := j.Stream(ctx, name)
	if err != nil {
		return nil, streamErr(name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream info: %w", err)
	}
	return info, nil
}

func (m *Manager) CreateStream(ctx context.Context, cfg jetstream.StreamConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.CreateStream(ctx, cfg); err != nil {
		if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			return fmt.Errorf("stream %q already exists", cfg.Name)
		}
		return fmt.Errorf("failed to create stream: %w", err)
	}
	return nil
}

func (m *Manager) UpdateStream(ctx context.Context, cfg jetstream.StreamConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("failed to update stream: %w", err)
	}
	return nil
}

func (m *Manager) PurgeStream(ctx context.Context, name string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	s, err := j.Stream(ctx, name)
	if err != nil {
		return streamErr(name, err)
	}
	if err := s.Purge(ctx); err != nil {
		return fmt.Errorf("failed to purge stream: %w", err)
	}
	return nil
}

func (m *Manager) DeleteStream(ctx context.Context, name string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if err := j.DeleteStream(ctx, name); err != nil {
		return streamErr(name, err)
	}
	return nil
}

func streamErr(name string, err error) error {
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("stream %q not found", name)
	}
	return fmt.Errorf("stream %q: %w", name, err)
}

// ============================================================================
// CONSUMERS
// ============================================================================

func (m *Manager) Consumers(ctx context.Context, stream string) ([]*jetstream.ConsumerInfo, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	s, err := j.Stream(ctx, stream)
	if err != nil {
		return nil, streamErr(stream, err)
	}
	out := []*jetstream.ConsumerInfo{}
	lister := s.ListConsumers(ctx)
	for info := range lister.Info() {
		out = append(out, info)
	}
	if err := lister.Err(); err != nil {
		return nil, fmt.Errorf("failed to list consumers: %w", err)
	}
	return out, nil
}

func (m *Manager) ConsumerInfo(ctx context.Context, stream, consumer string) (*jetstream.ConsumerInfo, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	c, err := j.Consumer(ctx, stream, consumer)
	if err != nil {
		return nil, consumerErr(consumer, err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get consumer info: %w", err)
	}
	return info, nil
}

func (m *Manager) CreateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.CreateConsumer(ctx, stream, cfg); err != nil {
		if errors.Is(err, jetstream.ErrConsumerExists) {
			name := cfg.Durable
			if name == "" {
				name = cfg.Name
			}
			return fmt.Errorf("consumer %q already exists", name)
		}
		return fmt.Errorf("failed to create consumer: %w", err)
	}
	return nil
}

func (m *Manager) UpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if _, err := j.UpdateConsumer(ctx, stream, cfg); err != nil {
		return fmt.Errorf("failed to update consumer: %w", err)
	}
	return nil
}

func (m *Manager) DeleteConsumer(ctx context.Context, stream, consumer string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	if err := j.DeleteConsumer(ctx, stream, consumer); err != nil {
		return consumerErr(consumer, err)
	}
	return nil
}

func consumerErr(name string, err error) error {
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("consumer %q not found", name)
	}
	return fmt.Errorf("consumer %q: %w", name, err)
}

// ============================================================================
// STREAM MESSAGES
// ============================================================================

// StreamMsg is one stored message. Data is carried separately so the API layer
// can base64 it rather than letting encoding/json mangle raw bytes.
type StreamMsg struct {
	Seq     uint64              `json:"seq"`
	Subject string              `json:"subject"`
	Data    []byte              `json:"-"`
	Time    time.Time           `json:"time"`
	Headers map[string][]string `json:"headers,omitempty"`
}

// StreamMessageRange fetches stored messages by sequence.
//
// One ephemeral ordered consumer with a batch fetch, rather than a GetMsg per
// sequence: a 50-message window is one round trip instead of fifty.
func (m *Manager) StreamMessageRange(ctx context.Context, name string, start, end uint64, subjectFilter string, max int) ([]*StreamMsg, error) {
	j, err := m.js()
	if err != nil {
		return nil, err
	}
	if start < 1 {
		start = 1
	}
	if end < start || max <= 0 {
		return []*StreamMsg{}, nil
	}

	cfg := jetstream.OrderedConsumerConfig{
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   start,
	}
	if subjectFilter != "" {
		cfg.FilterSubjects = []string{subjectFilter}
	}

	consumer, err := j.OrderedConsumer(ctx, name, cfg)
	if err != nil {
		return nil, streamErr(name, err)
	}

	// A bounded wait: the range may legitimately contain fewer messages than
	// asked for, and without this the fetch would block for its full default.
	batch, err := consumer.Fetch(max, jetstream.FetchMaxWait(3*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	out := []*StreamMsg{}
	for msg := range batch.Messages() {
		meta, err := msg.Metadata()
		if err != nil {
			continue
		}
		if meta.Sequence.Stream > end {
			break
		}
		out = append(out, &StreamMsg{
			Seq:     meta.Sequence.Stream,
			Subject: msg.Subject(),
			Data:    msg.Data(),
			Time:    meta.Timestamp,
			Headers: msg.Headers(),
		})
		if len(out) >= max {
			break
		}
	}
	if err := batch.Error(); err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	// Newest first, matching the live log's default direction.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ============================================================================
// STREAM TAIL
// ============================================================================

// StartStreamTail follows new messages on a stream, pushing them to the sink.
func (m *Manager) StartStreamTail(ctx context.Context, name, subjectFilter string) error {
	j, err := m.js()
	if err != nil {
		return err
	}
	m.StopStreamTail()

	cfg := jetstream.OrderedConsumerConfig{DeliverPolicy: jetstream.DeliverNewPolicy}
	if subjectFilter != "" {
		cfg.FilterSubjects = []string{subjectFilter}
	}

	consumer, err := j.OrderedConsumer(ctx, name, cfg)
	if err != nil {
		return streamErr(name, err)
	}

	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		meta, err := msg.Metadata()
		if err != nil {
			return
		}
		m.sink.TailMessage(TailMsg{
			Seq:     meta.Sequence.Stream,
			Subject: msg.Subject(),
			Data:    msg.Data(),
			Time:    meta.Timestamp,
			Headers: msg.Headers(),
		})
	})
	if err != nil {
		return fmt.Errorf("failed to tail stream: %w", err)
	}

	m.mu.Lock()
	m.tail = cc
	m.mu.Unlock()
	return nil
}

func (m *Manager) StopStreamTail() {
	m.mu.Lock()
	t := m.tail
	m.tail = nil
	m.mu.Unlock()
	if t != nil {
		t.Stop()
	}
}

func (m *Manager) IsTailing() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tail != nil
}
