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
	"github.com/stone-age-io/nats-desk/internal/browse"
	"github.com/stone-age-io/nats-desk/internal/natsconn"
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
		idleTimeout = flag.Duration("idle-timeout", 0, "exit this long after the last client disconnects (0 disables)")
		verbose     = flag.Bool("v", false, "verbose logging")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	assets, err := frontend.FS()
	if err != nil {
		return fmt.Errorf("load embedded assets: %w", err)
	}

	srv, err := server.New(server.Options{
		Port:        *port,
		Assets:      assets,
		Logger:      log,
		Dev:         *dev,
		DevOrigin:   *devOrigin,
		IdleTimeout: *idleTimeout,
	})
	if err != nil {
		// Port taken means we are the second copy. Hand the running instance
		// the user's intent - a focused window - and get out of its way.
		// This is the whole single-instance mechanism.
		if errors.Is(err, server.ErrAddrInUse) {
			url := fmt.Sprintf("http://127.0.0.1:%d/", *port)
			fmt.Printf("nats-desk is already running at %s\n", url)
			if !*noBrowser {
				if oerr := browse.Open(url); oerr != nil {
					log.Warn("could not open browser", "err", oerr)
				}
			}
			return nil
		}
		return err
	}

	// Wiring: the hub is the sink the NATS manager pushes into, and the hub's
	// connect/disconnect hooks drive the idle shutdown in the server.
	hub := ws.NewHub(log)
	hub.OnConnect = srv.ClientConnected
	hub.OnDisconnect = srv.ClientDisconnected

	mgr := natsconn.New(hub)
	defer mgr.Disconnect()

	srv.Mount("/ws", hub)
	api.New(mgr, log).Register(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	if *dev {
		// In -dev the app is served by Vite, not by us, so opening our own
		// port would just show a stale build. Auth is off in dev, so the
		// bare URL is enough.
		fmt.Printf("nats-desk backend listening on %s (dev mode: auth disabled)\n", srv.BaseURL())
	} else if *noBrowser {
		// Print the token URL, not the bare one - without the token there is
		// no way to reach the API, so the bare URL would be a dead end.
		fmt.Printf("open this URL to use nats-desk:\n  %s\n", srv.StartURL())
	} else if err := browse.Open(srv.StartURL()); err != nil {
		log.Warn("could not open browser", "err", err)
		fmt.Printf("open this URL to use nats-desk:\n  %s\n", srv.StartURL())
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
