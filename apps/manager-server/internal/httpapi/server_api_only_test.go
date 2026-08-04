//go:build api_only

package httpapi

import (
	"net/http"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/collector"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func newAPIOnlyTestHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := testutil.NewConfig(t)
	db := testutil.NewStore(t, cfg)
	manager := collector.NewManager(cfg, db)
	server := NewAPI(cfg, db, manager)
	installTestAdminAuth(t, server)
	return server.Handler()
}

func TestAPIOnlyServerDoesNotServeLegacyPanel(t *testing.T) {
	if !IsAPIOnly() {
		t.Fatal("api_only test compiled without api_only build mode")
	}
	handler := newAPIOnlyTestHandler(t)

	for _, path := range []string{"/management.html", "/management-assets/assets/app.js", "/"} {
		rr := testutil.Request(t, handler, http.MethodGet, path, "", "")
		testutil.RequireStatus(t, rr, http.StatusNotFound)
		if location := rr.Header().Get("Location"); location != "" {
			t.Fatalf("%s returned redirect to %q", path, location)
		}
	}

	capabilitiesRR := testutil.Request(t, handler, http.MethodGet, "/api/v1/meta/capabilities", "", "")
	testutil.RequireStatus(t, capabilitiesRR, http.StatusOK)
	var capabilities struct {
		Compatibility struct {
			LegacyPanel bool `json:"legacyPanel"`
		} `json:"compatibility"`
	}
	testutil.DecodeJSON(t, capabilitiesRR, &capabilities)
	if capabilities.Compatibility.LegacyPanel {
		t.Fatalf("api-only capabilities = %#v, legacyPanel must be false", capabilities.Compatibility)
	}
}
