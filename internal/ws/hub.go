// Package ws is the single push channel to the browser.
//
// One WebSocket carries everything the backend sends unprompted - subscription
// messages, connection status, RTT, and later KV watches and stream tails -
// tagged by a "type" field. One socket rather than several keeps ordering
// intact and means a reconnect re-establishes everything at once.
package ws

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/stone-age-io/nats-desk/internal/monitor"
	"github.com/stone-age-io/nats-desk/internal/natsconn"
)

// A subscription to '>' on a busy server produces messages orders of
// magnitude faster than a browser can render them - measured at ~950k msg/sec
// published against a UI that showed 3.4s of main-thread lag when every
// message was forwarded as its own frame.
//
// Two mechanisms bound the work that reaches the tab:
//
//   - batching: messages that arrive within one flush window are sent as a
//     single frame, so a burst costs one parse and one render pass instead of
//     hundreds.
//   - a hard cap per window: the log only keeps a couple of hundred entries on
//     screen anyway, so forwarding more than that per flush is work whose
//     result is discarded before anyone can read it. Excess is dropped
//     oldest-first - a firehose viewer wants the live edge - and counted, so
//     the UI can say what it missed rather than quietly lying.
const (
	flushInterval = 50 * time.Millisecond
	maxBatch      = 200
	ctrlBuffer    = 64
)

type client struct {
	conn *websocket.Conn

	mu      sync.Mutex
	pending [][]byte
	dropped uint64

	// Status, stats and drop reports are rare and must not be dropped, so
	// they bypass the batching path entirely.
	ctrl chan []byte

	closed chan struct{}
	once   sync.Once
}

func (c *client) stop() {
	c.once.Do(func() { close(c.closed) })
}

// queue adds a message frame, dropping the oldest if the window is full.
func (c *client) queue(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= maxBatch {
		// Drop from the front: the newest messages are the ones worth
		// showing when we cannot show them all.
		c.pending = c.pending[1:]
		c.dropped++
	}
	c.pending = append(c.pending, b)
}

// take swaps out the pending batch and the drop count.
func (c *client) take() ([][]byte, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 && c.dropped == 0 {
		return nil, 0
	}
	batch, dropped := c.pending, c.dropped
	c.pending, c.dropped = nil, 0
	return batch, dropped
}

type Hub struct {
	log *slog.Logger

	mu      sync.RWMutex
	clients map[*client]struct{}

	// Bracketing hooks so the process can exit once the last tab goes away.
	OnConnect    func()
	OnDisconnect func()
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{log: log, clients: map[*client]struct{}{}}
}

// ServeHTTP upgrades the request and runs the client until it goes away.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The Host allowlist in the server package already rejects foreign
	// origins before we get here, and the session cookie is required to
	// reach this handler at all. InsecureSkipVerify only disables the
	// library's own Origin check, which would otherwise duplicate that.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		h.log.Debug("websocket accept failed", "err", err)
		return
	}

	// We never expect a large frame inbound; the UI drives everything else
	// over REST.
	conn.SetReadLimit(64 * 1024)

	c := &client{
		conn:   conn,
		ctrl:   make(chan []byte, ctrlBuffer),
		closed: make(chan struct{}),
	}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	if h.OnConnect != nil {
		h.OnConnect()
	}

	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.stop()
		conn.CloseNow()
		if h.OnDisconnect != nil {
			h.OnDisconnect()
		}
	}()

	ctx := r.Context()
	go h.readPump(ctx, c)
	h.writePump(ctx, c)
}

// readPump exists to notice the client going away. The UI does not currently
// send anything, but without a reader the close handshake is never observed
// and a closed tab would look alive until the next write fails.
func (h *Hub) readPump(ctx context.Context, c *client) {
	defer c.stop()
	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}

func (h *Hub) writePump(ctx context.Context, c *client) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	write := func(b []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.conn.Write(wctx, websocket.MessageText, b)
		cancel()
		return err == nil
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case b := <-c.ctrl:
			if !write(b) {
				return
			}
		case <-ticker.C:
			batch, dropped := c.take()
			if len(batch) == 0 && dropped == 0 {
				continue
			}
			if len(batch) > 0 && !write(batchFrame(batch)) {
				return
			}
			if dropped > 0 {
				b, _ := json.Marshal(map[string]any{"type": "dropped", "count": dropped})
				if !write(b) {
					return
				}
			}
		}
	}
}

