package frontend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "record the current sources as the ones dist was built from")

// stampFile records which sources dist was built from. It lives inside dist
// so that a rebuild, which empties dist first, takes the old stamp with it.
const stampFile = "dist/.source-sha256"

// TestDistIsCurrent fails when the committed dist was built from different
// sources than the ones beside it. dist is what `go install` ships, so a
// frontend change committed without rebuilding it would put an old UI in
// front of a new backend, with nothing anywhere to say so.
//
// `make ui` runs this with -update after every build to write the stamp.
func TestDistIsCurrent(t *testing.T) {
	got, err := sourceHash(os.DirFS("."))
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		if err := os.WriteFile(stampFile, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(stampFile)
	if err != nil {
		t.Fatalf("frontend/dist has no record of what it was built from (%v); run `make ui`", err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Fatal("frontend/dist was built from different sources than these; run `make ui` and commit frontend/dist")
	}
}

// sourceHash digests everything vite builds dist from: every file under
// frontend/ except the output, the dependencies, Go files and dotfiles. New
// source directories are picked up without anyone remembering to list them.
//
// package-lock.json is left out on purpose. npm rewrites it for reasons that
// change nothing in the output, such as a different npm version, and a guard
// that fires on that gets ignored.
//
// Line endings are normalised first. Git checks text files out with CRLF on
// Windows and LF elsewhere, and the same sources must hash the same on both.
func sourceHash(fsys fs.FS) (string, error) {
	var list bytes.Buffer
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != "." && (name == "dist" || name == "node_modules" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".go") || name == "package-lock.json" {
			return nil
		}

		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))
		// The same shape as sha256sum's output, so a mismatch can be
		// chased down by hand.
		fmt.Fprintf(&list, "%x  %s\n", sum, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(list.Bytes())
	return hex.EncodeToString(sum[:]), nil
}
