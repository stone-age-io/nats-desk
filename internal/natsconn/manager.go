// Package natsconn owns the NATS connection.
//
// This is the piece that only exists because the client moved out of the
// browser: nats.go speaks the native protocol on 4222, so it reaches any
// server without the websocket listener a browser would have needed.
package natsconn

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// ErrNotConnected is returned by every operation that needs a live
// connection. The API layer maps it to 409 so the UI can tell "you are not
// connected" apart from "the server said no".
var ErrNotConnected = errors.New("not connected")

const statsPollInterval = 2 * time.Second

// Sink receives everything the manager pushes out. The WebSocket hub
// implements it; nothing in this package knows about HTTP.
type Sink interface {
	// reply is the message's reply subject, empty when it has none. A request
	// is only answerable from the UI if this survives the trip.
	Message(subID uint64, subject, reply string, data []byte, headers nats.Header)
	Status(state string, err error)
	Stats(rttMillis float64)
	KvChange(key, operation string)
	TailMessage(msg TailMsg)

	// GatherReply is one reply to a RequestAll, handed on the moment it
	// arrives. gatherID is the caller's, so the window that asked can pick
	// its own replies out of a channel every window shares.
	GatherReply(gatherID, subject string, data []byte, headers nats.Header)
}

// TailMsg is one message from a live stream tail.
type TailMsg struct {
	Seq     uint64      `json:"seq"`
	Subject string      `json:"subject"`
	Data    []byte      `json:"-"`
	Time    time.Time   `json:"time"`
	Headers nats.Header `json:"headers,omitempty"`
}

type subscription struct {
	sub           *nats.Subscription
	subject       string
	queue         string
	excludeSystem bool
}

type Manager struct {
	sink Sink

	mu        sync.RWMutex
	nc        *nats.Conn
	subs      map[uint64]*subscription
	nextSubID uint64
	stopStats chan struct{}

	// JetStream state. One connection has at most one open KV bucket and one
	// live stream tail, matching what the UI can show at once.
	jsHandle  jetstream.JetStream
	kv        jetstream.KeyValue
	kvWatcher jetstream.KeyWatcher
	tail      jetstream.ConsumeContext
}

func New(sink Sink) *Manager {
	return &Manager{sink: sink, subs: map[uint64]*subscription{}}
}

// AuthOptions mirrors what the connection form collects.
type AuthOptions struct {
	CredsText string
	User      string
	Pass      string
	Token     string
}

// Connect replaces any existing connection, using credentials the UI collected.
func (m *Manager) Connect(url string, auth AuthOptions) error {
	authOpt, err := auth.Option()
	if err != nil {
		return err
	}

	var opts []nats.Option
	if authOpt != nil {
		opts = append(opts, authOpt)
	}
	return m.ConnectWith(url, opts)
}

// ConnectWith replaces any existing connection, using options assembled
// elsewhere - a NATS CLI context builds its own, covering creds files, nkeys
// and client certificates that only a process on this machine can read.
//
// The manager's own options go last, because nats.go applies options in order
// and a context must not be able to displace the status handlers the UI
// depends on.
//
// The handlers report only for the current connection. Close runs the
// disconnected and closed callbacks asynchronously, so a connection replaced
// by a switch would otherwise report "reconnecting" and "disconnected" after
// its successor was already up, and the UI would believe them.
func (m *Manager) ConnectWith(url string, opts []nats.Option) error {
	m.Disconnect()

	all := make([]nats.Option, 0, len(opts)+5)
	all = append(all, opts...)
	all = append(all,
		nats.Name("nats-desk"),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if m.isCurrent(nc) {
				m.sink.Status("reconnecting", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			if m.isCurrent(nc) {
				m.sink.Status("connected", nil)
			}
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			if m.isCurrent(nc) {
				m.sink.Status("disconnected", nc.LastError())
			}
		}),
	)

	nc, err := nats.Connect(url, all...)
	if err != nil {
		return friendlyConnectError(err)
	}

	m.mu.Lock()
	m.nc = nc
	m.nextSubID = 0
	m.stopStats = make(chan struct{})
	stop := m.stopStats
	m.mu.Unlock()

	go m.pollStats(nc, stop)
	// Deliberately no Status("connected") here: the caller of Connect gets
	// the answer synchronously, and pushing it as well made the UI toast a
	// "Reconnected" it had never disconnected from. Only the reconnect and
	// close handlers push status.
	return nil
}

