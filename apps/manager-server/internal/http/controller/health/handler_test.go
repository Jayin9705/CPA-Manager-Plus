package health

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
)

func TestHealthReturnsLiveness(t *testing.T) {
	handler := &Handler{ServiceID: "cpa-manager-plus"}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	handler.Health(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"ok":true`) ||
		!strings.Contains(body, `"service":"cpa-manager-plus"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestReadyAllowsSQLiteDevelopmentMode(t *testing.T) {
	handler := &Handler{
		ServiceID: "cpa-manager-plus",
		App:       &app.Context{Config: config.Config{PersistenceBackend: "sqlite"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	handler.Ready(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"ready":true`) {
		t.Fatalf("body = %s", body)
	}
}

func TestReadyFailsClosedForUnavailablePostgresAuth(t *testing.T) {
	handler := &Handler{
		ServiceID: "cpa-manager-plus",
		App:       &app.Context{Config: config.Config{PersistenceBackend: "postgres"}},
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	handler.Ready(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if body := rr.Body.String(); !strings.Contains(body, `"ready":false`) {
		t.Fatalf("body = %s", body)
	}
}

func TestHealthRejectsNonGetRequests(t *testing.T) {
	handler := &Handler{ServiceID: "cpa-manager-plus"}
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rr := httptest.NewRecorder()

	handler.Health(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}
