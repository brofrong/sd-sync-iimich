// Package frontend embeds the built Svelte UI and the JSON locale catalogs
// shared with the Go backend, so the compiled binary needs no files on disk.
package frontend

import "embed"

// Dist holds the built frontend (produced by `npm run build` in this
// directory). Until the frontend is built it contains only dist/.gitkeep,
// which keeps the embed non-empty; the "all:" prefix is required because
// .gitkeep would otherwise be excluded as a dotfile.
//
//go:embed all:dist
var Dist embed.FS

// Locales holds the JSON translation catalogs shared by the Go backend
// (package internal/i18n) and the Svelte frontend.
//
//go:embed locales/*.json
var Locales embed.FS
