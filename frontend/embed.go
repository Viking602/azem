package frontend

import "embed"

// Assets is rebuilt by `make gui-build` before the desktop binary is compiled.
//
//go:embed all:dist
var Assets embed.FS
