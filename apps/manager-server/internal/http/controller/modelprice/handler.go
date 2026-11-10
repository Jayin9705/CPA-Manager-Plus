package modelprice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
)

type Handler struct {
	App *app.Context
}

const (
	modelPriceReadTimeout = 3 * time.Second
	modelPriceSyncTimeout = 45 * time.Second
)

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}

	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case path == "/v0/management/model-prices/runtime-models" && r.Method == http.MethodGet:
		status, err := h.App.ModelPriceService.RuntimeModelPricingStatus(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, status)
	case path == "/v0/management/model-prices/usage-summary" && r.Method == http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		summary, err := h.App.ModelPriceService.UsageSummary(ctx, h.App.Config.QueryLimit)
		if err != nil {
			response.Error(w, modelPriceReadErrorStatus(err), err)
			return
		}
		response.JSON(w, http.StatusOK, summary)
	case path == "/v0/management/model-prices" && r.Method == http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), modelPriceReadTimeout)
		defer cancel()
		prices, err := h.App.ModelPriceService.List(ctx)
		if err != nil {
			response.Error(w, modelPriceReadErrorStatus(err), err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})
	case path == "/v0/management/model-prices" && r.Method == http.MethodPut:
		var req modelpricesvc.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		prices, err := h.App.ModelPriceService.Replace(r.Context(), req.Prices)
		if err != nil {
			response.Error(w, modelPriceMutationStatus(err, http.StatusBadRequest), err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})
	case path == "/v0/management/model-prices/sync" && r.Method == http.MethodPost:
		var req modelpricesvc.SyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), modelPriceSyncTimeout)
		defer cancel()
		result, err := h.App.ModelPriceService.Sync(ctx, req)
		if err != nil {
			response.Error(w, modelPriceMutationStatus(err, response.ModelPriceErrorStatus(err)), err)
			return
		}
		response.JSON(w, http.StatusOK, result)
	default:
		response.MethodNotAllowed(w)
	}
}

func modelPriceMutationStatus(err error, fallback int) int {
	if errors.Is(err, modelpricesvc.ErrStructureChangeAfterRawDeletion) {
		return http.StatusConflict
	}
	return fallback
}

func modelPriceReadErrorStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}
