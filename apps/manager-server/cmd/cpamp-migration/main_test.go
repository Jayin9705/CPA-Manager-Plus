package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommonFlagHelpDoesNotExposeEnvironmentSecrets(t *testing.T) {
	const dsnSecret = "postgres://secret-in-help"
	const redisSecret = "redis-secret-in-help"
	t.Setenv("CPAMP_USAGE_PG_DSN", dsnSecret)
	t.Setenv("CPAMP_USAGE_REDIS_PASSWORD", redisSecret)

	flags, _ := newCommonFlags("test", false)
	var output bytes.Buffer
	flags.SetOutput(&output)
	flags.PrintDefaults()
	if strings.Contains(output.String(), dsnSecret) || strings.Contains(output.String(), redisSecret) {
		t.Fatal("flag help exposed a configured secret")
	}
}

func TestValidateCommonLoadsSecretFilesAfterFlagParsing(t *testing.T) {
	dir := t.TempDir()
	dsnPath := filepath.Join(dir, "pg-dsn")
	redisPath := filepath.Join(dir, "redis-password")
	if err := os.WriteFile(dsnPath, []byte("postgres://from-file\n"), 0o600); err != nil {
		t.Fatalf("write DSN: %v", err)
	}
	if err := os.WriteFile(redisPath, []byte("redis-from-file\n"), 0o600); err != nil {
		t.Fatalf("write Redis password: %v", err)
	}
	t.Setenv("CPAMP_USAGE_PG_DSN", "")
	t.Setenv("CPAMP_USAGE_REDIS_PASSWORD", "")
	t.Setenv("CPAMP_USAGE_PG_DSN_FILE", dsnPath)
	t.Setenv("CPAMP_USAGE_REDIS_PASSWORD_FILE", redisPath)

	flags, common := newCommonFlags("test", false)
	if err := validateCommon(flags, common, false); err != nil {
		t.Fatalf("validate common flags: %v", err)
	}
	if common.pgDSN != "postgres://from-file" || common.redisPassword != "redis-from-file" {
		t.Fatal("secret files were not loaded after flag parsing")
	}
}
