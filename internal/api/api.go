// Package api is the REST half of the backend: everything the UI asks for and
// waits on. Anything the backend sends unprompted goes over the WebSocket in
// package ws instead.
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stone-age-io/nats-desk/internal/monitor"
	"github.com/stone-age-io/nats-desk/internal/natsconn"
)

// Mounter is the subset of server.Server this package needs, so the two
// packages do not have to import each other.
type Mounter interface {
	Mount(pattern string, h http.Handler)
}

type API struct {
	mgr *natsconn.Manager
	mon *monitor.Monitor
	log *slog.Logger

	// OnQuitOnClose is told when the quit-on-close setting changes, so the
	// running process honours it at once rather than from its next start.
	OnQuitOnClose func(bool)
}

func New(mgr *natsconn.Manager, mon *monitor.Monitor, log *slog.Logger) *API {
	return &API{mgr: mgr, mon: mon, log: log}
}

func (a *API) Register(m Mounter) {
	route := func(pattern string, h http.HandlerFunc) { m.Mount(pattern, h) }

	route("POST /api/connect", a.connect)
	route("POST /api/disconnect", a.disconnect)
	route("GET /api/status", a.status)
	route("GET /api/info", a.info)
	route("POST /api/publish", a.publish)
	route("POST /api/request", a.request)
	route("POST /api/sub", a.subscribe)
	route("DELETE /api/sub/{id}", a.unsubscribe)

	a.registerKV(route)
	a.registerStreams(route)
	a.registerContexts(route)
	a.registerMonitor(route)
	a.registerServices(route)
	a.registerDesktop(route)
}

// ============================================================================
// PLUMBING
// ============================================================================

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// fail maps an error to a status the UI can act on. ErrNotConnected is 409
// rather than 500 so "you are not connected" reads differently from "the
// server rejected that".
func fail(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, natsconn.ErrNotConnected) {
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed request body"})
		return false
	}
	return true
}

// ============================================================================
// CONNECTION
// ============================================================================

func (a *API) connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Context   string `json:"context"`
		URL       string `json:"url"`
		CredsText string `json:"credsText"`
		User      string `json:"user"`
		Pass      string `json:"pass"`
		Token     string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}

	// A context carries its own URL and credentials, so it is the whole
	// request - the form fields do not apply on top of it.
	if req.Context != "" {
		a.connectContext(w, req.Context)
		return
	}

	if req.URL == "" {
		fail(w, errors.New("a server URL is required"))
		return
	}

	err := a.mgr.Connect(req.URL, natsconn.AuthOptions{
		CredsText: req.CredsText,
		User:      req.User,
		Pass:      req.Pass,
		Token:     req.Token,
	})
	if err != nil {
		fail(w, err)
		return
	}

	info, _ := a.mgr.ServerInfo()
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "info": info})
}

func (a *API) disconnect(w http.ResponseWriter, r *http.Request) {
	a.mgr.Disconnect()
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"connected": a.mgr.IsConnected()})
}

func (a *API) info(w http.ResponseWriter, r *http.Request) {
	info, err := a.mgr.ServerInfo()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// ============================================================================
// MESSAGING
// ============================================================================

// headers arrive as {name: [values]} so a repeated header survives the trip.
func toNatsHeader(in map[string][]string) nats.Header {
	if len(in) == 0 {
		return nil
	}
	h := nats.Header{}
	for k, vs := range in {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return h
}

func fromNatsHeader(h nats.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	return h
}

func (a *API) publish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject string              `json:"subject"`
		Reply   string              `json:"reply"`
		Data    string              `json:"data"`
		Headers map[string][]string `json:"headers"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Subject == "" {
		fail(w, errors.New("a subject is required"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		fail(w, errors.New("payload was not valid base64"))
		return
	}
	if err := a.mgr.Publish(req.Subject, req.Reply, data, toNatsHeader(req.Headers)); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func replyJSON(m *nats.Msg) map[string]any {
	return map[string]any{
		"subject": m.Subject,
		"data":    base64.StdEncoding.EncodeToString(m.Data),
		"headers": fromNatsHeader(m.Header),
	}
}

// request takes the first reply, or with "all" every reply within the
// timeout. The two give different shapes. The first reply comes back as the
// answer. With "all" the replies go over the push channel as they arrive,
// tagged with gatherId, and the answer, once the timeout is up, is only the
// tally: {count, truncated}.
//
// The browser picks the gather ID, not us. The first reply can be pushed
// before this handler has answered, so an ID handed back in the answer would
// arrive after the replies it was meant to label.
func (a *API) request(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject   string              `json:"subject"`
		Data      string              `json:"data"`
		Headers   map[string][]string `json:"headers"`
		TimeoutMs int                 `json:"timeoutMs"`
		All       bool                `json:"all"`
		GatherID  string              `json:"gatherId"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Subject == "" {
		fail(w, errors.New("a subject is required"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		fail(w, errors.New("payload was not valid base64"))
		return
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	if req.All {
		if req.GatherID == "" {
			fail(w, errors.New("a gather id is required"))
			return
		}
		g, err := a.mgr.RequestAll(req.GatherID, req.Subject, data, toNatsHeader(req.Headers), timeout)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": g.Count, "truncated": g.Truncated})
		return
	}

	reply, err := a.mgr.Request(req.Subject, data, toNatsHeader(req.Headers), timeout)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replyJSON(reply))
}

// ============================================================================
// SUBSCRIPTIONS
// ============================================================================

func (a *API) subscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Subject       string `json:"subject"`
		Queue         string `json:"queue"`
		ExcludeSystem bool   `json:"excludeSystem"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Subject == "" {
		fail(w, errors.New("a subject is required"))
		return
	}
	res, err := a.mgr.Subscribe(req.Subject, req.Queue, req.ExcludeSystem)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) unsubscribe(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, errors.New("bad subscription id"))
		return
	}
	res, err := a.mgr.Unsubscribe(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
