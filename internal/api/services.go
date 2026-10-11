package api

import (
	"net/http"

	"github.com/stone-age-io/nats-desk/internal/services"
)

// Requests to an endpoint are not here. They are ordinary requests to the
// endpoint's subject, so the UI sends them through /api/request like any
// other - one request path, one set of timeout and no-responder messages.

func (a *API) registerServices(route func(string, http.HandlerFunc)) {
	route("GET /api/services", a.servicesDiscover)
	route("GET /api/services/{name}/{id}/stats", a.servicesStats)
}

func (a *API) servicesDiscover(w http.ResponseWriter, r *http.Request) {
	nc, err := a.mgr.Conn()
	if err != nil {
		fail(w, err)
		return
	}
	c, err := services.Discover(nc)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (a *API) servicesStats(w http.ResponseWriter, r *http.Request) {
	nc, err := a.mgr.Conn()
	if err != nil {
		fail(w, err)
		return
	}
	stats, err := services.Stats(nc, r.PathValue("name"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
