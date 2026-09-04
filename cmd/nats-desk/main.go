// Command nats-desk is a local NATS client: it serves its own UI on loopback
// and talks to NATS over the native protocol, so it works against any server
// without the websocket listener a browser-only client would require.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stone-age-io/nats-desk/frontend"
	"github.com/stone-age-io/nats-desk/internal/api"
	"github.com/stone-age-io/nats-desk/internal/applog"
	"github.com/stone-age-io/nats-desk/internal/autostart"
	"github.com/stone-age-io/nats-desk/internal/browse"
	"github.com/stone-age-io/nats-desk/internal/buildinfo"
	"github.com/stone-age-io/nats-desk/internal/monitor"
	"github.com/stone-age-io/nats-desk/internal/natsconn"
	"github.com/stone-age-io/nats-desk/internal/scheme"
	"github.com/stone-age-io/nats-desk/internal/server"
	"github.com/stone-age-io/nats-desk/internal/ws"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nats-desk:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port        = flag.Int("port", server.DefaultPort, "loopback port to listen on")
		dev         = flag.Bool("dev", false, "developer mode: skip auth, allow the Vite dev origin")
		devOrigin   = flag.String("dev-origin", "localhost:5173", "extra allowed Host in -dev")
		noBrowser   = flag.Bool("no-browser", false, "do not open a browser on start")
		tab         = flag.Bool("tab", false, "open an ordinary browser tab instead of an app window")
		idleTimeout = flag.Duration("idle-timeout", 0, "exit this long after the last client disconnects (0 disables)")
		showVersion = flag.Bool("version", false, "print the version and exit")
		verbose     = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("nats-desk", buildinfo.Version)
		return nil
	}

	log := applog.Setup(*verbose)

	// A natsdesk:// argument means a browser sent us, not a person at a
	// prompt: the installed app found nothing listening and used the URL
	// scheme to start us. Its window is already open and polling, so the
	// default is to get the port up and stay out of the way. See
	// internal/scheme.
	action := ""
	if args := flag.Args(); len(args) > 0 {
		if a, ok := scheme.Action(args[0]); ok {
			action = a
			log.Debug("started by a URL scheme handler", "action", action)
			if action != scheme.ActionOpen {
				*noBrowser = true
			}
		}
	}

	assets, err := frontend.FS()
	if err != nil {
		return fmt.Errorf("load embedded assets: %w", err)
	}

	// No stored token in -dev: auth is off there, and writing a credential to
	// disk for a mode that does not check it is pure downside.
	tokenPath := ""
	if !*dev {
		tokenPath = server.TokenPath()
	}

	srv, err := server.New(server.Options{
		Port:        *port,
		Assets:      assets,
		Logger:      log,
		Dev:         *dev,
		DevOrigin:   *devOrigin,
		IdleTimeout: *idleTimeout,
		TokenPath:   tokenPath,
	})
	if err != nil {
		// Port taken means we are the second copy. Hand the running instance
		// the user's intent - a focused window - and get out of its way.
		// This is the whole single-instance mechanism.
		if errors.Is(err, server.ErrAddrInUse) {
			return openRunningInstance(*port, tokenPath, *noBrowser, *tab, log)
		}
		return err
	}

	// Both of these name an absolute path to this executable, so both have to
	// be re-checked on every start: a moved or re-downloaded binary silently
	// stops being the one that gets launched. Neither is worth failing over.
	if !*dev {
		if err := scheme.Register(); err != nil {
			log.Warn("could not register the natsdesk:// handler", "err", err)
		}
		if err := autostart.Sync(); err != nil {
			log.Warn("could not update the autostart entry", "err", err)
		}
		if *idleTimeout > 0 {
			if on, _ := autostart.Enabled(); on {
				log.Warn("autostart is on and -idle-timeout is set; nothing will restart the app after it exits",
					"idle-timeout", *idleTimeout)
			}
		}
	}

	// Wiring: the hub is the sink the NATS manager pushes into, and the hub's
	// connect/disconnect hooks drive the idle shutdown in the server.
	hub := ws.NewHub(log)
	hub.OnConnect = srv.ClientConnected
	hub.OnDisconnect = srv.ClientDisconnected

	mgr := natsconn.New(hub)
	defer mgr.Disconnect()

	// Monitoring borrows the data connection for the account-scoped endpoints
	// and opens its own for anything system-account or HTTP.
	mon := monitor.New(mgr, hub, log)
	defer mon.Close()

	srv.Mount("/ws", hub)
	api.New(mgr, mon, log).Register(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	switch {
	case *dev:
		// In -dev the app is served by Vite, not by us, so opening our own
		// port would just show a stale build. Auth is off in dev, so the
		// bare URL is enough.
		fmt.Printf("nats-desk backend listening on %s (dev mode: auth disabled)\n", srv.BaseURL())
	case *noBrowser:
		// Print the token URL, not the bare one - without the token there is
		// no way to reach the API, so the bare URL would be a dead end. To
		// stdout only: the log may be a file that outlives the run, and this
		// string is a credential.
		fmt.Printf("open this URL to use nats-desk:\n  %s\n", srv.StartURL())
		log.Info("started without a browser", "url", srv.BaseURL())
	default:
		openUI(srv.StartURL(), *tab, log)
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	case <-srv.Idle():
		log.Info("no clients connected, shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// openUI launches the app window, or an ordinary tab if asked.
//
// An app window is the default because it is what the request "make this feel
// native" actually resolves to: no address bar, no tab strip, its own taskbar
// button. It needs nothing installed and it cannot go stale, because the
// process that opens it is by definition running.
func openUI(url string, tab bool, log *slog.Logger) {
	open := browse.OpenApp
	if tab {
		open = browse.Open
	}
	if err := open(url); err != nil {
		log.Warn("could not open browser", "err", err)
		fmt.Printf("open this URL to use nats-desk:\n  %s\n", url)
	}
}

// openRunningInstance handles a second launch while a first is already
// listening. The stored token is what makes this work in a browser that has
// no cookie yet - a different browser, or a profile that has never been here.
// Without one, the bare URL is still worth opening: a browser that does hold
// the cookie is authenticated already.
func openRunningInstance(port int, tokenPath string, noBrowser, tab bool, log *slog.Logger) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	if tok := server.ReadToken(tokenPath); tok != "" {
		url += "?token=" + tok
	}

	log.Info("another copy is already running", "port", port)
	if noBrowser {
		return nil
	}
	openUI(url, tab, log)
	return nil
}