// batchFrame wraps already-marshalled frames into one envelope without
// re-encoding them.
func batchFrame(batch [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"type":"batch","frames":[`)
	for i, b := range batch {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(b)
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

// queueMsg batches a droppable frame for every connected client.
func (h *Hub) queueMsg(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.log.Error("marshal frame", "err", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.queue(b)
	}
}

// sendCtrl delivers a frame that must not be dropped.
func (h *Hub) sendCtrl(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.log.Error("marshal frame", "err", err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ctrl <- b:
		default:
			h.log.Warn("control channel full, dropping frame")
		}
	}
}

// ============================================================================
// natsconn.Sink
// ============================================================================

func (h *Hub) Message(subID uint64, subject string, data []byte, headers nats.Header) {
	h.queueMsg(struct {
		Type    string              `json:"type"`
		SubID   uint64              `json:"subId"`
		Subject string              `json:"subject"`
		Data    string              `json:"data"`
		Headers map[string][]string `json:"headers,omitempty"`
		Ts      int64               `json:"ts"`
	}{
		Type:    "msg",
		SubID:   subID,
		Subject: subject,
		Data:    base64.StdEncoding.EncodeToString(data),
		Headers: headers,
		Ts:      time.Now().UnixMilli(),
	})
}

func (h *Hub) Status(state string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	h.sendCtrl(struct {
		Type  string `json:"type"`
		State string `json:"state"`
		Err   string `json:"err,omitempty"`
	}{Type: "status", State: state, Err: msg})
}

func (h *Hub) Stats(rttMillis float64) {
	h.sendCtrl(struct {
		Type string  `json:"type"`
		RTT  float64 `json:"rtt"`
	}{Type: "stats", RTT: rttMillis})
}

// KvChange goes down the control path: key edits are rare and a dropped one
// would leave the key list quietly out of date.
func (h *Hub) KvChange(key, operation string) {
	h.sendCtrl(struct {
		Type      string `json:"type"`
		Key       string `json:"key"`
		Operation string `json:"operation"`
	}{Type: "kv", Key: key, Operation: operation})
}

// MonitorServers pushes the cluster grid. Control path: a server heartbeats
// once every ten seconds, and a grid that quietly missed an update would show
// a stale row with no way to tell.
func (h *Hub) MonitorServers(rows []monitor.ServerRow) {
	h.sendCtrl(struct {
		Type    string              `json:"type"`
		Servers []monitor.ServerRow `json:"servers"`
	}{Type: "monitor_servers", Servers: rows})
}

// MonitorEvent takes the batched path. On a cluster with connection churn,
// $SYS.ACCOUNT.*.CONNECT and DISCONNECT arrive as fast as clients come and go,
// which is a firehose by any other name.
func (h *Hub) MonitorEvent(ev monitor.Event) {
	h.queueMsg(struct {
		Type string `json:"type"`
		monitor.Event
	}{Type: "monitor_event", Event: ev})
}

// MonitorStatus pushes which sources are live. Control path: it is rare, and
// it is what the sources panel draws itself from.
func (h *Hub) MonitorStatus(st monitor.Status) {
	h.sendCtrl(struct {
		Type   string         `json:"type"`
		Status monitor.Status `json:"status"`
	}{Type: "monitor_status", Status: st})
}

// TailMessage takes the batched path: tailing a busy stream is as much of a
// firehose as a wildcard subscription.
func (h *Hub) TailMessage(m natsconn.TailMsg) {
	h.queueMsg(struct {
		Type    string              `json:"type"`
		Seq     uint64              `json:"seq"`
		Subject string              `json:"subject"`
		Data    string              `json:"data"`
		Time    time.Time           `json:"time"`
		Headers map[string][]string `json:"headers,omitempty"`
	}{
		Type:    "tail",
		Seq:     m.Seq,
		Subject: m.Subject,
		Data:    base64.StdEncoding.EncodeToString(m.Data),
		Time:    m.Time,
		Headers: m.Headers,
	})
}
