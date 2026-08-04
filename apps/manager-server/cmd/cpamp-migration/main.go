package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/migration"
)

const defaultSQLitePath = "/var/lib/cpa-manager-plus/usage.sqlite"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(ctx, os.Args[2:])
	case "source-stats":
		err = runSourceStats(ctx, os.Args[2:])
	case "prepare":
		err = runPrepare(ctx, os.Args[2:])
	case "status":
		err = runStatus(ctx, os.Args[2:])
	case "pause":
		err = runPause(ctx, os.Args[2:], true)
	case "resume":
		err = runPause(ctx, os.Args[2:], false)
	case "backfill":
		err = runBackfill(ctx, os.Args[2:])
	case "verify":
		err = runVerify(ctx, os.Args[2:])
	case "init-stream":
		err = runInitStream(ctx, os.Args[2:])
	case "relay":
		err = runRelay(ctx, os.Args[2:])
	case "persist":
		err = runPersist(ctx, os.Args[2:])
	case "rollup":
		err = runRollup(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "migration %s failed: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

type commonFlags struct {
	sqlitePath    string
	pgDSN         string
	redisAddr     string
	redisPassword string
	redisDB       int
	stream        string
	group         string
	consumer      string
	jsonOutput    bool
}

func newCommonFlags(name string, needsSQLite bool) (*flag.FlagSet, *commonFlags) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	result := &commonFlags{}
	defaultSQLite := os.Getenv("CPAMP_MIGRATION_SQLITE_PATH")
	if defaultSQLite == "" {
		defaultSQLite = os.Getenv("USAGE_DB_PATH")
	}
	if defaultSQLite == "" {
		defaultSQLite = defaultSQLitePath
	}
	flags.StringVar(&result.sqlitePath, "sqlite", defaultSQLite, "source SQLite path")
	flags.StringVar(&result.pgDSN, "pg-dsn", "", "PostgreSQL DSN (prefer CPAMP_USAGE_PG_DSN_FILE)")
	redisDB := 0
	if value := os.Getenv("CPAMP_USAGE_REDIS_DB"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			redisDB = parsed
		}
	}
	flags.StringVar(&result.redisAddr, "redis-addr", envOrDefault("CPAMP_USAGE_REDIS_ADDR", migration.DefaultRedisAddress), "Redis address")
	flags.StringVar(&result.redisPassword, "redis-password", "", "Redis password (prefer CPAMP_USAGE_REDIS_PASSWORD_FILE)")
	flags.IntVar(&result.redisDB, "redis-db", redisDB, "Redis database number")
	flags.StringVar(&result.stream, "stream", envOrDefault("CPAMP_USAGE_REDIS_STREAM", migration.UsageStreamKey), "Redis stream key")
	flags.StringVar(&result.group, "group", envOrDefault("CPAMP_USAGE_REDIS_GROUP", migration.PersistConsumerGroup), "Redis consumer group")
	flags.StringVar(&result.consumer, "consumer", envOrDefault("CPAMP_USAGE_REDIS_CONSUMER", "cpamp-persist-v1"), "Redis consumer name")
	flags.BoolVar(&result.jsonOutput, "json", false, "print JSON")
	if needsSQLite {
		flags.Usage = func() {
			fmt.Fprintf(os.Stderr, "usage of %s: --sqlite PATH --pg-dsn DSN\n", name)
			flags.PrintDefaults()
		}
	}
	return flags, result
}

