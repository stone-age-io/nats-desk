// Package frontend carries the built single-page app into the binary.
//
// The embed lives here rather than in cmd/ or internal/ because //go:embed
// cannot reach into a parent directory - the directive has to sit beside the
// files it takes. dist/.gitkeep is committed so a fresh clone builds before
// anyone has run `npm run build`; the `all:` prefix is what makes a dot-file
// count, and without it the pattern matches nothing and the build fails.
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
