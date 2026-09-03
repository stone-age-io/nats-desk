// Package contexts exposes the `nats` CLI's own context store.
//
// This is one of the things moving out of the browser bought us: the contexts
// are files on this machine, and the credentials they point at - .creds files,
// nkey seeds, client certificates - are read by this process at connect time.
// A page in a browser can reach none of that.
//
// The store is the CLI's, not ours. We read and write the same files `nats
// context` does, through the same library it uses, so a context created here
// works from the command line and vice versa.
package contexts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/nats.go"
)

// Summary is one row in the context picker.
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Auth        string `json:"auth"`
	Selected    bool   `json:"selected"`
}

// Detail is a context as the editor sees it: the file's own JSON, unchanged.
type Detail struct {
	Name   string          `json:"name"`
	Path   string          `json:"path"`
	Config json.RawMessage `json:"config"`
}

// List returns every known context, newest CLI selection marked.
//
// Each context is read raw rather than through natscontext.New, because a
// full load expands paths and eagerly resolves the deprecated `nsc` field -
// which shells out to the nsc binary. Listing must not run other programs.
func List() ([]Summary, error) {
	backend := natscontext.NewDefaultFileBackend()
	ctx := context.Background()

	names, err := backend.List(ctx)
	if err != nil {
		return nil, err
	}
	selected := natscontext.SelectedContext()

	out := make([]Summary, 0, len(names))
	for _, name := range names {
		s := Summary{Name: name, Selected: name == selected}

		raw, err := backend.Load(ctx, name)
		if err == nil {
			if c, err := natscontext.NewFromBytesRaw(raw); err == nil {
				s.Description = c.Description()
				s.URL = c.ServerURL()
				s.Auth = authLabel(c)
			}
		}
		// A context we cannot parse still gets a row. Hiding it would leave
		// the user wondering why `nats context ls` shows one more than we do.
		if s.Auth == "" {
			s.Auth = "unreadable"
		}

		out = append(out, s)
	}
	return out, nil
}

// authLabel names the credential the context would actually connect with.
// The order matches natscontext.NATSOptions, so what we show is what would be
// used - Validate rejects more than one anyway, but old files predate it.
func authLabel(c *natscontext.Context) string {
	switch {
	case c.User() != "":
		return "user/password"
	case c.Creds() != "":
		return "creds file"
	case c.NKey() != "":
		return "nkey"
	case c.UserJWT() != "":
		return "user JWT"
	case c.Token() != "":
		return "token"
	case c.WindowsCertStore() != "":
		return "windows cert store"
	default:
		return "none"
	}
}

// Get returns the context file verbatim, for editing.
//
// Verbatim matters: a loaded context has `~` and `$VARS` already expanded, so
// round-tripping through one would quietly rewrite a portable `~/.creds/x.creds`
// into an absolute path belonging to this machine.
func Get(name string) (*Detail, error) {
	raw, err := natscontext.NewDefaultFileBackend().Load(context.Background(), name)
	if err != nil {
		return nil, err
	}
	path, err := natscontext.ContextPath(name)
	if err != nil {
		return nil, err
	}
	return &Detail{Name: name, Path: path, Config: json.RawMessage(raw)}, nil
}

// Save writes a context, creating it if it does not exist. cfg is the file's
// own JSON; Save goes through natscontext so its validation runs - one
// credential type only, a sane name, a usable Windows cert store matcher.
func Save(name string, cfg []byte) error {
	if err := natscontext.ValidateName(name); err != nil {
		return err
	}
	c, err := natscontext.NewFromBytesRaw(cfg)
	if err != nil {
		return fmt.Errorf("not a valid context: %w", err)
	}
	c.Name = name
	return c.Save(name)
}

// Delete removes a context. The CLI refuses to delete the selected one unless
// it is the only one left, and so do we - by way of the same function.
func Delete(name string) error {
	return natscontext.DeleteContext(name)
}

// Select makes name the context the `nats` CLI uses by default. This writes
// outside our own state, so the UI asks for it explicitly rather than doing it
// as a side effect of picking a context to connect with.
func Select(name string) error {
	if !natscontext.IsKnown(name) {
		return fmt.Errorf("no context named %q", name)
	}
	return natscontext.SelectContext(name)
}

// ConnectOptions resolves a context into what nats.Connect needs. Callers add
// their own options afterwards, and theirs win: nats applies options in order.
func ConnectOptions(name string) (string, []nats.Option, error) {
	if name == "" {
		return "", nil, errors.New("a context name is required")
	}
	c, err := natscontext.New(name, true)
	if err != nil {
		return "", nil, err
	}
	opts, err := c.NATSOptions()
	if err != nil {
		return "", nil, err
	}
	return c.ServerURL(), opts, nil
}