// Option turns the collected credentials into a nats.Option.
//
// Order matches the old client: a creds file wins, then a token, then
// user/password. Anything absent simply means an anonymous connection.
//
// Exported because the monitoring package's system-account connection collects
// the same fields through the same form and has to interpret them identically.
func (a AuthOptions) Option() (nats.Option, error) {
	switch {
	case strings.TrimSpace(a.CredsText) != "":
		// The UI hands us the file's contents, not a path. nats.go's
		// UserCredentials only reads paths, so the JWT and seed are pulled
		// out here rather than staging a temp file - which would mean
		// writing a credential to disk to satisfy an API shape.
		raw := []byte(strings.ReplaceAll(a.CredsText, "\r\n", "\n"))
		userJWT, err := jwt.ParseDecoratedJWT(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: %w", err)
		}
		kp, err := jwt.ParseDecoratedNKey(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: no seed found: %w", err)
		}
		seed, err := kp.Seed()
		if err != nil {
			return nil, fmt.Errorf("invalid .creds file: %w", err)
		}
		return nats.UserJWTAndSeed(userJWT, string(seed)), nil

	case a.Token != "":
		return nats.Token(a.Token), nil

	case a.User != "":
		return nats.UserInfo(a.User, a.Pass), nil
	}
	return nil, nil
}

// friendlyConnectError turns nats.go's wording into something a UI can show.
func friendlyConnectError(err error) error {
	switch {
	case errors.Is(err, nats.ErrAuthorization):
		return errors.New("authentication failed - check credentials")
	case errors.Is(err, nats.ErrNoServers):
		return errors.New("cannot reach the NATS server - check the URL and that it is running")
	default:
		return fmt.Errorf("connection failed: %w", err)
	}
}

func (m *Manager) Disconnect() {
	m.stopKvWatcher()
	m.StopStreamTail()

	m.mu.Lock()
	nc := m.nc
	stop := m.stopStats
	m.nc = nil
	m.stopStats = nil
	m.subs = map[uint64]*subscription{}
	m.jsHandle = nil
	m.kv = nil
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}
	if nc != nil {
		// Drain would wait for in-flight handlers; Close is what the UI
		// means by "disconnect now".
		nc.Close()
		// Reported here, not by the closed handler, which now ignores this
		// connection: from here the report goes out before Disconnect
		// returns, so it cannot arrive after whatever connects next. Other
		// windows learn of the disconnect from it.
		m.sink.Status("disconnected", nil)
	}
}

// isCurrent reports whether nc is the connection the manager holds now.
func (m *Manager) isCurrent(nc *nats.Conn) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nc == nc
}

func (m *Manager) conn() (*nats.Conn, error) {
	m.mu.RLock()
	nc := m.nc
	m.mu.RUnlock()
	if nc == nil || nc.IsClosed() {
		return nil, ErrNotConnected
	}
	return nc, nil
}

// Conn exposes the live connection for callers that issue their own
// requests against it - the account-scoped monitoring endpoints, which are
// auto-imported into every account and so need no second connection.
func (m *Manager) Conn() (*nats.Conn, error) {
	return m.conn()
}

func (m *Manager) IsConnected() bool {
	_, err := m.conn()
	return err == nil
}

// ServerInfo is what the connection popover shows.
func (m *Manager) ServerInfo() (map[string]any, error) {
	nc, err := m.conn()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"server_id":   nc.ConnectedServerId(),
		"server_name": nc.ConnectedServerName(),
		"version":     nc.ConnectedServerVersion(),
		"cluster":     nc.ConnectedClusterName(),
		"url":         nc.ConnectedUrl(),
		"addr":        nc.ConnectedAddr(),
		"tls":         nc.TLSRequired(),
		"headers":     nc.HeadersSupported(),
		"max_payload": nc.MaxPayload(),
	}, nil
}

func (m *Manager) pollStats(nc *nats.Conn, stop <-chan struct{}) {
	t := time.NewTicker(statsPollInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if nc.IsClosed() {
				return
			}
			rtt, err := nc.RTT()
			if err != nil {
				continue
			}
			m.sink.Stats(float64(rtt.Microseconds()) / 1000.0)
		}
	}
}

// ============================================================================
// PUB / SUB
// ============================================================================

type SubResult struct {
	ID            uint64 `json:"id"`
	Subject       string `json:"subject"`
	Queue         string `json:"queue,omitempty"`
	ExcludeSystem bool   `json:"excludeSystem"`
	Size          int    `json:"size"`
}

// Subscribe registers a subscription and streams its messages to the sink.
//
// A non-empty queue joins that queue group, so the server hands each message
// to one member of the group rather than to every subscriber - this window
// becomes one more worker beside whatever else is in the group, and sees only
// its share of the traffic.
func (m *Manager) Subscribe(subject, queue string, excludeSystem bool) (SubResult, error) {
	nc, err := m.conn()
	if err != nil {
		return SubResult{}, err
	}

	// Honour the request only where it is meaningful, so the flag reported
	// back - and shown in the UI - is what is actually happening.
	exclude := excludeSystem && CanExcludeSystem(subject)

	m.mu.Lock()
	m.nextSubID++
	id := m.nextSubID
	m.mu.Unlock()

	handler := func(msg *nats.Msg) {
		if exclude && IsSystemSubject(msg.Subject) {
			return
		}
		m.sink.Message(id, msg.Subject, msg.Reply, msg.Data, msg.Header)
	}

	var sub *nats.Subscription
	if queue != "" {
		sub, err = nc.QueueSubscribe(subject, queue, handler)
	} else {
		sub, err = nc.Subscribe(subject, handler)
	}
	if err != nil {
		if errors.Is(err, nats.ErrBadQueueName) {
			return SubResult{}, fmt.Errorf("%q is not a valid queue group name", queue)
		}
		return SubResult{}, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	m.mu.Lock()
	m.subs[id] = &subscription{sub: sub, subject: subject, queue: queue, excludeSystem: exclude}
	size := len(m.subs)
	m.mu.Unlock()

	return SubResult{ID: id, Subject: subject, Queue: queue, ExcludeSystem: exclude, Size: size}, nil
}

func (m *Manager) Unsubscribe(id uint64) (SubResult, error) {
	m.mu.Lock()
	s, ok := m.subs[id]
	if ok {
		delete(m.subs, id)
	}
	size := len(m.subs)
	m.mu.Unlock()

	if !ok {
		return SubResult{Size: size}, nil
	}
	if err := s.sub.Unsubscribe(); err != nil {
		return SubResult{Size: size}, fmt.Errorf("failed to unsubscribe: %w", err)
	}
	return SubResult{ID: id, Subject: s.subject, Queue: s.queue, ExcludeSystem: s.excludeSystem, Size: size}, nil
}

// Publish sends one message. A non-empty reply sets its reply subject, which
// is how a request is made by hand: the answer goes to a subject of the
// caller's choosing - one this window may be subscribed to - rather than to a
// private inbox that only Request can see.
func (m *Manager) Publish(subject, reply string, data []byte, headers nats.Header) error {
	nc, err := m.conn()
	if err != nil {
		return err
	}
	msg := &nats.Msg{Subject: subject, Reply: reply, Data: data, Header: headers}
	if err := nc.PublishMsg(msg); err != nil {
		// nats.go reports a bad reply subject as the same ErrBadSubject as a
		// bad subject, so name both rather than blame the wrong field.
		if errors.Is(err, nats.ErrBadSubject) && reply != "" {
			return fmt.Errorf("failed to publish: %q or reply subject %q is not a valid subject", subject, reply)
		}
		return fmt.Errorf("failed to publish: %w", err)
	}
	// Publish is buffered; without this the UI would report success before
	// the bytes have left the process, and a failing subject would surface
	// only much later as an async error.
	return nc.Flush()
}

type Reply struct {
	Subject string      `json:"subject"`
	Data    []byte      `json:"-"`
	Headers nats.Header `json:"-"`
}

func (m *Manager) Request(subject string, data []byte, headers nats.Header, timeout time.Duration) (*nats.Msg, error) {
	nc, err := m.conn()
	if err != nil {
		return nil, err
	}
	msg := &nats.Msg{Subject: subject, Data: data, Header: headers}
	reply, err := nc.RequestMsg(msg, timeout)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			return nil, fmt.Errorf("no responder is listening on %s", subject)
		}
		if errors.Is(err, nats.ErrTimeout) {
			return nil, fmt.Errorf("request timed out after %s", timeout)
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return reply, nil
}

// maxGathered caps the replies one gathering request hands on, at the number
// the message log keeps on screen. Replies past it are counted, not sent. A
// var so tests can reach it.
var maxGathered = 200

// Gathered is the tally of one gathering request, once its timeout is up. The
// replies themselves have already gone to the sink.
type Gathered struct {
	// Count is how many replies went to the sink.
	Count int

	// Truncated counts replies that arrived after maxGathered.
	Truncated int
}

// RequestAll sends one request and passes every reply that arrives within the
// timeout to the sink as GatherReply, tagged with gatherID - a scatter-gather.
//
// The replies go out as they arrive, not as one answer when the timeout is up.
// The timeout is the longest anyone waits, not how long the first reply takes:
// with a five second window and responders that answer in milliseconds,
// holding everything back meant five seconds of nothing and then the lot.
//
// Request stops at the first reply because core NATS request-reply works that
// way: every subscriber gets the request, and the requester takes whichever
// answer arrives first. A subject that several things answer, such as
// $SRV.PING or a fleet of devices, needs this instead.
//
// It waits for the full timeout, as `nats req --replies=0` does. It does not
// stop when replies stall, as jsm.go's DoReq does for the services census. A
// stall rule suits $SYS and $SRV, where all responders answer within
// milliseconds of each other. An application responder that takes 400ms over
// a database query would be cut off with no sign that it was, and this log
// must not hide a gap.
//
// The inbox is a plain one, not a NewRespInbox: that shape matches the
// subscription Request shares, which would receive a copy of every reply only
// to discard it.
func (m *Manager) RequestAll(gatherID, subject string, data []byte, headers nats.Header, timeout time.Duration) (Gathered, error) {
	nc, err := m.conn()
	if err != nil {
		return Gathered{}, err
	}

	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return Gathered{}, fmt.Errorf("request failed: %w", err)
	}
	defer sub.Unsubscribe()

	deadline := time.Now().Add(timeout)
	if err := nc.PublishMsg(&nats.Msg{Subject: subject, Reply: inbox, Data: data, Header: headers}); err != nil {
		return Gathered{}, fmt.Errorf("request failed: %w", err)
	}

	var g Gathered
	for {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		reply, err := sub.NextMsg(left)
		if errors.Is(err, nats.ErrTimeout) {
			break
		}
		if errors.Is(err, nats.ErrNoResponders) {
			// The server sends this only when nothing subscribes, so it
			// cannot follow real replies.
			return Gathered{}, fmt.Errorf("no responder is listening on %s", subject)
		}
		if err != nil {
			return Gathered{}, fmt.Errorf("request failed: %w", err)
		}
		if g.Count < maxGathered {
			m.sink.GatherReply(gatherID, reply.Subject, reply.Data, reply.Header)
			g.Count++
		} else {
			g.Truncated++
		}
	}

	if g.Count == 0 {
		return Gathered{}, fmt.Errorf("request timed out after %s", timeout)
	}
	return g, nil
}
