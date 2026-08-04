//go:build !api_only

package httpapi

import "embed"

// embeddedPanel is compiled only into the rollback-compatible manager binary.
// The api_only build tag excludes both HTML and static assets from the API
// binary, so the separated backend has no frontend payload at all.
//
//go:embed web/management.html web/management-assets
var embeddedPanel embed.FS
