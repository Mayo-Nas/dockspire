// Package web bundles the browser interface into the application executable.
package web

import "embed"

// Assets contains runtime browser files only; Go source is never served.
//
//go:embed *.html *.css *.js favicon.ico brand/*
var Assets embed.FS
