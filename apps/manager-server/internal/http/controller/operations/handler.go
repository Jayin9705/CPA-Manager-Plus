package operations

import (
	"errors"
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	servermetricssvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/servermetrics"
)

type SnapshotProvider interface {
	Snapshot() (servermetricssvc.Snapshot, error)
}

type Handler struct {
	App       *app.Context
	Collector SnapshotProvider
}

func New(appCtx *app.Context) *Handler {
	return &Handler{
		App:       appCtx,
		Collector: servermetricssvc.New(appCtx.StartedAt),
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}
	if h.App == nil {
		response.Error(w, http.StatusServiceUnavailable, errors.New("application context unavailable"))
		return
	}
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}
	if h.Collector == nil {
		response.Error(w, http.StatusServiceUnavailable, errors.New("server metrics collector unavailable"))
		return
	}
	snapshot, err := h.Collector.Snapshot()
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.JSON(w, http.StatusOK, snapshot)
}
