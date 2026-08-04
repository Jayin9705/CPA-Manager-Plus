package httpapi

import (
	"net/http"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/router"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const serviceID = "cpa-manager-plus"

var modelsDevModelPriceSyncURL = "https://models.dev/catalog.json"
var modelPriceSyncURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
var openRouterModelPriceSyncURL = "https://openrouter.ai/api/v1/models"

type Server struct {
	handler http.Handler
	appCtx  *app.Context
}

func New(cfg config.Config, store *store.Store, collector *collector.Manager, automationRuntimeService ...app.AutomationRuntimeService) *Server {
	return newServer(cfg, store, collector, !IsAPIOnly(), automationRuntimeService...)
}

// NewAPI is the pure Go API constructor used by the separated backend image.
// It intentionally omits the embedded panel routes; the normal New constructor
// keeps rollback-compatible behavior while api_only builds remain panel-free.
func NewAPI(cfg config.Config, store *store.Store, collector *collector.Manager, automationRuntimeService ...app.AutomationRuntimeService) *Server {
	return newServer(cfg, store, collector, false, automationRuntimeService...)
}

func newServer(cfg config.Config, store *store.Store, collector *collector.Manager, includePanel bool, automationRuntimeService ...app.AutomationRuntimeService) *Server {
	startedAt := time.Now().UnixMilli()
	appCtx := app.FromExistingWithModelsDev(
		cfg,
		store,
		collector,
		startedAt,
		embeddedPanel,
		&modelsDevModelPriceSyncURL,
		&modelPriceSyncURL,
		&openRouterModelPriceSyncURL,
		serviceID,
		automationRuntimeService...,
	)
	handler := router.New(appCtx)
	if !includePanel {
		handler = router.NewAPI(appCtx)
	}
	return &Server{
		handler: handler,
		appCtx:  appCtx,
	}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) AppContext() *app.Context {
	return s.appCtx
}
