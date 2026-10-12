package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingFileIsDefaults(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), fileName))
	if err != nil {
		t.Fatalf("missing file should not be an error: %v", err)
	}
	if s.QuitOnClose {
		t.Fatal("QuitOnClose should default to off")
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), fileName)

	if err := Update(p, func(s *Settings) { s.QuitOnClose = true }); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(p); err != nil || !s.QuitOnClose {
		t.Fatalf("after turning on: %+v, %v", s, err)
	}

	if err := Update(p, func(s *Settings) { s.QuitOnClose = false }); err != nil {
		t.Fatal(err)
	}
	if s, err := Load(p); err != nil || s.QuitOnClose {
		t.Fatalf("after turning off: %+v, %v", s, err)
	}
}

// A broken file must not stop the app starting, and setting a value from the
// UI is how it gets repaired.
func TestCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), fileName)
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Load(p)
	if err == nil {
		t.Fatal("a corrupt file should be reported")
	}
	if s.QuitOnClose {
		t.Fatal("a corrupt file should read as the defaults")
	}

	if err := Update(p, func(s *Settings) { s.QuitOnClose = true }); err != nil {
		t.Fatalf("update should replace a corrupt file: %v", err)
	}
	if s, err := Load(p); err != nil || !s.QuitOnClose {
		t.Fatalf("after repair: %+v, %v", s, err)
	}
}

func TestNoConfigDir(t *testing.T) {
	if _, err := Load(""); err != nil {
		t.Fatalf("no path should read as the defaults: %v", err)
	}
	if err := Update("", func(*Settings) {}); err == nil {
		t.Fatal("update with nowhere to write should fail")
	}
}
