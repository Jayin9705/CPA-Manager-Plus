package router

import (
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	accountactioncontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/accountaction"
	apikeyaliascontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/apikeyalias"
	authcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/auth"
	automationcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/automation"
	codexinspectioncontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/codexinspection"
	dashboardcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/dashboard"
	healthcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/health"
	managerconfigcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/managerconfig"
	metacontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/meta"
	modelpricecontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/modelprice"
	monitoringcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/monitoring"
	operationscontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/operations"
	panelcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/panel"
	proxycontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/proxy"
	quotacooldowncontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/quotacooldown"
	quotasnapshotcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/quotasnapshot"
	setupcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/setup"
	systemcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/system"
	updatecheckcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/updatecheck"
	usagecontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
)

func New(appCtx *app.Context) http.Handler {
	return newRouter(appCtx, true)
}

// NewAPI builds the JSON/proxy API without registering the legacy embedded
// panel routes. The regular New router remains unchanged for rollback and the
// lightweight CPA-hosted panel.
func NewAPI(appCtx *app.Context) http.Handler {
	return newRouter(appCtx, false)
}

func newRouter(appCtx *app.Context, includePanel bool) http.Handler {
	healthHandler := &healthcontroller.Handler{ServiceID: appCtx.ServiceID, App: appCtx}
	systemHandler := &systemcontroller.Handler{App: appCtx}
	setupHandler := &setupcontroller.Handler{App: appCtx}
	managerConfigHandler := &managerconfigcontroller.Handler{App: appCtx}
	usageHandler := &usagecontroller.Handler{App: appCtx}
	modelPriceHandler := &modelpricecontroller.Handler{App: appCtx}
	apiKeyAliasHandler := &apikeyaliascontroller.Handler{App: appCtx}
	accountActionHandler := &accountactioncontroller.Handler{App: appCtx}
	authHandler := &authcontroller.Handler{App: appCtx}
	automationHandler := automationcontroller.New(appCtx)
	quotaCooldownHandler := &quotacooldowncontroller.Handler{App: appCtx}
	codexInspectionHandler := &codexinspectioncontroller.Handler{App: appCtx}
	dashboardHandler := &dashboardcontroller.Handler{App: appCtx}
	monitoringHandler := &monitoringcontroller.Handler{App: appCtx}
	quotaSnapshotHandler := &quotasnapshotcontroller.Handler{App: appCtx}
	operationsHandler := operationscontroller.New(appCtx)
	proxyHandler := &proxycontroller.Handler{App: appCtx}
	panelHandler := &panelcontroller.Handler{App: appCtx}
	metaHandler := &metacontroller.Handler{App: appCtx}

	mux := http.NewServeMux()
	updates := &updatecheckcontroller.Handler{App: appCtx}
	mux.HandleFunc("/usage-service/updates", middleware.WithCORS(appCtx.Config, updates.Handle))
	mux.HandleFunc("/usage-service/updates/", middleware.WithCORS(appCtx.Config, updates.Handle))
	mux.HandleFunc("/health", middleware.WithCORS(appCtx.Config, healthHandler.Health))
	mux.HandleFunc("/healthz", middleware.WithCORS(appCtx.Config, healthHandler.Health))
	mux.HandleFunc("/readyz", middleware.WithCORS(appCtx.Config, healthHandler.Ready))
	mux.HandleFunc("/api/v1/meta/capabilities", middleware.WithCORS(appCtx.Config, metaHandler.Capabilities))
	mux.HandleFunc("/api/v1/auth/login", middleware.WithCORS(appCtx.Config, authHandler.Login))
	mux.HandleFunc("/api/v1/auth/logout", middleware.WithCORS(appCtx.Config, authHandler.Logout))
	mux.HandleFunc("/api/v1/auth/session", middleware.WithCORS(appCtx.Config, authHandler.Session))
	mux.HandleFunc("/api/v1/auth/csrf", middleware.WithCORS(appCtx.Config, authHandler.Session))
	mux.HandleFunc("/api/v1/status", middleware.WithCORS(appCtx.Config, systemHandler.Status))
	mux.HandleFunc("/api/v1/setup", middleware.WithCORS(appCtx.Config, setupHandler.Setup))
	mux.HandleFunc("/api/v1/config", middleware.WithCORS(appCtx.Config, managerConfigHandler.Handle))
	mux.HandleFunc("/api/v1/account-processing-policy", middleware.WithCORS(appCtx.Config, automationHandler.Handle))
	mux.HandleFunc("/api/v1/quota-cooldowns", middleware.WithCORS(appCtx.Config, quotaCooldownHandler.Handle))
	mux.HandleFunc("/api/v1/operations/metrics", middleware.WithCORS(appCtx.Config, operationsHandler.Handle))
	mux.HandleFunc("/status", middleware.WithCORS(appCtx.Config, systemHandler.Status))
	mux.HandleFunc("/usage-service/info", middleware.WithCORS(appCtx.Config, systemHandler.Info))
	mux.HandleFunc("/usage-service/auth/login", middleware.WithCORS(appCtx.Config, authHandler.Login))
	mux.HandleFunc("/usage-service/auth/logout", middleware.WithCORS(appCtx.Config, authHandler.Logout))
	mux.HandleFunc("/usage-service/auth/session", middleware.WithCORS(appCtx.Config, authHandler.Session))
	mux.HandleFunc("/usage-service/config", middleware.WithCORS(appCtx.Config, managerConfigHandler.Handle))
	mux.HandleFunc("/usage-service/account-processing-policy", middleware.WithCORS(appCtx.Config, automationHandler.Handle))
	mux.HandleFunc("/usage-service/quota-cooldowns", middleware.WithCORS(appCtx.Config, quotaCooldownHandler.Handle))
	mux.HandleFunc("/usage-service/quota-cooldowns/", middleware.WithCORS(appCtx.Config, quotaCooldownHandler.Handle))
	mux.HandleFunc("/setup", middleware.WithCORS(appCtx.Config, setupHandler.Setup))
	mux.HandleFunc("/favicon.ico", panelHandler.Favicon)
	mux.HandleFunc("/apple-touch-icon.png", panelHandler.AppleTouchIcon)
	mux.HandleFunc("/", rootHandler(appCtx, usageHandler, modelPriceHandler, apiKeyAliasHandler, accountActionHandler, codexInspectionHandler, dashboardHandler, monitoringHandler, quotaSnapshotHandler, managerConfigHandler, proxyHandler, includePanel))
	if includePanel {
		mux.HandleFunc("/management.html", panelHandler.ManagementHTML)
		mux.HandleFunc("/management-assets/", panelHandler.ManagementAssets)
	}

	return serverMetadata(middleware.Security(appCtx.Config, middleware.Recovery(middleware.RequestLogger(mux))))
}

func serverMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server-Version", buildinfo.Version)
		w.Header().Set("X-Server-Commit", buildinfo.Commit)
		w.Header().Set("X-Server-Build-Date", buildinfo.BuildDate)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func rootHandler(
	appCtx *app.Context,
	usageHandler *usagecontroller.Handler,
	modelPriceHandler *modelpricecontroller.Handler,
	apiKeyAliasHandler *apikeyaliascontroller.Handler,
	accountActionHandler *accountactioncontroller.Handler,
	codexInspectionHandler *codexinspectioncontroller.Handler,
	dashboardHandler *dashboardcontroller.Handler,
	monitoringHandler *monitoringcontroller.Handler,
	quotaSnapshotHandler *quotasnapshotcontroller.Handler,
	managerConfigHandler *managerconfigcontroller.Handler,
	proxyHandler *proxycontroller.Handler,
	includePanel bool,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			middleware.WriteCORS(appCtx.Config, w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Canonical v1 resource names are translated to the existing handlers
		// during the compatibility window. This changes neither business logic
		// nor Redis/PostgreSQL facts and lets the separated console migrate one
		// feature at a time.
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/model-prices"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/model-prices", "/v0/management/model-prices", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/api-key-aliases"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/api-key-aliases", "/v0/management/api-key-aliases", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/account-actions"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/account-actions", "/v0/management/account-action-candidates", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/inspections"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/inspections", "/v0/management/codex-inspection", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/dashboard"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/dashboard", "/v0/management/dashboard", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/monitoring"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/monitoring", "/v0/management/monitoring", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/usage"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/usage", "/v0/management/usage", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/playground"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/playground", "/v0/management/playground", 1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/plugins"):
			r.URL.Path = strings.Replace(r.URL.Path, "/api/v1/plugins", "/v0/management/plugins", 1)
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/model-prices") {
			middleware.WithCORS(appCtx.Config, modelPriceHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/api-key-aliases") {
			middleware.WithCORS(appCtx.Config, apiKeyAliasHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/account-action-candidates") {
			middleware.WithCORS(appCtx.Config, accountActionHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/codex-inspection") {
			middleware.WithCORS(appCtx.Config, codexInspectionHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/dashboard/") {
			middleware.WithCORS(appCtx.Config, dashboardHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/monitoring/") {
			middleware.WithCORS(appCtx.Config, monitoringHandler.Handle)(w, r)
			return
		}
		cleanQuotaSnapshotPath := strings.TrimRight(r.URL.Path, "/")
		if cleanQuotaSnapshotPath == "/v0/management/quota-snapshots" ||
			cleanQuotaSnapshotPath == "/v0/management/quota-snapshots/query" {
			middleware.WithCORS(appCtx.Config, quotaSnapshotHandler.Handle)(w, r)
			return
		}
		cleanUsagePath := strings.TrimRight(r.URL.Path, "/")
		if cleanUsagePath == "/v0/management/usage" || strings.HasPrefix(cleanUsagePath, "/v0/management/usage/") {
			middleware.WithCORS(appCtx.Config, usageHandler.Handle)(w, r)
			return
		}
		if r.URL.Path == "/v0/management/cpa-connection/validate" {
			middleware.WithCORS(appCtx.Config, managerConfigHandler.ValidateCPAConnection)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/playground/") {
			middleware.WithCORS(appCtx.Config, proxyHandler.Playground)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/") {
			middleware.WithCORS(appCtx.Config, proxyHandler.Management)(w, r)
			return
		}
		if r.URL.Path == "/v1/models" || r.URL.Path == "/v1/models/" ||
			r.URL.Path == "/models" || r.URL.Path == "/models/" {
			middleware.WithCORS(appCtx.Config, proxyHandler.ModelList)(w, r)
			return
		}
		if proxysvc.IsCPAPluginResourcePath(r.URL.Path) {
			middleware.WithCORS(appCtx.Config, proxyHandler.CPAResource)(w, r)
			return
		}
		if proxysvc.IsCPAV8APIKeysConfigPath(r.URL.Path) {
			middleware.WithCORS(appCtx.Config, proxyHandler.V8APIKeysConfig)(w, r)
			return
		}
		if includePanel && r.URL.Path == "/" {
			http.Redirect(w, r, "/management.html", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	}
}
