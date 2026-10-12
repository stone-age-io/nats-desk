// Package frontend carries the built single-page app into the binary.
//
// The embed lives here rather than in cmd/ or internal/ because //go:embed
// cannot reach into a parent directory - the directive has to sit beside the
// files it takes.
//
// dist is committed, already built. `go install ...@latest` compiles the
// module as published, which means the committed files and nothing else, and
// it cannot run npm. While dist was ignored, that embedded an empty directory,
// and the app window showed a file listing instead of the app. `make ui`
// rebuilds dist, and TestDistIsCurrent fails when the sources have changed
// without it. The `all:` prefix keeps names that begin with `.` or `_`, which
// the default pattern drops and a bundler is free to emit.
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app rooted at dist, so handlers see "index.html"
// rather than "dist/index.html".
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
