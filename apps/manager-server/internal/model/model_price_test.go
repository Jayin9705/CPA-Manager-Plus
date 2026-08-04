package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelPriceJSONOmitsRawSyncRecord(t *testing.T) {
	payload, err := json.Marshal(ModelPrice{
		Prompt:     1.25,
		Completion: 2.5,
		RawJSON:    `{"provider":"example","large":"internal-only"}`,
	})
	if err != nil {
		t.Fatalf("marshal model price: %v", err)
	}

	body := string(payload)
	if strings.Contains(body, "rawJson") || strings.Contains(body, "internal-only") {
		t.Fatalf("raw synchronization record leaked into JSON: %s", body)
	}
	if !strings.Contains(body, `"prompt":1.25`) || !strings.Contains(body, `"completion":2.5`) {
		t.Fatalf("public price fields missing from JSON: %s", body)
	}
}
