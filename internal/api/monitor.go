package api

import (
	"errors"
	"net/http"

	"github.com/nats-io/nats.go"
	"github.com/stone-age-io/nats-desk/internal/contexts"
	"github.com/stone-age-io/nats-desk/internal/monitor"
	"github.com/stone-age-io/nats-desk/internal/natsconn"
)

func (a *API) registerMonitor(route func(string, http.HandlerFunc)) {
	route("GET /api/monitor/status", a.monitorStatus)
	route("GET /api/monitor/servers", a.monitorServers)
	route("POST /api/monitor/refresh", a.monitorRefresh)
	route("GET /api/monitor/account", a.monitorAccount)

	route("POST /api/monitor/sys", a.monitorConnectSys)
	route("DELETE /api/monitor/sys", a.monitorDisconnectSys)

	route("POST /api/monitor/http", a.monitorSetHTTP)
	route("DELETE /api/monitor/http", a.monitorClearHTTP)
	route("GET /api/monitor/http/{endpoint}", a.monitorHTTPEndpoint)

	route("GET /api/monitor/endpoint/{name}", a.monitorEndpoint)
}

func (a *API) monitorStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.mon.Status())
}

func (a *API) monitorServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.mon.Servers())
}

func (a *API) monitorRefresh(w http.ResponseWriter, r *http.Request) {
	if err := a.mon.RefreshServers(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.mon.Servers())
}

func (a *API) monitorAccount(w http.ResponseWriter, r *http.Request) {
	view, err := a.mon.Account()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// monitorConnectSys opens the second, system-account connection.
//
// It takes either a NATS CLI context name or the same fields the main
// connection form collects. The context path is the good one: the credentials
// stay in the file the CLI already keeps them in, and nats-desk stores nothing.
func (a *API) monitorConnectSys(w http.ResponseWriter, r *http.Request) {
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

	if req.Context != "" {
		url, opts, err := contexts.ConnectOptions(req.Context)
		if err != nil {
			fail(w, err)
			return
		}
		if err := a.mon.ConnectSys(url, opts); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.mon.Status())
		return
	}

	if req.URL == "" {
		fail(w, errors.New("a server URL or a context is required"))
		return
	}

	opt, err := natsconn.AuthOptions{
		CredsText: req.CredsText,
		User:      req.User,
		Pass:      req.Pass,
		Token:     req.Token,
	}.Option()
	if err != nil {
		fail(w, err)
		return
	}

	var opts []nats.Option
	if opt != nil {
		opts = append(opts, opt)
	}
	if err := a.mon.ConnectSys(req.URL, opts); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.mon.Status())
}

func (a *API) monitorDisconnectSys(w http.ResponseWriter, r *http.Request) {
	a.mon.DisconnectSys()
	writeJSON(w, http.StatusOK, a.mon.Status())
}

func (a *API) monitorSetHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Bases []string `json:"bases"`
		CA    string   `json:"ca"`
		// Insecure skips certificate verification. Named for what it is, and
		// never a default.
		Insecure bool `json:"insecure"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := a.mon.SetHTTP(req.Bases, monitor.HTTPOptions{CA: req.CA, Insecure: req.Insecure}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.mon.Status())
}

func (a *API) monitorClearHTTP(w http.ResponseWriter, r *http.Request) {
	a.mon.ClearHTTP()
	writeJSON(w, http.StatusOK, a.mon.Status())
}

// monitorHTTPEndpoint proxies one :8222 endpoint to every configured server.
// The query string is passed through, so the endpoints' own options work.
func (a *API) monitorHTTPEndpoint(w http.ResponseWriter, r *http.Request) {
	res, err := a.mon.FetchHTTP(r.PathValue("endpoint"), r.URL.Query())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// monitorEndpoint runs one endpoint over $SYS, which reaches every server in
// the cluster in a single request.
func (a *API) monitorEndpoint(w http.ResponseWriter, r *http.Request) {
	res, err := a.mon.Endpoint(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
