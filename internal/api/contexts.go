package api

import (
	"encoding/json"
	"net/http"

	"github.com/stone-age-io/nats-desk/internal/contexts"
)

func (a *API) registerContexts(route func(string, http.HandlerFunc)) {
	route("GET /api/contexts", a.listContexts)
	route("GET /api/contexts/{name}", a.getContext)
	route("PUT /api/contexts/{name}", a.saveContext)
	route("DELETE /api/contexts/{name}", a.deleteContext)
	route("POST /api/contexts/{name}/select", a.selectContext)
}

func (a *API) listContexts(w http.ResponseWriter, r *http.Request) {
	list, err := contexts.List()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getContext(w http.ResponseWriter, r *http.Request) {
	detail, err := contexts.Get(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// saveContext takes the context file's own JSON. The UI edits it directly
// rather than through a form, so a field nats-desk has never heard of - a new
// CLI option, a Windows cert store matcher - survives being edited here.
func (a *API) saveContext(w http.ResponseWriter, r *http.Request) {
	var cfg json.RawMessage
	if !decode(w, r, &cfg) {
		return
	}
	if err := contexts.Save(r.PathValue("name"), cfg); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) deleteContext(w http.ResponseWriter, r *http.Request) {
	if err := contexts.Delete(r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// selectContext changes which context the `nats` CLI uses by default. It is
// its own call, and its own button in the UI, because it writes state shared
// with every other NATS tool on the machine - not something to do as a side
// effect of picking a context to connect with.
func (a *API) selectContext(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := contexts.Select(name); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"selected": name})
}

// connectContext dials using a context's own resolved options.
func (a *API) connectContext(w http.ResponseWriter, name string) {
	url, opts, err := contexts.ConnectOptions(name)
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.mgr.ConnectWith(url, opts); err != nil {
		fail(w, err)
		return
	}
	info, _ := a.mgr.ServerInfo()
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "url": url, "info": info})
}
