package dashboard

import "embed"

// LocalBundle is populated by the release builder with the existing Go server,
// built site, and its query contracts. Source builds select those paths explicitly.
//
//go:embed all:local-assets
var LocalBundle embed.FS
