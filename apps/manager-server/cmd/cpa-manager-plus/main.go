package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/adminreset"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/cpaconnection"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/derivedmaintenance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/managerdatasnapshot"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/runtimeconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/command/usagecompact"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/migration"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/processlock"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	bootstrapservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/bootstrap"
	collectorservice "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/worker"
)

func main() {
	if handled, err := writeVersion(os.Args[1:], os.Stdout); handled {
		if err != nil {
			log.Printf("write version: %v", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reset-admin-key", "reset-admin-password":
			if err := adminreset.Run(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("reset admin key: %v", err)
				os.Exit(1)
			}
			return
		case "cleanup-derived":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := derivedmaintenance.Run(ctx, os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("cleanup derived data: %v", err)
				os.Exit(1)
			}
			return
		case "compact-usage":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := usagecompact.Run(ctx, os.Args[2:], os.Stdout, os.Stderr); err != nil {
				if usagecompact.IsHelp(err) {
					return
				}
				log.Printf("compact usage database: %v", err)
				os.Exit(1)
			}
			return
		case "store-cpa-connection":
			if err := cpaconnection.Run(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("store CPA connection: %v", err)
				os.Exit(1)
			}
			return
		case "manager-data-snapshot":
			if err := runManagerDataSnapshotCommand(os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("manage Manager data snapshot: %v", err)
				os.Exit(1)
			}
			return
		case "sanitize-runtime-config":
			if err := runtimeconfig.Run(os.Args[2:], os.Stdout, os.Stderr); err != nil {
				log.Printf("sanitize runtime config: %v", err)
				os.Exit(1)
			}
			return
		}
	}
	runServer()
}

func writeVersion(args []string, stdout io.Writer) (bool, error) {
	if len(args) != 1 || (args[0] != "-v" && args[0] != "--version") {
		return false, nil
	}
	_, err := fmt.Fprintln(stdout, buildinfo.Version)
	return true, err
}

func runManagerDataSnapshotCommand(args []string, stdout io.Writer, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return managerdatasnapshot.Run(ctx, args, stdout, stderr)
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	databaseLock, err := processlock.Acquire(cfg.DBPath)
	if err != nil {
		log.Fatalf("acquire manager database process lock: %v", err)
	}
	defer func() {
		if err := databaseLock.Close(); err != nil {
			log.Printf("close manager database process lock: %v", err)
		}
	}()
	cfg.DBPath = databaseLock.DatabasePath()
	if err := sqliterepo.RequireExistingDataKeyForEncryptedCPAConnection(
		context.Background(),
		cfg.DBPath,
		cfg.DataKey,
		cfg.DataKeyPath,
	); err != nil {
		log.Fatalf("validate data key availability: %v", err)
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
	}
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close sqlite: %v", err)
		}
	}()

	bootstrapResult, err := bootstrapservice.Run(context.Background(), cfg, db, dataKeyCreated)
	if err != nil {
		log.Fatalf("bootstrap manager server: %v", err)
	}
	if bootstrapResult.GeneratedAdminKey != "" {
		log.Printf("CPA Manager Plus admin key generated: %s", bootstrapResult.GeneratedAdminKey)
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
	walMaintenance, err := sqliterepo.NewWALMaintenance(cfg.DBPath)
	if err != nil {
		log.Printf("configure SQLite WAL maintenance: %v", err)
	} else {
		walMaintenance.Start(ctx)
		defer func() {
			if err := walMaintenance.Close(); err != nil {
				log.Printf("close SQLite WAL maintenance: %v", err)
			}
		}()
	}

	serverApp := httpapi.New(cfg, db, manager)
	// SQLite WAL 维护状态只对 SQLite 后端有意义；PG 模式下不挂载，
	// 否则 /status 会去查询 sqlite_master 而报 500。
	if !postgresOnly {
		serverApp.AppContext().DatabaseMaintenance = walMaintenance
	}

	// ---- separated 架构（PostgreSQL + Redis）专属接线：保留本地定制 ----
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
	if usagePostgres != nil {
		serverApp.AppContext().MonitoringService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().DashboardService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().UsageService.SetPostgresSource(usagePostgres)
		serverApp.AppContext().ModelPriceService.SetUsageSummarySource(usagePostgres)
		manager.SetUsagePostgresFallback(usagePostgres)
	}
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), 10*time.Second)
	if err := serverApp.AppContext().CodexInspectionService.Recover(recoveryCtx); err != nil {
		log.Printf("recover codex inspection runs: %v", err)
	}
	cancelRecovery()
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
	serverApp.AppContext().QuotaCooldownRecoveryService = rateLimitAutoDisableWorker
	accountActionWorker := worker.NewAccountActionCandidateWorkerWithMutationCoordinator(
		db,
		serverApp.AppContext().AuthFileMutationCoordinator,
		runtimeSettings.AccountActionsAutoDisable,
	)
	// separated 架构：usage 事件与汇总全部在 PostgreSQL/Redis。
	// 下面的 rollup / archive-retention worker 全部基于 SQLite store 的 DataMigrations /
	// UsageAggregates / UsageArchives 仓储，PG 模式下这些仓储为 nil，启动会空指针崩溃。
	// 因此 postgresOnly 时一律不创建、不启动（与原始 separated 部署保持一致）。
	var accountHistoryRollupWorker *worker.AccountHistoryRollupWorker
	var usageDerivedRollupWorker *worker.UsagePricingRollupWorker
	if !postgresOnly {
		accountHistoryRollupWorker = worker.NewAccountHistoryRollupWorker(db)
		usageDerivedRollupWorker = worker.NewUsagePricingRollupWorker(db)
		serverApp.AppContext().ModelPriceService.SetPricesChangedNotifier(usageDerivedRollupWorker.Wake)
	} else {
		log.Printf("PostgreSQL raw-event monitoring enabled; local account-history and pricing rollups disabled")
	}
	var usageHourlyAggregateWorker *worker.UsageHourlyAggregateWorker
	if !postgresOnly && cfg.DashboardHourlyRollupEnabled && cfg.SQLiteHourlyRollupEnabled {
		usageHourlyAggregateWorker = worker.NewUsageHourlyAggregateWorker(db)
	} else if postgresOnly {
		log.Printf("PostgreSQL-only usage pipeline enabled; local hourly rollup disabled")
	}
	var usageArchiveRetentionWorker *worker.UsageArchiveRetentionWorker
	if !postgresOnly && cfg.UsageArchiveRetentionEnabled && cfg.UsageArchiveRetentionDays > 0 && cfg.DashboardHourlyRollupEnabled {
		usageArchiveRetentionWorker = worker.NewUsageArchiveRetentionWorker(
			serverApp.AppContext().UsageService,
			cfg.UsageArchiveRetentionDays,
		)
	}
	serverApp.AppContext().UsageService.SetEventsInsertedNotifier(func() {
		if accountHistoryRollupWorker != nil {
			accountHistoryRollupWorker.Wake()
		}
		if usageDerivedRollupWorker != nil {
			usageDerivedRollupWorker.Wake()
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
	manager.SetUsageEventHandler(worker.NewUsageEventFanout(
		automationRuntime.UsageEventHandler(),
		accountHistoryRollupWorker,
		usageDerivedRollupWorker,
		usageHourlyAggregateWorker,
	))

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           serverApp.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
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
	log.Printf("cpa-manager-plus listening on %s", listener.Addr())
	codexInspectionWorker := worker.NewCodexInspectionWorker(serverApp.AppContext().Store, serverApp.AppContext().CodexInspectionService)
	serverResult := make(chan error, 1)
	go serveHTTPServer(server, listener, stop, serverResult)
	go serverApp.AppContext().UpdateCheckService.Run(ctx)
	if err := serverApp.AppContext().UsageService.StartImportSessionCleanup(ctx); err != nil {
		log.Printf("[startup] start usage import session cleanup: %v", err)
	}
	if err := serverApp.AppContext().UsageService.StartArchiveJobs(ctx); err != nil {
		log.Printf("[startup] start usage archive jobs: %v", err)
	}

	// PG 模式下 Store 的 SQLite 专有仓储（DataMigrations / UsageAggregates /
	// UsageArchives）为 nil，RunDerivedStartupMaintenance、StartDerivedMaintenance、
	// LegacyQuotaSnapshotMigration 都会因空指针崩溃，因此一律跳过。
	if !postgresOnly {
		if err := db.RunDerivedStartupMaintenance(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[startup] post-listen index preparation failed; continuing without blocking background workers: %v", err)
		}
	}
	if ctx.Err() == nil {
		log.Printf("[startup] starting background workers")
		automationRuntime.Start(ctx)
		codexInspectionWorker.Start(ctx)
		if accountHistoryRollupWorker != nil {
			accountHistoryRollupWorker.Start(ctx)
		}
		if usageDerivedRollupWorker != nil {
			usageDerivedRollupWorker.Start(ctx)
		}
		if usageHourlyAggregateWorker != nil {
			usageHourlyAggregateWorker.Start(ctx)
		}
		collectorWorker.Start(ctx)
		if !postgresOnly {
			db.StartDerivedMaintenance(ctx)
			worker.NewLegacyQuotaSnapshotMigrationWorker(db).Start(ctx)
		}
	}

	usageCacheAccountingMigrationWorker := worker.NewUsageCacheAccountingMigrationWorker(db, func() {
		if accountHistoryRollupWorker != nil {
			accountHistoryRollupWorker.Wake()
		}
		if usageDerivedRollupWorker != nil {
			usageDerivedRollupWorker.Wake()
		}
		if usageHourlyAggregateWorker != nil {
			usageHourlyAggregateWorker.Wake()
		}
		go func() {
			if err := waitForUsageResponseMetadataBackfill(ctx, db); err != nil {
				if ctx.Err() == nil {
					log.Printf("usage response metadata backfill: %v", err)
				}
				return
			}
			if usageArchiveRetentionWorker != nil {
				usageArchiveRetentionWorker.Start(ctx)
			}
		}()
	})
	if ctx.Err() == nil && db.DataMigrations != nil {
		usageCacheAccountingMigrationWorker.Start(ctx)
	}

	select {
	case <-ctx.Done():
		select {
		case err := <-serverResult:
			if err != nil {
				log.Printf("http server stopped unexpectedly: %v", err)
			}
		default:
		}
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
	if err := usageArchiveRetentionWorker.StopAndWait(shutdownCtx); err != nil {
		log.Printf("shutdown usage archive retention: %v", err)
	}
	if err := serverApp.AppContext().UsageService.WaitArchiveJobs(shutdownCtx); err != nil {
		log.Printf("shutdown usage archive jobs: %v", err)
	}
}

func serveHTTPServer(server *http.Server, listener net.Listener, stop context.CancelFunc, result chan<- error) {
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	result <- err
	stop()
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
	}, nil
}

const usageResponseMetadataBackfillRetryDelay = 5 * time.Second

func waitForUsageResponseMetadataBackfill(ctx context.Context, db *store.Store) error {
	return waitForBackfillReadiness(ctx, usageResponseMetadataBackfillRetryDelay, func(runCtx context.Context) error {
		return runUsageResponseMetadataBackfill(runCtx, db)
	})
}

func waitForBackfillReadiness(ctx context.Context, retryDelay time.Duration, run func(context.Context) error) error {
	if run == nil {
		return errors.New("usage response metadata backfill is not configured")
	}
	if retryDelay <= 0 {
		retryDelay = time.Second
	}
	for {
		err := run(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("usage response metadata backfill failed; will retry: %v", err)
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func runUsageResponseMetadataBackfill(ctx context.Context, db *store.Store) error {
	const batchLimit = 1000
	total := 0
	for {
		updated, err := db.BackfillUsageResponseMetadata(ctx, batchLimit)
		if err != nil {
			return err
		}
		if updated == 0 {
			if total > 0 {
				log.Printf("usage response metadata backfill completed: updated=%d", total)
			}
			return nil
		}
		total += updated
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
