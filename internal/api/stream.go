package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"github.com/nats-io/nats.go/jetstream"
)

// Default cap on a message-range fetch. The UI defaults to a 50-sequence
// window; this only bounds what a hand-edited request can ask for.
const defaultMsgLimit = 500

func (a *API) registerStreams(route func(string, http.HandlerFunc)) {
	route("GET /api/streams", a.streams)
	route("POST /api/streams", a.createStream)
	route("PUT /api/streams", a.updateStream)
	route("GET /api/streams/{name}", a.streamInfo)
	route("DELETE /api/streams/{name}", a.deleteStream)
	route("POST /api/streams/{name}/purge", a.purgeStream)
	route("GET /api/streams/{name}/messages", a.streamMessages)
	route("POST /api/streams/{name}/tail", a.startTail)

	// Stopping is its own path rather than DELETE on the stream's tail:
	// there is only ever one tail, and the UI stops it without necessarily
	// knowing which stream it belonged to.
	route("DELETE /api/tail", a.stopTail)

	route("GET /api/streams/{name}/consumers", a.consumers)
	route("POST /api/streams/{name}/consumers", a.createConsumer)
	route("GET /api/streams/{name}/consumers/{consumer}", a.consumerInfo)
	route("PUT /api/streams/{name}/consumers/{consumer}", a.updateConsumer)
	route("DELETE /api/streams/{name}/consumers/{consumer}", a.deleteConsumer)
}

// ============================================================================
// STREAMS
// ============================================================================

func (a *API) streams(w http.ResponseWriter, r *http.Request) {
	list, err := a.mgr.Streams(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) streamInfo(w http.ResponseWriter, r *http.Request) {
	info, err := a.mgr.StreamInfo(r.Context(), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) createStream(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.StreamConfig
	if !decode(w, r, &cfg) {
		return
	}
	if cfg.Name == "" {
		fail(w, errors.New("a stream name is required"))
		return
	}
	if err := a.mgr.CreateStream(r.Context(), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) updateStream(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.StreamConfig
	if !decode(w, r, &cfg) {
		return
	}
	if cfg.Name == "" {
		fail(w, errors.New("a stream name is required"))
		return
	}
	if err := a.mgr.UpdateStream(r.Context(), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) purgeStream(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.PurgeStream(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) deleteStream(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.DeleteStream(r.Context(), r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ============================================================================
// MESSAGES
// ============================================================================

func (a *API) streamMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := strconv.ParseUint(q.Get("start"), 10, 64)
	end, _ := strconv.ParseUint(q.Get("end"), 10, 64)
	max, _ := strconv.Atoi(q.Get("max"))
	if max <= 0 || max > defaultMsgLimit {
		max = defaultMsgLimit
	}

	msgs, err := a.mgr.StreamMessageRange(r.Context(), r.PathValue("name"), start, end, q.Get("filter"), max)
	if err != nil {
		fail(w, err)
		return
	}

	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"seq":     m.Seq,
			"subject": m.Subject,
			"data":    base64.StdEncoding.EncodeToString(m.Data),
			"time":    m.Time,
			"headers": m.Headers,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) startTail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filter string `json:"filter"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := a.mgr.StartStreamTail(r.Context(), r.PathValue("name"), req.Filter); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tailing": true})
}

func (a *API) stopTail(w http.ResponseWriter, r *http.Request) {
	a.mgr.StopStreamTail()
	writeJSON(w, http.StatusOK, map[string]any{"tailing": false})
}

// ============================================================================
// CONSUMERS
// ============================================================================

func (a *API) consumers(w http.ResponseWriter, r *http.Request) {
	list, err := a.mgr.Consumers(r.Context(), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) consumerInfo(w http.ResponseWriter, r *http.Request) {
	info, err := a.mgr.ConsumerInfo(r.Context(), r.PathValue("name"), r.PathValue("consumer"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) createConsumer(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.ConsumerConfig
	if !decode(w, r, &cfg) {
		return
	}
	if err := a.mgr.CreateConsumer(r.Context(), r.PathValue("name"), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) updateConsumer(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.ConsumerConfig
	if !decode(w, r, &cfg) {
		return
	}
	if err := a.mgr.UpdateConsumer(r.Context(), r.PathValue("name"), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) deleteConsumer(w http.ResponseWriter, r *http.Request) {
	err := a.mgr.DeleteConsumer(r.Context(), r.PathValue("name"), r.PathValue("consumer"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
