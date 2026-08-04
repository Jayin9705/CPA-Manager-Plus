package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCreatesDefaultConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(configEnvKey, configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "0.0.0.0:18317" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if !cfg.DashboardHourlyRollupEnabled {
		t.Fatal("DashboardHourlyRollupEnabled = false by default")
	}
	if cfg.AuthPostgresDSN != DefaultAuthPostgresDSN {
		t.Fatalf("AuthPostgresDSN = %q, want default", cfg.AuthPostgresDSN)
	}
	if cfg.UsageImportChunkBytes != DefaultUsageImportChunkBytes ||
		cfg.UsageImportDiskQuotaBytes != DefaultUsageImportDiskQuotaBytes ||
		cfg.UsageImportMaxSessions != DefaultUsageImportMaxSessions ||
		cfg.UsageImportSessionTTL != DefaultUsageImportSessionTTL {
		t.Fatalf("usage import defaults = %#v", cfg)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), `"dataDir": "./data"`) {
		t.Fatalf("generated config does not contain relative dataDir: %s", data)
	}
}

func TestLoadWithoutCreatingDefaultDoesNotCreateConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv(configEnvKey, configPath)

	cfg, err := LoadWithoutCreatingDefault()
	if err != nil {
		t.Fatalf("LoadWithoutCreatingDefault() error = %v", err)
	}
	if want := filepath.Join(dir, "data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config file exists or stat failed: %v", err)
	}
}

func TestLoadReadsConfigAndResolvesRelativePaths(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	secretPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret-value\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{
  "httpAddr": "127.0.0.1:19000",
  "dataDir": "state",
  "cpaUpstreamUrl": "http://cpa.local:8317",
  "managementKeyFile": "secret.txt",
  "collectorMode": "http",
  "queue": "custom-usage",
  "popSide": "left",
  "batchSize": 7,
	  "pollIntervalMs": 250,
	  "queryLimit": 900,
	  "pprofAddr": "127.0.0.1:6060",
	  "panelPath": "panel.html",
  "corsOrigins": ["http://panel.local"],
  "tlsSkipVerify": true,
  "quotaCooldownEnabled": true,
	  "accountActionsEnabled": true,
	  "accountActionsAutoDisable": true,
	  "usageImportChunkBytes": 1048576,
	  "usageImportDiskQuotaBytes": 1073741824,
	  "usageImportMaxSessions": 3,
	  "usageImportSessionTTLMinutes": 120
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19000" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "state", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.CPAUpstreamURL != "http://cpa.local:8317" {
		t.Fatalf("CPAUpstreamURL = %q", cfg.CPAUpstreamURL)
	}
	if cfg.ManagementKey != "secret-value" {
		t.Fatalf("ManagementKey = %q", cfg.ManagementKey)
	}
	if cfg.CollectorMode != "http" || cfg.Queue != "custom-usage" || cfg.PopSide != "left" {
		t.Fatalf("collector config = %#v", cfg)
	}
	if cfg.BatchSize != 7 || cfg.PollInterval != 250*time.Millisecond || cfg.QueryLimit != 900 {
		t.Fatalf("numeric config = %#v", cfg)
	}
	if cfg.PprofAddr != "127.0.0.1:6060" {
		t.Fatalf("PprofAddr = %q", cfg.PprofAddr)
	}
	if want := filepath.Join(dir, "panel.html"); cfg.PanelPath != want {
		t.Fatalf("PanelPath = %q, want %q", cfg.PanelPath, want)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://panel.local" {
		t.Fatalf("CORSOrigins = %#v", cfg.CORSOrigins)
	}
	if !cfg.TLSSkipVerify {
		t.Fatal("TLSSkipVerify = false")
	}
	if !cfg.QuotaCooldownEnabled {
		t.Fatal("QuotaCooldownEnabled = false")
	}
	if !cfg.AccountActionsEnabled {
		t.Fatal("AccountActionsEnabled = false")
	}
	if !cfg.AccountActionsAutoDisable {
		t.Fatal("AccountActionsAutoDisable = false")
	}
	if cfg.UsageImportChunkBytes != 1048576 || cfg.UsageImportDiskQuotaBytes != 1073741824 ||
		cfg.UsageImportMaxSessions != 3 || cfg.UsageImportSessionTTL != 2*time.Hour {
		t.Fatalf("usage import config = %#v", cfg)
	}
}

func TestLoadEnvOverridesConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "httpAddr": "127.0.0.1:19000",
  "dataDir": "state",
  "managementKeyFile": "secret.txt",
  "batchSize": 7
}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)
	t.Setenv("HTTP_ADDR", "127.0.0.1:19001")
	t.Setenv("USAGE_DATA_DIR", filepath.Join(dir, "env-data"))
	t.Setenv("CPA_MANAGEMENT_KEY", "env-secret")
	t.Setenv("USAGE_BATCH_SIZE", "12")
	t.Setenv("CPA_MANAGER_PPROF_ADDR", "[::1]:6061")
	t.Setenv("USAGE_DASHBOARD_HOURLY_ROLLUP_ENABLED", "false")
	t.Setenv("USAGE_IMPORT_CHUNK_BYTES", "2097152")
	t.Setenv("USAGE_IMPORT_DISK_QUOTA_BYTES", "2147483648")
	t.Setenv("USAGE_IMPORT_MAX_SESSIONS", "4")
	t.Setenv("USAGE_IMPORT_SESSION_TTL_MINUTES", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19001" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if want := filepath.Join(dir, "env-data", "usage.sqlite"); cfg.DBPath != want {
		t.Fatalf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if cfg.ManagementKey != "env-secret" {
		t.Fatalf("ManagementKey = %q", cfg.ManagementKey)
	}
	if cfg.BatchSize != 12 {
		t.Fatalf("BatchSize = %d", cfg.BatchSize)
	}
	if cfg.PprofAddr != "[::1]:6061" {
		t.Fatalf("PprofAddr = %q", cfg.PprofAddr)
	}
	if cfg.DashboardHourlyRollupEnabled {
		t.Fatal("DashboardHourlyRollupEnabled = true, want false")
	}
	if cfg.UsageImportChunkBytes != 2097152 || cfg.UsageImportDiskQuotaBytes != 2147483648 ||
		cfg.UsageImportMaxSessions != 4 || cfg.UsageImportSessionTTL != 30*time.Minute {
		t.Fatalf("usage import env config = %#v", cfg)
	}
}

func TestNormalizeCollectorMode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "auto"},
		{"AUTO", "auto"},
		{"http", "http"},
		{"HTTP", "http"},
		{"resp", "resp"},
		{"subscribe", "subscribe"},
		{" Subscribe ", "subscribe"},
		{"unknown", "auto"},
	}
	for _, tc := range cases {
		if got := normalizeCollectorMode(tc.input); got != tc.want {
			t.Errorf("normalizeCollectorMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLoadSeparatedConsoleSecurityConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataDir":"state"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)
	t.Setenv("CPAMP_CONSOLE_ORIGINS", "https://console.example.com, http://localhost:18080")
	t.Setenv("CPAMP_PUBLIC_API_ORIGIN", "https://api.example.com/")
	t.Setenv("CPAMP_PUBLIC_CONSOLE_ORIGIN", "https://console.example.com/")
	t.Setenv("CPAMP_COOKIE_SECURE", "true")
	t.Setenv("CPAMP_API_SHADOW_MODE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.ConsoleOrigins) != 2 || cfg.ConsoleOrigins[0] != "https://console.example.com" {
		t.Fatalf("ConsoleOrigins = %#v", cfg.ConsoleOrigins)
	}
	if !cfg.CSRFEnabled || !cfg.CookieSecure {
		t.Fatalf("security flags = csrf:%v secure:%v", cfg.CSRFEnabled, cfg.CookieSecure)
	}
	if !cfg.APIShadowMode {
		t.Fatal("APIShadowMode = false")
	}
	if cfg.PublicAPIOrigin != "https://api.example.com" || cfg.PublicConsoleOrigin != "https://console.example.com" {
		t.Fatalf("public origins = %q %q", cfg.PublicAPIOrigin, cfg.PublicConsoleOrigin)
	}
}

func TestLoadRejectsWildcardConsoleOrigin(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataDir":"state"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)
	t.Setenv("CPAMP_CONSOLE_ORIGINS", "*")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted wildcard CPAMP_CONSOLE_ORIGINS")
	}
}

