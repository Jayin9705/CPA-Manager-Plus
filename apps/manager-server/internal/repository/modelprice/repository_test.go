package modelprice

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
)

func TestCompactLoadAndReplacePreserveRawJSON(t *testing.T) {
	ctx := context.Background()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "model-prices.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repository := New(db)
	const rawJSON = `{"provider":"example","metadata":{"large":"server-only"}}`
	if _, err := repository.UpsertSynced(ctx, map[string]model.ModelPrice{
		"gpt-test": {
			Prompt:           1,
			Completion:       2,
			Source:           "sync-test",
			SourceModelID:    "upstream/gpt-test",
			RawJSON:          rawJSON,
			PromptConfigured: true,
		},
	}); err != nil {
		t.Fatalf("seed synchronized price: %v", err)
	}

	prices, err := repository.LoadAll(ctx)
	if err != nil {
		t.Fatalf("load compact prices: %v", err)
	}
	price, ok := prices["gpt-test"]
	if !ok {
		t.Fatalf("missing synchronized price: %#v", prices)
	}
	if price.RawJSON != "" {
		t.Fatalf("compact load returned raw JSON: %q", price.RawJSON)
	}

	price.Prompt = 3.5
	prices["gpt-test"] = price
	if err := repository.ReplaceAll(ctx, prices); err != nil {
		t.Fatalf("replace prices: %v", err)
	}

	var storedRawJSON string
	if err := db.QueryRowContext(ctx, `select raw_json from model_prices where model = ?`, "gpt-test").Scan(&storedRawJSON); err != nil {
		t.Fatalf("read preserved raw JSON: %v", err)
	}
	if storedRawJSON != rawJSON {
		t.Fatalf("stored raw JSON = %q, want %q", storedRawJSON, rawJSON)
	}

	prices, err = repository.LoadAll(ctx)
	if err != nil {
		t.Fatalf("reload compact prices: %v", err)
	}
	if prices["gpt-test"].Prompt != 3.5 || prices["gpt-test"].RawJSON != "" {
		t.Fatalf("reloaded price = %#v", prices["gpt-test"])
	}
}
