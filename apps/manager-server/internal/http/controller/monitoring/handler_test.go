package monitoring

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	monitoringsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestHandleAccountHistoryRejectsUnknownTargetFields(t *testing.T) {
	st := newHandlerTestStore(t)
	handler := &Handler{App: &app.Context{
		AdminAuthService:  testutil.NewAdminAuth(t),
		MonitoringService: monitoringsvc.New(st),
	}}
	req := httptest.NewRequest(
		http.MethodPost,
		"/v0/management/monitoring/account-history",
		bytes.NewBufferString(`{"accounts":[{"source_hash":"source-only"}]}`),
	)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	recorder := httptest.NewRecorder()

	handler.Handle(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "source_hash") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestHandleQuotaWeeklyUsageRejectsUnknownTargetFields(t *testing.T) {
	st := newHandlerTestStore(t)
	handler := &Handler{App: &app.Context{
		AdminAuthService:  testutil.NewAdminAuth(t),
		MonitoringService: monitoringsvc.New(st),
	}}
	req := httptest.NewRequest(
		http.MethodPost,
		"/v0/management/monitoring/quota-weekly-usage",
		bytes.NewBufferString(`{"targets":[{"target_key":"a","auth_file":"a.json","period_start_ms":1,"unexpected":true}]}`),
	)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	recorder := httptest.NewRecorder()

	handler.Handle(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unexpected") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func newHandlerTestStore(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}
