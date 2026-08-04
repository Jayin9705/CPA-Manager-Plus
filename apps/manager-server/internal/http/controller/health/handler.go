package health

import (
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
)

type Handler struct {
	ServiceID string
	App       *app.Context
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"ok": true, "service": h.ServiceID})
}

// Ready reports whether the process has passed the dependency checks required
// to serve authenticated console traffic. Liveness remains intentionally
// independent so supervisors can distinguish "process is alive" from
// "process is ready for traffic".
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}

	// SQLite/development mode has no mandatory external authentication
	// dependency. PostgreSQL production mode validates the auth repositories at
	// startup and must not advertise readiness when either backend is absent.
	if h.App != nil && h.App.Config.PersistenceBackend == "postgres" {
		if h.App.AdminAuthService == nil || h.App.AdminAuthService.Ready() != nil {
			response.JSON(w, http.StatusServiceUnavailable, map[string]any{
				"ok":      false,
				"ready":   false,
				"service": h.ServiceID,
			})
			return
		}
	}

	response.JSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"ready":   true,
		"service": h.ServiceID,
	})
}
