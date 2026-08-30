package webui

import "embed"

// Files contains the production frontend build when present and a development fallback.
//
//go:embed *
var Files embed.FS
