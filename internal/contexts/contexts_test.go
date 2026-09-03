package contexts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/jsm.go/natscontext"
)

// isolate points natscontext's default registry at a temp directory. The
// package reads XDG_CONFIG_HOME on every call and caches nothing, so this is
// enough to keep a test off the developer's real `nats` contexts.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func save(t *testing.T, name string, cfg map[string]any) {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(name, raw); err != nil {
		t.Fatalf("Save(%q): %v", name, err)
	}
}

func loadBack(t *testing.T, name string) map[string]any {
	t.Helper()
	d, err := Get(name)
	if err != nil {
		t.Fatalf("Get(%q): %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(d.Config, &out); err != nil {
		t.Fatalf("stored context is not JSON: %v", err)
	}
	return out
}

// The editor round-trips the whole file, not a form we mapped by hand. A field
// nats-desk has never heard of has to come back out the way it went in - that
// is the entire reason the UI edits raw JSON.
func TestSaveKeepsFieldsWeDoNotModel(t *testing.T) {
	isolate(t)

	save(t, "edge", map[string]any{
		"url":          "nats://example.net:4222",
		"inbox_prefix": "_DESK",
		"tls_first":    true,
		"socks_proxy":  "socks5://127.0.0.1:1080",
	})

	got := loadBack(t, "edge")
	for field, want := range map[string]any{
		"inbox_prefix": "_DESK",
		"tls_first":    true,
		"socks_proxy":  "socks5://127.0.0.1:1080",
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %v", field, got[field], want)
		}
	}
}

// Get must not hand back an expanded context. A loaded one has already had `~`
// and $VARS resolved against this machine, so editing through it would rewrite
// a portable "~/x.creds" into an absolute path the moment the user hit save.
func TestGetDoesNotExpandPaths(t *testing.T) {
	isolate(t)

	save(t, "portable", map[string]any{
		"url":   "nats://example.net:4222",
		"creds": "~/.creds/user.creds",
	})

	if got := loadBack(t, "portable")["creds"]; got != "~/.creds/user.creds" {
		t.Errorf("creds = %v, want the unexpanded ~/.creds/user.creds", got)
	}
}

// Save routes through natscontext so its validation applies. Two credential
// types is the mistake worth catching: the connection would silently use one
// of them and ignore the other.
func TestSaveRejectsTwoCredentialTypes(t *testing.T) {
	isolate(t)

	err := Save("confused", json.RawMessage(`{"user":"bob","creds":"/tmp/x.creds"}`))
	if err == nil {
		t.Fatal("saved a context with both a user and a creds file")
	}
	if !strings.Contains(err.Error(), "too many types of credentials") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestSaveRejectsPathTraversal(t *testing.T) {
	isolate(t)

	if err := Save("../escape", json.RawMessage(`{"url":"nats://x:4222"}`)); err == nil {
		t.Fatal("accepted a context name that climbs out of the context directory")
	}
}

// List marks the CLI's selected context, which is what the picker labels.
func TestListMarksTheSelectedContext(t *testing.T) {
	isolate(t)

	save(t, "one", map[string]any{"url": "nats://one:4222", "user": "u"})
	save(t, "two", map[string]any{"url": "nats://two:4222"})

	if err := Select("two"); err != nil {
		t.Fatalf("Select: %v", err)
	}

	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d contexts, want 2", len(list))
	}

	byName := map[string]Summary{}
	for _, s := range list {
		byName[s.Name] = s
	}
	if !byName["two"].Selected {
		t.Error("two is the selected context but is not marked")
	}
	if byName["one"].Selected {
		t.Error("one is marked selected and should not be")
	}
	if byName["one"].Auth != "user/password" {
		t.Errorf("one auth = %q, want user/password", byName["one"].Auth)
	}
	if byName["two"].Auth != "none" {
		t.Errorf("two auth = %q, want none", byName["two"].Auth)
	}
}

// authLabel claims which credential the connection would use, so its order has
// to match natscontext.NATSOptions. A context carrying more than one no longer
// passes Validate, but files written before that check exist.
func TestAuthLabelFollowsConnectPrecedence(t *testing.T) {
	tests := []struct {
		name string
		opts []natscontext.Option
		want string
	}{
		{"user", []natscontext.Option{natscontext.WithUser("bob")}, "user/password"},
		{"creds", []natscontext.Option{natscontext.WithCreds("/tmp/x.creds")}, "creds file"},
		{"nkey", []natscontext.Option{natscontext.WithNKey("/tmp/x.nk")}, "nkey"},
		{"jwt", []natscontext.Option{natscontext.WithUserJWT("ey...")}, "user JWT"},
		{"token", []natscontext.Option{natscontext.WithToken("t0k")}, "token"},
		{"none", nil, "none"},

		// User wins over creds because NATSOptions checks it first. If that
		// ever changes, this label starts lying and so does the picker.
		{
			"user beats creds",
			[]natscontext.Option{natscontext.WithUser("bob"), natscontext.WithCreds("/tmp/x.creds")},
			"user/password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := natscontext.New(tt.name, false, tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got := authLabel(c); got != tt.want {
				t.Errorf("authLabel = %q, want %q", got, tt.want)
			}
		})
	}
}
