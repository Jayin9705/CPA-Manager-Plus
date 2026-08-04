//go:build api_only

package httpapi

import "io/fs"

// Keep the app context shape identical in API-only builds while avoiding a
// go:embed dependency on frontend resources.
var embeddedPanel fs.FS
