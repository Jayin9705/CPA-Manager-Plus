package operations

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	servermetricssvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/servermetrics"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

type fakeCollector struct {
	snapshot servermetricssvc.Snapshot
	err      error
}

func (c fakeCollector) Snapshot() (servermetricssvc.Snapshot, error) {
	return c.snapshot, c.err
}

func TestHandleRequiresPanelSession(t *testing.T) {
	handler := &Handler{
		App:       &app.Context{AdminAuthService: testutil.NewAdminAuth(t)},
		Collector: fakeCollector{},
	}
	recorder := httptest.NewRecorder()
	handler.Handle(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/operations/metrics", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleReturnsSnapshot(t *testing.T) {
	want := servermetricssvc.Snapshot{TimestampMS: 123, CPU: servermetricssvc.CPU{UsagePercent: 42}}
	handler := &Handler{
		App:       &app.Context{AdminAuthService: testutil.NewAdminAuth(t)},
		Collector: fakeCollector{snapshot: want},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	recorder := httptest.NewRecorder()
	handler.Handle(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	var got servermetricssvc.Snapshot
	if err := json.NewDecoder(recorder.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TimestampMS != want.TimestampMS || got.CPU.UsagePercent != want.CPU.UsagePercent {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
}

func TestHandleReportsCollectorFailure(t *testing.T) {
	handler := &Handler{
		App:       &app.Context{AdminAuthService: testutil.NewAdminAuth(t)},
		Collector: fakeCollector{err: errors.New("metrics unavailable")},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	recorder := httptest.NewRecorder()
	handler.Handle(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}
