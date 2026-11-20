package contexts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jsm.go/natscontext"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// testCreds returns a well-formed user .creds. It is signed by a throwaway
// account, which is all a parse needs.
func testCreds(t *testing.T) string {
	t.Helper()
	akp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	upub, _ := ukp.PublicKey()
	ujwt, err := jwt.NewUserClaims(upub).Encode(akp)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := ukp.Seed()
	out, err := jwt.FormatUserConfig(ujwt, seed)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func cfg(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCreateWritesCredsBesideTheContexts(t *testing.T) {
	isolate(t)
	creds := testCreds(t)

	path, err := Create("prof", cfg(t, map[string]any{"url": "nats://one:4222"}), creds)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "nats", "creds", "prof.creds")
	if path != want {
		t.Fatalf("creds written to %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != creds {
		t.Error("the creds file does not hold the text it was given")
	}
	if stored := loadBack(t, "prof")["creds"]; stored != path {
		t.Errorf("context creds = %v, want %q", stored, path)
	}
}

func TestCreateWithoutCredsWritesNoFile(t *testing.T) {
	isolate(t)

	path, err := Create("plain", cfg(t, map[string]any{"url": "nats://one:4222", "token": "s3cret"}), "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != "" {
		t.Errorf("creds path = %q, want none", path)
	}
	if got := loadBack(t, "plain")["token"]; got != "s3cret" {
		t.Errorf("token = %v, want s3cret", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "nats", "creds")); !os.IsNotExist(err) {
		t.Error("a creds folder was made for a context with no creds")
	}
}

// Create is for new contexts only. Replacing one is what the editor's PUT is
// for, after the user has seen the file.
func TestCreateRefusesAnExistingContext(t *testing.T) {
	isolate(t)
	save(t, "taken", map[string]any{"url": "nats://keep:4222"})

	_, err := Create("taken", cfg(t, map[string]any{"url": "nats://other:4222"}), testCreds(t))
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if got := loadBack(t, "taken")["url"]; got != "nats://keep:4222" {
		t.Errorf("the existing context was changed: url = %v", got)
	}
	if p, _ := CredsPath("taken"); fileExists(p) {
		t.Error("a creds file was written for a context that was refused")
	}
}

// A creds file left behind - by a context deleted since, say - is not ours to
// overwrite.
func TestCreateRefusesAnExistingCredsFile(t *testing.T) {
	isolate(t)
	p, err := CredsPath("x")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Create("x", cfg(t, map[string]any{"url": "nats://one:4222"}), testCreds(t))
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "someone else's" {
		t.Error("the existing creds file was overwritten")
	}
	if natscontext.IsKnown("x") {
		t.Error("the context was created anyway")
	}
}

// natscontext refuses two credential types. The creds file was already
// written by then, and must not be left behind.
func TestCreateRemovesCredsWhenTheContextIsRefused(t *testing.T) {
	isolate(t)

	_, err := Create("both", cfg(t, map[string]any{"url": "nats://one:4222", "user": "u"}), testCreds(t))
	if err == nil || !strings.Contains(err.Error(), "too many types of credentials") {
		t.Fatalf("got %v, want natscontext's credential refusal", err)
	}
	if p, _ := CredsPath("both"); fileExists(p) {
		t.Error("the creds file was left behind")
	}
	if natscontext.IsKnown("both") {
		t.Error("the context was created anyway")
	}
}

func TestCreateRejectsTextThatIsNotCreds(t *testing.T) {
	isolate(t)

	_, err := Create("bad", cfg(t, map[string]any{"url": "nats://one:4222"}), "not a creds file")
	if err == nil || !strings.Contains(err.Error(), "invalid .creds") {
		t.Fatalf("got %v, want an invalid .creds error", err)
	}
	if p, _ := CredsPath("bad"); fileExists(p) {
		t.Error("an unusable creds file was written")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
