package meta

import (
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
)

type Handler struct {
	App *app.Context
}

func (h *Handler) Capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"apiVersion":               "v1",
		"service":                  h.App.ServiceID,
		"serverVersion":            buildinfo.Version,
		"serverCommit":             buildinfo.Commit,
		"serverBuildDate":          buildinfo.BuildDate,
		"csrfRequired":             h.App.Config.CSRFEnabled,
		"consoleOriginsConfigured": len(h.App.Config.ConsoleOrigins) > 0,
		"compatibility": map[string]any{
			"legacyPanel":    legacyPanelEnabled(),
			"legacyApiPaths": true,
		},
	})
}
