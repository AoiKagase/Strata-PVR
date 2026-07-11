package web

import "embed"

// FS contains the Web UI assets shipped with the strata-pvr binary.
//
// Keep this file in the web directory so the assets can be embedded without
// duplicating them under internal/wui. The directory remains usable as a
// normal external asset directory during local development.
//
//go:embed *.html *.css *.js
var FS embed.FS