func TestLoadReadsPostgresAndRedisSecretsFromFiles(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"dataDir":"state"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(configEnvKey, configPath)

	secretFiles := map[string]string{
		"CPAMP_CORE_PG_DSN_FILE":          "postgres://core-from-file",
		"CPAMP_USAGE_PG_DSN_FILE":         "postgres://usage-from-file",
		"CPAMP_AUTH_PG_DSN_FILE":          "postgres://auth-from-file",
		"CPAMP_USAGE_REDIS_PASSWORD_FILE": "usage-redis-from-file",
		"CPAMP_AUTH_REDIS_PASSWORD_FILE":  "auth-redis-from-file",
	}
	for envName, value := range secretFiles {
		path := filepath.Join(dir, strings.ToLower(envName))
		if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", envName, err)
		}
		t.Setenv(envName, path)
	}
	t.Setenv("CPAMP_PERSISTENCE_BACKEND", "postgres")
	t.Setenv("CPAMP_USAGE_SINK", "redis")
	t.Setenv("CPAMP_USAGE_SQLITE_HOURLY_ROLLUP_ENABLED", "false")
	t.Setenv("CPAMP_USAGE_SQLITE_ACCOUNT_ROLLUP_ENABLED", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CorePostgresDSN != "postgres://core-from-file" ||
		cfg.UsagePostgresDSN != "postgres://usage-from-file" ||
		cfg.AuthPostgresDSN != "postgres://auth-from-file" {
		t.Fatalf("PostgreSQL file secrets were not loaded")
	}
	if cfg.UsageRedisPassword != "usage-redis-from-file" || cfg.AuthRedisPassword != "auth-redis-from-file" {
		t.Fatalf("Redis file secrets were not loaded")
	}
}

func TestLoadAuthRedisPasswordFallsBackToUsageSecretFile(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	passwordPath := filepath.Join(dir, "redis-password")
	if err := os.WriteFile(configPath, []byte(`{"dataDir":"state"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(passwordPath, []byte("shared-redis-password\n"), 0o600); err != nil {
		t.Fatalf("write password: %v", err)
	}
	t.Setenv(configEnvKey, configPath)
	t.Setenv("CPAMP_USAGE_REDIS_PASSWORD_FILE", passwordPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UsageRedisPassword != "shared-redis-password" || cfg.AuthRedisPassword != cfg.UsageRedisPassword {
		t.Fatal("authentication Redis password did not inherit the usage secret")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		configEnvKey,
		"HTTP_ADDR",
		"USAGE_DATA_DIR",
		"USAGE_DB_PATH",
		"CPA_UPSTREAM_URL",
		"CPA_MANAGEMENT_KEY",
		"CPA_MANAGEMENT_KEY_FILE",
		"CPA_MANAGER_ADMIN_KEY",
		"CPA_MANAGER_ADMIN_KEY_FILE",
		"CPA_MANAGER_DATA_KEY",
		"CPA_MANAGER_DATA_KEY_FILE",
		"CPAMP_PERSISTENCE_BACKEND",
		"CPAMP_CORE_PG_DSN",
		"CPAMP_CORE_PG_DSN_FILE",
		"CPAMP_USAGE_PG_DSN",
		"CPAMP_USAGE_PG_DSN_FILE",
		"CPAMP_AUTH_PG_DSN",
		"CPAMP_AUTH_PG_DSN_FILE",
		"CPAMP_USAGE_SINK",
		"CPAMP_USAGE_REDIS_PASSWORD",
		"CPAMP_USAGE_REDIS_PASSWORD_FILE",
		"CPAMP_AUTH_REDIS_PASSWORD",
		"CPAMP_AUTH_REDIS_PASSWORD_FILE",
		"CPAMP_USAGE_SQLITE_HOURLY_ROLLUP_ENABLED",
		"CPAMP_USAGE_SQLITE_ACCOUNT_ROLLUP_ENABLED",
		"USAGE_COLLECTOR_MODE",
		"USAGE_RESP_QUEUE",
		"USAGE_RESP_POP_SIDE",
		"USAGE_BATCH_SIZE",
		"USAGE_POLL_INTERVAL_MS",
		"USAGE_QUERY_LIMIT",
		"CPA_MANAGER_PPROF_ADDR",
		"USAGE_CORS_ORIGINS",
		"CPAMP_CONSOLE_ORIGINS",
		"CPAMP_PUBLIC_API_ORIGIN",
		"CPAMP_PUBLIC_CONSOLE_ORIGIN",
		"CPAMP_CSRF_ENABLED",
		"CPAMP_COOKIE_SECURE",
		"CPAMP_API_SHADOW_MODE",
		"USAGE_RESP_TLS_SKIP_VERIFY",
		"USAGE_QUOTA_COOLDOWN_ENABLED",
		"USAGE_ACCOUNT_ACTIONS_ENABLED",
		"USAGE_ACCOUNT_ACTIONS_AUTO_DISABLE",
		"USAGE_DASHBOARD_HOURLY_ROLLUP_ENABLED",
		"USAGE_IMPORT_CHUNK_BYTES",
		"USAGE_IMPORT_DISK_QUOTA_BYTES",
		"USAGE_IMPORT_MAX_SESSIONS",
		"USAGE_IMPORT_SESSION_TTL_MINUTES",
		"PANEL_PATH",
	} {
		t.Setenv(key, "")
	}
}
