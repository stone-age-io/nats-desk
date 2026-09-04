//go:build !windows

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Everywhere except Windows the entry is a file, and the three operations are
// the same three regardless of what the file contains. Only the path and the
// text differ, so those are all the per-platform files have to supply.

func readFile(where func() (string, error)) (string, error) {
	p, err := where()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeFile(where func() (string, error), content string) error {
	p, err := where()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func removeFile(where func() (string, error)) error {
	p, err := where()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
