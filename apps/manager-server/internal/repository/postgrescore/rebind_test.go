package postgrescore

import "testing"

func TestRewritePostgresSQLRebindsOnlyParameters(t *testing.T) {
	input := `select ?, '?', "?", $tag$?$tag$, value -- ?
		from sample /* ? /* nested ? */ still comment */ where id = $4 and other = ?`
	want := `select $5, '?', "?", $tag$?$tag$, value -- ?
		from sample /* ? /* nested ? */ still comment */ where id = $4 and other = $6`
	if got := rewritePostgresSQL(input); got != want {
		t.Fatalf("unexpected PostgreSQL rewrite\nwant: %s\n got: %s", want, got)
	}
}

func TestRewritePostgresSQLUsesPostgresDatabaseClock(t *testing.T) {
	input := `update leases set heartbeat_at_ms = cast(unixepoch('subsec') * 1000 as integer), expires_at_ms = cast(unixepoch('subsec') * 1000 as integer) + ?`
	want := `update leases set heartbeat_at_ms = cast(extract(epoch from clock_timestamp()) * 1000 as bigint), expires_at_ms = cast(extract(epoch from clock_timestamp()) * 1000 as bigint) + $1`
	if got := rewritePostgresSQL(input); got != want {
		t.Fatalf("unexpected PostgreSQL clock rewrite\nwant: %s\n got: %s", want, got)
	}
}

func TestValidateSchemaName(t *testing.T) {
	for _, schema := range []string{"cpamp_core", "_cpamp_test_123", "a"} {
		if err := validateSchemaName(schema); err != nil {
			t.Fatalf("valid schema %q rejected: %v", schema, err)
		}
	}
	for _, schema := range []string{"", "1core", "core-test", "core.public", "core test"} {
		if err := validateSchemaName(schema); err == nil {
			t.Fatalf("invalid schema %q accepted", schema)
		}
	}
}
