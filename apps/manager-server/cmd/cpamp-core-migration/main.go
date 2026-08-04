package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/coremigration"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sqlitePath := flags.String("sqlite", os.Getenv("USAGE_DB_PATH"), "legacy SQLite core source")
	postgresDSN := flags.String("pg-dsn", os.Getenv("CPAMP_CORE_PG_DSN"), "PostgreSQL core DSN")
	replace := flags.Bool("replace", false, "replace the not-yet-live PostgreSQL core tables")
	if err := flags.Parse(os.Args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fail(err)
	}
	var report coremigration.Report
	var err error
	switch os.Args[1] {
	case "sync":
		report, err = coremigration.Sync(ctx, *sqlitePath, *postgresDSN, *replace)
	case "verify":
		report, err = coremigration.Verify(ctx, *sqlitePath, *postgresDSN)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cpamp-core-migration <sync|verify> --sqlite PATH --pg-dsn DSN [--replace]")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "core migration failed:", err)
	os.Exit(1)
}