func validateCommon(flags *flag.FlagSet, common *commonFlags, needsSQLite bool) error {
	if common.pgDSN == "" {
		common.pgDSN = envOrFile("CPAMP_USAGE_PG_DSN", "CPAMP_USAGE_PG_DSN_FILE")
	}
	if common.redisPassword == "" {
		common.redisPassword = envOrFile("CPAMP_USAGE_REDIS_PASSWORD", "CPAMP_USAGE_REDIS_PASSWORD_FILE")
	}
	if common.pgDSN == "" {
		return errors.New("set CPAMP_USAGE_PG_DSN or pass --pg-dsn")
	}
	if needsSQLite && common.sqlitePath == "" {
		return errors.New("source SQLite path is required")
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envOrFile(name, fileName string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	path := strings.TrimSpace(os.Getenv(fileName))
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func runInit(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("init", false)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	if err := pg.Initialize(ctx); err != nil {
		return err
	}
	return printValue(map[string]string{"status": "ready"}, common.jsonOutput)
}

func openRedis(ctx context.Context, common *commonFlags) (*redis.Client, error) {
	return migration.OpenRedis(ctx, migration.RedisOptions{
		Address: common.redisAddr, Password: common.redisPassword, DB: common.redisDB,
	})
}

func runPrepare(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("prepare", true)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, true); err != nil {
		return err
	}
	source, err := migration.OpenSQLiteSource(common.sqlitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	state, err := pg.Prepare(ctx, source)
	if err != nil {
		return err
	}
	return printValue(state, common.jsonOutput)
}

func runSourceStats(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("source-stats", true)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if common.sqlitePath == "" {
		return errors.New("source SQLite path is required")
	}
	source, err := migration.OpenSQLiteSource(common.sqlitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	stats, err := source.Stats(ctx, 0)
	if err != nil {
		return err
	}
	return printValue(stats, common.jsonOutput)
}

func runStatus(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("status", false)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	states, err := pg.States(ctx)
	if err != nil {
		return err
	}
	return printValue(states, common.jsonOutput)
}

func runPause(ctx context.Context, args []string, pause bool) error {
	name := "resume"
	if pause {
		name = "pause"
	}
	flags, common := newCommonFlags(name, false)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	if err := pg.SetPause(ctx, pause); err != nil {
		return err
	}
	fmt.Printf("pause_requested=%t\n", pause)
	return nil
}

func runBackfill(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("backfill", true)
	batchSize := flags.Int("batch-size", 500, "rows per transaction")
	maxBatches := flags.Int("max-batches", 10, "maximum transactions for this invocation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, true); err != nil {
		return err
	}
	source, err := migration.OpenSQLiteSource(common.sqlitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	state, err := pg.Backfill(ctx, source, *batchSize, *maxBatches)
	if err != nil {
		return err
	}
	return printValue(state, common.jsonOutput)
}

func runVerify(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("verify", true)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, true); err != nil {
		return err
	}
	source, err := migration.OpenSQLiteSource(common.sqlitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	state, err := pg.Verify(ctx, source)
	if err != nil {
		return err
	}
	if state.Status == "FAILED" {
		return errors.New(state.LastError)
	}
	return printValue(state, common.jsonOutput)
}

func runInitStream(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("init-stream", false)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	redisClient, err := openRedis(ctx, common)
	if err != nil {
		return err
	}
	defer redisClient.Close()
	if err := migration.EnsureUsageStream(ctx, redisClient, common.stream, common.group); err != nil {
		return err
	}
	if err := pg.MarkRedisReady(ctx, common.stream, common.group); err != nil {
		return err
	}
	info, err := redisClient.XInfoStream(ctx, common.stream).Result()
	if err != nil {
		return err
	}
	return printValue(map[string]interface{}{
		"stream":         common.stream,
		"group":          common.group,
		"length":         info.Length,
		"first_entry_id": info.FirstEntry.ID,
		"last_entry_id":  info.LastEntry.ID,
	}, common.jsonOutput)
}

func runRelay(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("relay", true)
	batchSize := flags.Int("batch-size", 100, "SQLite rows per relay batch")
	maxBatches := flags.Int("max-batches", 1, "maximum relay batches; zero means unlimited with --follow")
	follow := flags.Bool("follow", false, "continue polling until interrupted")
	pollInterval := flags.Duration("poll-interval", 250*time.Millisecond, "SQLite polling interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, true); err != nil {
		return err
	}
	source, err := migration.OpenSQLiteSource(common.sqlitePath)
	if err != nil {
		return err
	}
	defer source.Close()
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	redisClient, err := openRedis(ctx, common)
	if err != nil {
		return err
	}
	defer redisClient.Close()
	state, err := pg.Relay(ctx, source, migration.RelayOptions{
		Stream: common.stream, Group: common.group, BatchSize: *batchSize,
		MaxBatches: *maxBatches, Follow: *follow, PollInterval: *pollInterval,
		Redis: redisClient,
	})
	if err != nil {
		return err
	}
	return printValue(state, common.jsonOutput)
}

func runPersist(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("persist", false)
	batchSize := flags.Int64("batch-size", 100, "Redis messages per PostgreSQL transaction")
	maxBatches := flags.Int("max-batches", 1, "maximum persist batches; zero means unlimited with --follow")
	follow := flags.Bool("follow", false, "continue consuming until interrupted")
	claimIdle := flags.Duration("claim-idle", 30*time.Second, "minimum idle time before XAUTOCLAIM")
	deleteAfterAck := flags.Bool("delete-after-ack", false, "delete exact Redis Stream IDs after PostgreSQL commit and XACK")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	redisClient, err := openRedis(ctx, common)
	if err != nil {
		return err
	}
	defer redisClient.Close()
	state, err := pg.Persist(ctx, migration.PersistOptions{
		Stream: common.stream, Group: common.group, Consumer: common.consumer,
		BatchSize: *batchSize, MaxBatches: *maxBatches, Follow: *follow,
		ClaimIdle: *claimIdle, DeleteAfterAck: *deleteAfterAck, Redis: redisClient,
	})
	if err != nil {
		return err
	}
	return printValue(state, common.jsonOutput)
}

func runRollup(ctx context.Context, args []string) error {
	flags, common := newCommonFlags("rollup", false)
	kind := flags.String("kind", "hourly", "rollup kind: hourly or daily")
	batchSize := flags.Int("batch-size", 500, "PostgreSQL event rows per rollup transaction")
	maxBatches := flags.Int("max-batches", 10, "maximum rollup batches; zero means unlimited with --follow")
	follow := flags.Bool("follow", false, "continue rolling up until interrupted")
	pollInterval := flags.Duration("poll-interval", time.Second, "rollup polling interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := validateCommon(flags, common, false); err != nil {
		return err
	}
	task := migration.HourlyRollupTaskName
	if *kind == "daily" {
		task = migration.DailyRollupTaskName
	} else if *kind != "hourly" {
		return errors.New("--kind must be hourly or daily")
	}
	pg, err := migration.OpenPostgres(ctx, common.pgDSN)
	if err != nil {
		return err
	}
	defer pg.Close()
	state, err := pg.Rollup(ctx, migration.RollupOptions{
		Task: task, BatchSize: *batchSize, MaxBatches: *maxBatches,
		Follow: *follow, PollInterval: *pollInterval,
	})
	if err != nil {
		return err
	}
	return printValue(state, common.jsonOutput)
}

func printValue(value any, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	switch value := value.(type) {
	case migration.MigrationState:
		fmt.Printf("task=%s phase=%s status=%s source_hwm=%d last_copied=%d last_stream=%s last_verified=%d processed=%d inserted=%d skipped=%d mismatch=%d pause=%t updated=%s\n",
			value.MigrationName, value.Phase, value.Status, value.SourceHWMID, value.LastCopiedID,
			value.LastStreamID, value.LastVerifiedID, value.ProcessedRows, value.InsertedRows, value.SkippedRows,
			value.MismatchRows, value.PauseRequested, value.UpdatedAt.Format(time.RFC3339))
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	}
	return nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cpamp-migration <init|source-stats|prepare|status|pause|resume|backfill|verify|init-stream|relay|persist|rollup> [flags]")
	fmt.Fprintln(os.Stderr, "  PostgreSQL DSN: CPAMP_USAGE_PG_DSN_FILE, CPAMP_USAGE_PG_DSN or --pg-dsn")
	fmt.Fprintln(os.Stderr, "  SQLite path:   CPAMP_MIGRATION_SQLITE_PATH, USAGE_DB_PATH or --sqlite")
	fmt.Fprintln(os.Stderr, "  batch flags:    --batch-size N --max-batches N")
	fmt.Fprintln(os.Stderr, "  Redis secret:   CPAMP_USAGE_REDIS_PASSWORD_FILE (preferred) or CPAMP_USAGE_REDIS_PASSWORD")
	fmt.Fprintln(os.Stderr, "  Redis flags:    --redis-addr ADDR --stream KEY --group NAME --consumer NAME")
}
