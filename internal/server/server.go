// Package server is the local HTTP surface: it serves the embedded app and
// hosts the REST API and WebSocket that the app talks to.
//
// It listens on loopback only and on a fixed port. Fixed is not a preference:
// an installed PWA's identity - and therefore its stored preferences - is its
// origin, and the origin includes the port. A port that moved between runs
// would strand the user's settings on the previous origin every time.
//
// The fixed port also gives us single-instance behaviour for free. Binding is
// the check: if the address is already taken, another copy is running, and the
// caller can simply point a browser at it and exit. No lockfile, no pid to go
// stale, and the OS reclaims the port if we crash.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// DefaultPort is the fixed loopback port. Changing it orphans the stored
// preferences of anyone who installed the app from the old one.
const DefaultPort = 4111

type Options struct {
	Port   int
	Assets fs.FS
	Logger *slog.Logger

	// Dev relaxes the checks for frontend iteration: auth is skipped and
	// DevOrigin joins the Host allowlist so Vite's dev server can proxy in.
	Dev       bool
	DevOrigin string

	// IdleTimeout is how long to wait after the last WebSocket client
	// disconnects before shutting down. Zero disables idle shutdown.
	IdleTimeout time.Duration
}

type Server struct {
	opts  Options
	token string
	hosts []string
	dev   bool
	log   *slog.Logger

	mux *http.ServeMux
	srv *http.Server
	ln  net.Listener

	mu        sync.Mutex
	clients   int
	idleTimer *time.Timer
	idleOnce  sync.Once
	idle      chan struct{}
}

// ErrAddrInUse reports that another instance already holds the port.
var ErrAddrInUse = errors.New("address already in use")

// New binds the listener and builds the routes. Binding here rather than in
// Serve is deliberate: the caller needs to distinguish "port taken" from any
// other failure before it decides whether to launch a browser and exit.
func New(opts Options) (*Server, error) {
	if opts.Port == 0 {
		opts.Port = DefaultPort
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	addr := fmt.Sprintf("127.0.0.1:%d", opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return nil, fmt.Errorf("%w: %s", ErrAddrInUse, addr)
		}
		return nil, err
	}

	s := &Server{
		opts:  opts,
		token: newToken(),
		dev:   opts.Dev,
		log:   opts.Logger,
		mux:   http.NewServeMux(),
		ln:    ln,
		idle:  make(chan struct{}),
		hosts: []string{
			addr,
			fmt.Sprintf("localhost:%d", opts.Port),
			fmt.Sprintf("[::1]:%d", opts.Port),
		},
	}
	if opts.Dev && opts.DevOrigin != "" {
		s.hosts = append(s.hosts, opts.DevOrigin)
	}

	s.routes()
	s.srv = &http.Server{
		Handler:           s.checkHost(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the WebSocket is a long-lived response and any
		// deadline here would sever it mid-stream.
	}
	return s, nil
}

func (s *Server) routes() {
	// The app shell. claimToken runs first so the startup URL can trade its
	// one-time token for a cookie before anything else looks at the request.
	app := spaHandler(s.opts.Assets)
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.claimToken(w, r) {
			return
		}
		app.ServeHTTP(w, r)
	}))
}

// Mount registers a handler behind the auth gate. Used by the API and
// WebSocket packages so the gate cannot be forgotten at a call site.
func (s *Server) Mount(pattern string, h http.Handler) {
	s.mux.Handle(pattern, s.requireAuth(h))
}

// MountFunc is Mount for a bare handler function.
func (s *Server) MountFunc(pattern string, h http.HandlerFunc) {
	s.Mount(pattern, h)
}

// BaseURL is the address to open for an instance that is already running.
func (s *Server) BaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/", s.opts.Port)
}

// StartURL is BaseURL plus the one-time token, for the browser we launch.
func (s *Server) StartURL() string {
	return fmt.Sprintf("%s?token=%s", s.BaseURL(), s.token)
}

// Serve runs until Shutdown is called. Returns nil on clean shutdown.
func (s *Server) Serve() error {
	s.log.Info("listening", "url", s.BaseURL())
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Idle is closed once the idle timeout elapses with no clients connected.
// main selects on it to exit when the last browser tab goes away.
func (s *Server) Idle() <-chan struct{} { return s.idle }

// ClientConnected and ClientDisconnected bracket a WebSocket session.
//
// Browsers drop the socket on navigation and reload, so the timeout has to be
// long enough to survive a refresh without killing the process underneath the
// user. Counting sockets rather than watching for process exit also means a
// second tab keeps us alive.
func (s *Server) ClientConnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients++
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

func (s *Server) ClientDisconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients > 0 {
		s.clients--
	}
	if s.clients > 0 || s.opts.IdleTimeout <= 0 {
		return
	}
	s.idleTimer = time.AfterFunc(s.opts.IdleTimeout, func() {
		s.mu.Lock()
		n := s.clients
		s.mu.Unlock()
		if n == 0 {
			s.idleOnce.Do(func() { close(s.idle) })
		}
	})
}
