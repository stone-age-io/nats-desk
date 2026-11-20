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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
)

// Summary is one row in the context picker.
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Auth        string `json:"auth"`
	Selected    bool   `json:"selected"`

	// Color is the context's color_scheme, which the nats CLI uses to colour
	// its tables (blue, cyan, green, magenta, red, yellow). The UI colours the
	// connection pill with it, so a production context looks different from
	// the others without nats-desk storing anything of its own.
	Color string `json:"color,omitempty"`
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
				s.Color = c.ColorScheme()
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

// ErrExists is Create refusing to replace a context or a creds file.
var ErrExists = errors.New("already exists")

// Create writes a new context and refuses to replace an existing one. The
// form saves a typed connection this way, and a profile from browser storage
// becomes a context the nats CLI can use too.
//
// credsText, when given, is a .creds file's contents. A profile holds the
// file's text, not a path, because a browser can only read an upload. A
// context can only point at a file. natscontext can inline creds as a data:
// URI, but nats CLI 0.3.0 reads that as a path and fails (tested), so such a
// context would work here and break the CLI. So the text is written to
// CredsPath(name) and the context points at it. Neither file is overwritten,
// and the creds file is removed again if the context cannot be saved.
func Create(name string, cfg []byte, credsText string) (credsPath string, err error) {
	if err := natscontext.ValidateName(name); err != nil {
		return "", err
	}
	if natscontext.IsKnown(name) {
		return "", fmt.Errorf("a context named %q %w", name, ErrExists)
	}

	var fields map[string]any
	if err := json.Unmarshal(cfg, &fields); err != nil {
		return "", fmt.Errorf("not a valid context: %w", err)
	}
	if fields == nil {
		return "", errors.New("not a valid context: expected a JSON object")
	}

	if strings.TrimSpace(credsText) != "" {
		if err := checkCreds(credsText); err != nil {
			return "", err
		}
		// perr, not err: a := here would shadow the named result, and the
		// deferred cleanup below would then check the wrong variable.
		path, perr := CredsPath(name)
		if perr != nil {
			return "", perr
		}
		if werr := writeNew(path, []byte(credsText)); werr != nil {
			return "", werr
		}
		defer func() {
			if err != nil {
				_ = os.Remove(path)
			}
		}()
		fields["creds"] = path
		credsPath = path
	}

	raw, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	if err = Save(name, raw); err != nil {
		return "", err
	}
	return credsPath, nil
}

// CredsPath is where Create writes a context's creds: a creds folder next to
// the CLI's own context folder, <config>/nats/creds/<name>.creds. It is
// worked out from natscontext.ContextPath, so it moves with XDG_CONFIG_HOME
// the same way the contexts do, and it does not depend on nats-desk's own
// directory, which a CLI context should not need.
func CredsPath(name string) (string, error) {
	p, err := natscontext.ContextPath(name) // <config>/nats/context/<name>.json
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(p)), "creds", name+".creds"), nil
}

// writeNew creates path with data and fails if it already exists. 0600 and
// 0700 matter on Unix. On Windows the ACL inherited from the user profile is
// what protects the file, as it is for the contexts themselves.
func writeNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s %w; remove it or choose another name", path, ErrExists)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// checkCreds parses the text the way a connect would, so that a context
// pointing at an unusable file is never written.
func checkCreds(text string) error {
	raw := []byte(strings.ReplaceAll(text, "\r\n", "\n"))
	if _, err := jwt.ParseDecoratedJWT(raw); err != nil {
		return fmt.Errorf("invalid .creds file: %w", err)
	}
	if _, err := jwt.ParseDecoratedNKey(raw); err != nil {
		return fmt.Errorf("invalid .creds file: %w", err)
	}
	return nil
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
