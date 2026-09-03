package api

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/nats-io/nats.go/jetstream"
)

// KV keys may contain '/' and '.', so the path parameter is a trailing
// wildcard rather than a single segment - "{key}" would truncate
// "config/db/host" at the first slash.

func (a *API) registerKV(route func(string, http.HandlerFunc)) {
	route("GET /api/kv/buckets", a.kvBuckets)
	route("POST /api/kv/buckets", a.kvCreateBucket)
	route("PUT /api/kv/buckets", a.kvUpdateBucket)
	route("DELETE /api/kv/buckets/{bucket}", a.kvDestroyBucket)
	route("POST /api/kv/open", a.kvOpen)
	route("POST /api/kv/watch", a.kvStartWatch)
	route("DELETE /api/kv/watch", a.kvStopWatch)
	route("GET /api/kv/status", a.kvStatus)
	route("GET /api/kv/keys", a.kvKeys)
	route("GET /api/kv/keys/{key...}", a.kvGet)
	route("PUT /api/kv/keys/{key...}", a.kvPut)
	route("DELETE /api/kv/keys/{key...}", a.kvDelete)
	route("POST /api/kv/purge/{key...}", a.kvPurge)
	route("GET /api/kv/history/{key...}", a.kvHistory)
}

func (a *API) kvBuckets(w http.ResponseWriter, r *http.Request) {
	list, err := a.mgr.KvBuckets(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) kvCreateBucket(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.KeyValueConfig
	if !decode(w, r, &cfg) {
		return
	}
	if cfg.Bucket == "" {
		fail(w, errors.New("a bucket name is required"))
		return
	}
	if err := a.mgr.CreateKvBucket(r.Context(), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) kvUpdateBucket(w http.ResponseWriter, r *http.Request) {
	var cfg jetstream.KeyValueConfig
	if !decode(w, r, &cfg) {
		return
	}
	if cfg.Bucket == "" {
		fail(w, errors.New("a bucket name is required"))
		return
	}
	if err := a.mgr.UpdateKvBucket(r.Context(), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) kvDestroyBucket(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.DestroyKvBucket(r.Context(), r.PathValue("bucket")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// kvOpen selects the bucket. It deliberately does NOT start the watcher:
// WatchAll replays all existing keys the moment it starts, and the client
// cannot have its handler registered until this call returns. Watching is a
// separate step so the replay cannot outrun the listener.
func (a *API) kvOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bucket string `json:"bucket"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Bucket == "" {
		fail(w, errors.New("a bucket name is required"))
		return
	}
	if err := a.mgr.OpenKvBucket(r.Context(), req.Bucket); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucket": req.Bucket})
}

func (a *API) kvStartWatch(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.WatchKvBucket(r.Context()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"watching": true})
}

func (a *API) kvStopWatch(w http.ResponseWriter, r *http.Request) {
	a.mgr.StopKvWatcher()
	writeJSON(w, http.StatusOK, map[string]any{"watching": false})
}

func (a *API) kvStatus(w http.ResponseWriter, r *http.Request) {
	st, err := a.mgr.KvStatus(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) kvKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.mgr.KvKeys(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *API) kvGet(w http.ResponseWriter, r *http.Request) {
	e, err := a.mgr.KvGet(r.Context(), r.PathValue("key"))
	if err != nil {
		fail(w, err)
		return
	}
	if e == nil {
		// A missing key is a normal answer, not an error - the UI shows an
		// empty editor ready to create it.
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":       e.Key,
		"value":     base64.StdEncoding.EncodeToString(e.Value),
		"revision":  e.Revision,
		"created":   e.Created,
		"operation": e.Operation,
	})
}

func (a *API) kvPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &req) {
		return
	}
	value, err := base64.StdEncoding.DecodeString(req.Value)
	if err != nil {
		fail(w, errors.New("value was not valid base64"))
		return
	}
	rev, err := a.mgr.KvPut(r.Context(), r.PathValue("key"), value)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev})
}

func (a *API) kvDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.KvDelete(r.Context(), r.PathValue("key")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) kvPurge(w http.ResponseWriter, r *http.Request) {
	if err := a.mgr.KvPurge(r.Context(), r.PathValue("key")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) kvHistory(w http.ResponseWriter, r *http.Request) {
	hist, err := a.mgr.KvHistory(r.Context(), r.PathValue("key"))
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(hist))
	for _, e := range hist {
		out = append(out, map[string]any{
			"revision":  e.Revision,
			"operation": e.Operation,
			"value":     base64.StdEncoding.EncodeToString(e.Value),
			"created":   e.Created,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
