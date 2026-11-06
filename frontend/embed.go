// Package frontend holds the built web interface. Only cmd/mailarkiv imports it,
// so tests under internal/ need no frontend build.
package frontend

import "embed"

//go:embed all:build
var Build embed.FS
