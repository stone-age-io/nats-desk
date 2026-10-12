package api

import (
	"net/http"
	"os"

	"github.com/stone-age-io/nats-desk/internal/applog"
	"github.com/stone-age-io/nats-desk/internal/autostart"
	"github.com/stone-age-io/nats-desk/internal/buildinfo"
	"github.com/stone-age-io/nats-desk/internal/settings"
)

// The desktop endpoints are about the process, not about NATS. They are the
// only part of the API that would still answer with no NATS server anywhere
// on the network, which is why nothing here goes near the connection manager.
//
// Autostart is stored by the operating system, so it is read back from there;
// quit-on-close has nowhere like that and lives in package settings.

func (a *API) registerDesktop(route func(string, http.HandlerFunc)) {
	route("GET /api/desktop", a.desktopStatus)
	route("PUT /api/desktop/autostart", a.setAutostart)
	route("PUT /api/desktop/quit-on-close", a.setQuitOnClose)
}

func (a *API) desktopStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := autostart.Enabled()
	if err != nil {
		// Not fatal, and not worth failing the whole panel over: report the
		// state we could not read as off and say why alongside it.
		a.log.Warn("could not read the autostart setting", "err", err)
	}

	exe, _ := os.Executable()

	// A broken file reads as the defaults; main has already logged it.
	prefs, _ := settings.Load(settings.Path())

	writeJSON(w, http.StatusOK, map[string]any{
		"version":            buildinfo.Version,
		"executable":         exe,
		"autostartSupported": autostart.Supported(),
		"autostart":          enabled,
		"autostartError":     errText(err),
		"quitOnClose":        prefs.QuitOnClose,
		// Empty when the log is going to a terminal, which is the case where
		// the user can already see it.
		"logPath": applog.Path(),
	})
}

func (a *API) setAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}

	var err error
	if req.Enabled {
		err = autostart.Enable()
	} else {
		err = autostart.Disable()
	}
	if err != nil {
		fail(w, err)
		return
	}

	a.log.Info("autostart changed", "enabled", req.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"autostart": req.Enabled})
}

func (a *API) setQuitOnClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}

	if err := settings.Update(settings.Path(), func(s *settings.Settings) {
		s.QuitOnClose = req.Enabled
	}); err != nil {
		fail(w, err)
		return
	}
	if a.OnQuitOnClose != nil {
		a.OnQuitOnClose(req.Enabled)
	}

	a.log.Info("quit on close changed", "enabled", req.Enabled)
	writeJSON(w, http.StatusOK, map[string]any{"quitOnClose": req.Enabled})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
