package postgrescore

import (
	"strings"
	"testing"
)

func TestCoreSchemaMigrationContainsEveryDurableRepositoryTable(t *testing.T) {
	var schema strings.Builder
	for _, migration := range coreSchemaMigrations {
		for _, statement := range migration.statements {
			schema.WriteString(statement)
			schema.WriteByte('\n')
		}
	}
	for _, table := range []string{
		"settings",
		"model_prices",
		"model_price_context_tiers",
		"model_price_service_tiers",
		"api_key_aliases",
		"account_action_candidates",
		"quota_cooldowns",
		"codex_inspection_runs",
		"codex_inspection_leases",
		"codex_inspection_results",
		"codex_inspection_logs",
		"codex_inspection_disable_ownership",
		"dead_letter_events",
	} {
		if !strings.Contains(schema.String(), "create table if not exists "+table+" ") {
			t.Errorf("core schema is missing table %s", table)
		}
	}
	if strings.Contains(schema.String(), "jsonb") {
		t.Fatal("phase-one core schema must preserve JSON payloads as text")
	}
}

func TestCoreSchemaAddsPricingAndOwnershipUpgradeInVersionTwo(t *testing.T) {
	if len(coreSchemaMigrations) < 2 {
		t.Fatal("core schema is missing version 2")
	}

	versionOne := strings.Join(coreSchemaMigrations[0].statements, "\n")
	versionTwo := strings.Join(coreSchemaMigrations[1].statements, "\n")
	for _, table := range []string{"model_price_context_tiers", "model_price_service_tiers"} {
		if strings.Contains(versionOne, "create table if not exists "+table+" ") {
			t.Fatalf("%s must be introduced by an upgrade migration, not by rewriting version 1", table)
		}
		if !strings.Contains(versionTwo, "create table if not exists "+table+" ") {
			t.Errorf("version 2 is missing table %s", table)
		}
	}

	for _, fragment := range []string{
		"add column if not exists account_snapshot text",
		"partition by file_name, provider, auth_index, account_id, account_snapshot",
		"add primary key (file_name, provider, auth_index, account_id, account_snapshot)",
	} {
		if !strings.Contains(versionTwo, fragment) {
			t.Errorf("version 2 is missing ownership upgrade fragment %q", fragment)
		}
	}
}

func TestCoreSchemaVersionsAreContiguous(t *testing.T) {
	for index, migration := range coreSchemaMigrations {
		want := index + 1
		if migration.version != want {
			t.Fatalf("migration index %d has version %d, want %d", index, migration.version, want)
		}
		if len(migration.statements) == 0 {
			t.Fatalf("migration version %d has no statements", migration.version)
		}
	}
}
