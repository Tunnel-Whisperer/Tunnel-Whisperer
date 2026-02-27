package dashboard

import "embed"

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS
