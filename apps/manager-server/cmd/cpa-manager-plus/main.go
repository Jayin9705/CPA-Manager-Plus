package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/adminreset"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/migration"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	bootstrapservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrap"
	collectorservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/worker"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-admin-key", "reset-admin-password":
			if err := adminreset.Run(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("reset admin key: %v", err)
				os.Exit(1)
			}
			return
		}
	}
	runServer()
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	dataKey, dataKeyCreated, err := security.LoadOrCreateDataKey(cfg.DataKey, cfg.DataKeyPath)
	if err != nil {
		log.Fatalf("load data key: %v", err)
	}
	protector, err := security.NewProtector(dataKey)
	if err != nil {
		log.Fatalf("initialize secret protector: %v", err)
	}
	postgresOnly := cfg.PersistenceBackend == "postgres"
	var db *store.Store
	if postgresOnly {
		coreCtx, cancelCore := context.WithTimeout(context.Background(), 5*time.Second)
		db, err = store.OpenPostgres(coreCtx, cfg.CorePostgresDSN, protector)
		cancelCore()
		if err != nil {
			log.Fatalf("open PostgreSQL core store: %v", err)
		}
		log.Printf("PostgreSQL-only core store enabled")
	} else {
		db, err = store.Open(cfg.DBPath, protector)
		if err != nil {
			log.Fatalf("open sqlite: %v", err)
		}
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close core store: %v", err)
		}
	}()

	bootstrapResult, err := bootstrapservice.Run(context.Background(), cfg, db, dataKeyCreated)
	if err != nil {
		log.Fatalf("bootstrap manager server: %v", err)
	}
	if bootstrapResult.GeneratedAdminKey != "" {
		if postgresOnly {
			log.Printf("legacy CPA Manager Plus admin credential initialized in PostgreSQL")
		} else {
			log.Printf("CPA Manager Plus admin key generated: %s", bootstrapResult.GeneratedAdminKey)
		}
	} else {
		log.Printf("CPA Manager Plus admin credential initialized")
	}
	if bootstrapResult.DataKeyCreated {
		log.Printf("CPA Manager Plus data key created at %s", cfg.DataKeyPath)
	}
	if bootstrapResult.MigratedLegacy {
		log.Printf("CPA Manager Plus legacy data migrated")
	}

	manager := collector.NewManager(cfg, db)
	collectorService := collectorservice.New(manager)
	collectorWorker := worker.NewCollectorWorker(cfg, db, collectorService)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var usagePostgres *migration.Postgres
	if strings.TrimSpace(cfg.UsagePostgresDSN) != "" {
		pgCtx, cancelPG := context.WithTimeout(context.Background(), 3*time.Second)
		usagePostgres, err = migration.OpenPostgres(pgCtx, cfg.UsagePostgresDSN)
		cancelPG()
		if err != nil {
			if postgresOnly {
				log.Fatalf("open required PostgreSQL usage store: %v", err)
			}
			log.Printf("open PostgreSQL usage reader: %v; using SQLite development fallback", err)
			usagePostgres = nil
		} else {
			defer usagePostgres.Close()
			log.Printf("PostgreSQL usage store enabled")
		}
	}
	if postgresOnly && usagePostgres == nil {
		log.Fatalf("PostgreSQL usage store is required in PostgreSQL-only mode")
	}
	if postgresOnly {
		redisCtx, cancelRedis := context.WithTimeout(context.Background(), 3*time.Second)
		redisClient, redisErr := migration.OpenRedis(redisCtx, migration.RedisOptions{
			Address: cfg.UsageRedisAddr, Password: cfg.UsageRedisPassword, DB: cfg.UsageRedisDB,
		})
		if redisErr == nil {
			redisErr = migration.EnsureUsageStream(redisCtx, redisClient, cfg.UsageRedisStream, cfg.UsageRedisGroup)
			_ = redisClient.Close()
		}
		cancelRedis()
		if redisErr != nil {
			log.Fatalf("verify required Redis usage stream: %v", redisErr)
		}
	}

	var serverApp *httpapi.Server
	if httpapi.IsAPIOnly() {
		serverApp = httpapi.NewAPI(cfg, db, manager)
	} else {
		serverApp = httpapi.New(cfg, db, manager)
	}
	defer func() {
		if err := serverApp.AppContext().AdminAuthService.Close(); err != nil {
			log.Printf("close administrator authentication service: %v", err)
		}
	}()
	if postgresOnly {
		if err := serverApp.AppContext().AdminAuthService.Ready(); err != nil {
			log.Fatalf("verify required administrator authentication backends: %v", err)
		}
	}
	if usagePostgres != nil {
		serverApp.AppContext().MonitoringService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().DashboardService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().UsageService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().ModelPriceService.SetUsageSummarySource(usagePostgres)
		manager.SetUsagePostgresFallback(usagePostgres)
	}
	if !cfg.APIShadowMode {
		recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), 10*time.Second)
		if err := serverApp.AppContext().CodexInspectionService.Recover(recoveryCtx); err != nil {
			log.Printf("recover codex inspection runs: %v", err)
		}
		cancelRecovery()
	} else {
		log.Printf("API shadow mode enabled: process-local workers are disabled")
	}
	if err := serverApp.AppContext().UsageService.StartImportSessionCleanup(ctx); err != nil {
		log.Fatalf("start usage import session cleanup: %v", err)
	}
	automationSettingsService := serverApp.AppContext().AccountProcessingPolicyService
	runtimeSettings := automationSettingsService.RuntimeSettings(ctx)
	rateLimitAutoDisableWorker := worker.NewRateLimitAutoDisableWorkerWithMutationCoordinator(
		db,
		serverApp.AppContext().AuthFileMutationCoordinator,
		collector.RuntimeConfig{
			CPAUpstreamURL: cfg.CPAUpstreamURL,
			ManagementKey:  cfg.ManagementKey,
		},
	)
	accountActionWorker := worker.NewAccountActionCandidateWorkerWithMutationCoordinator(
		db,
		serverApp.AppContext().AuthFileMutationCoordinator,
		runtimeSettings.AccountActionsAutoDisable,
	)
	var accountHistoryRollupWorker *worker.AccountHistoryRollupWorker
	var usagePricingRollupWorker *worker.UsagePricingRollupWorker
	if !postgresOnly && cfg.SQLiteAccountRollupEnabled {
		accountHistoryRollupWorker = worker.NewAccountHistoryRollupWorker(db)
		accountHistoryRollupWorker.Start(ctx)
		usagePricingRollupWorker = worker.NewUsagePricingRollupWorker(db)
		usagePricingRollupWorker.Start(ctx)
		serverApp.AppContext().ModelPriceService.SetPricesChangedNotifier(usagePricingRollupWorker.Wake)
	} else {
		log.Printf("PostgreSQL raw-event monitoring enabled; local account-history and pricing rollups disabled")
	}
	var usageHourlyAggregateWorker *worker.UsageHourlyAggregateWorker
	if !postgresOnly && cfg.DashboardHourlyRollupEnabled && cfg.SQLiteHourlyRollupEnabled {
		usageHourlyAggregateWorker = worker.NewUsageHourlyAggregateWorker(db)
		usageHourlyAggregateWorker.Start(ctx)
	} else if !postgresOnly && cfg.DashboardHourlyRollupEnabled {
		log.Printf("PostgreSQL aggregate monitoring enabled; local hourly rollup disabled")
	} else if postgresOnly {
		log.Printf("PostgreSQL-only usage pipeline enabled")
	}
	serverApp.AppContext().UsageService.SetEventsInsertedNotifier(func() {
		if accountHistoryRollupWorker != nil {
			accountHistoryRollupWorker.Wake()
		}
		if usagePricingRollupWorker != nil {
			usagePricingRollupWorker.Wake()
		}
		if usageHourlyAggregateWorker != nil {
			usageHourlyAggregateWorker.Wake()
		}
	})
	automationRuntime := worker.NewAutomationRuntime(
		automationSettingsService,
		manager,
		rateLimitAutoDisableWorker,
		accountActionWorker,
	)
	serverApp.AppContext().AutomationRuntimeService = automationRuntime
	if !cfg.APIShadowMode {
		automationRuntime.Start(ctx)
	} else {
		log.Printf("API shadow mode: automation runtime worker start skipped")
	}
	manager.SetUsageEventHandler(worker.NewUsageEventFanout(
		automationRuntime.UsageEventHandler(),
		accountHistoryRollupWorker,
		usagePricingRollupWorker,
		usageHourlyAggregateWorker,
	))

	if !cfg.APIShadowMode {
		collectorWorker.Start(ctx)
	} else {
		log.Printf("API shadow mode: collector worker start skipped")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           serverApp.Handler(),
		ReadTimeout:       2 * time.Minute,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	pprofServer, err := newPprofServer(cfg.PprofAddr)
	if err != nil {
		log.Fatalf("configure pprof: %v", err)
	}
	if pprofServer != nil {
		go func() {
			log.Printf("cpa-manager-plus pprof listening on %s", pprofServer.Addr)
			if err := pprofServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("pprof server: %v", err)
			}
		}()
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		log.Fatalf("http listen: %v", err)
	}
	codexInspectionWorker := worker.NewCodexInspectionWorker(serverApp.AppContext().Store, serverApp.AppContext().CodexInspectionService)
	if !cfg.APIShadowMode {
		codexInspectionWorker.Start(ctx)
	}
	serverResult := make(chan error, 1)
	go func() {
		log.Printf("cpa-manager-plus listening on %s", listener.Addr())
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverResult <- err
	}()

	if !postgresOnly && !cfg.APIShadowMode {
		usageCacheAccountingMigrationWorker := worker.NewUsageCacheAccountingMigrationWorker(db, func() {
			go runUsageResponseMetadataBackfill(ctx, db)
			if accountHistoryRollupWorker != nil {
				accountHistoryRollupWorker.Wake()
			}
			if usagePricingRollupWorker != nil {
				usagePricingRollupWorker.Wake()
			}
			if usageHourlyAggregateWorker != nil {
				usageHourlyAggregateWorker.Wake()
			}
		})
		usageCacheAccountingMigrationWorker.Start(ctx)
	}

	select {
	case <-ctx.Done():
	case err := <-serverResult:
		if err != nil {
			log.Printf("http server stopped unexpectedly: %v", err)
		}
		// Ensure every worker using the shared process context observes the same
		// shutdown even when the HTTP listener, rather than a signal, initiated it.
		stop()
	}
	stopCodexInspectionWorker(codexInspectionWorker, 20*time.Second)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	collectorWorker.Stop(context.Background())
	if pprofServer != nil {
		if err := pprofServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown pprof: %v", err)
		}
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

type codexInspectionStopper interface {
	StopAndWait(context.Context) error
}

func stopCodexInspectionWorker(stopper codexInspectionStopper, timeout time.Duration) {
	if stopper == nil {
		return
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), timeout)
	err := stopper.StopAndWait(shutdownCtx)
	cancelShutdown()
	if err == nil {
		return
	}
	// Shutdown must remain bounded. The worker has already fenced new starts and
	// cancelled owned work; if it still cannot finish within the grace period,
	// leave an explicit error for the supervisor and let startup recovery reclaim
	// the expired lease instead of hanging the process indefinitely.
	log.Printf("shutdown codex inspection worker: %v; continuing bounded process shutdown", err)
}

func newPprofServer(addr string) (*http.Server, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("pprof address must use a loopback host: %q", addr)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}, nil
}

func runUsageResponseMetadataBackfill(ctx context.Context, db *store.Store) {
	const batchLimit = 1000
	total := 0
	for {
		updated, err := db.BackfillUsageResponseMetadata(ctx, batchLimit)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("usage response metadata backfill: %v", err)
			}
			return
		}
		if updated == 0 {
			if total > 0 {
				log.Printf("usage response metadata backfill completed: updated=%d", total)
			}
			return
		}
		total += updated
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
