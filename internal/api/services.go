package api

import (
	"errors"
	"net/http"

	"github.com/stone-age-io/nats-desk/internal/services"
)

// Requests to an endpoint are not here. They are ordinary requests to the
// endpoint's subject, so the UI sends them through /api/request like any
// other - one request path, one set of timeout and no-responder messages.

func (a *API) registerServices(route func(string, http.HandlerFunc)) {
	route("GET /api/services", a.servicesList)
	route("POST /api/services/refresh", a.servicesRefresh)
	route("POST /api/services/{name}/{id}/stats", a.servicesStats)
}

func (a *API) servicesList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.Snapshot())
}

func (a *API) servicesRefresh(w http.ResponseWriter, r *http.Request) {
	snap, err := a.svc.Refresh()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (a *API) servicesStats(w http.ResponseWriter, r *http.Request) {
	in, err := a.svc.InstanceStats(r.PathValue("name"), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, services.ErrUnknownInstance) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}
