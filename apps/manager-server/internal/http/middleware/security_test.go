package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
)

func TestSecurityRejectsUnlistedOriginBeforeHandler(t *testing.T) {
	handlerCalled := false
	handler := Security(config.Config{
		ConsoleOrigins: []string{"https://console.example.com"},
		CSRFEnabled:    true,
	}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		handlerCalled = true
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.AddCookie(&http.Cookie{Name: adminauthsvc.SessionCookieName, Value: "opaque-session"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || handlerCalled {
		t.Fatalf("status=%d handlerCalled=%v", rr.Code, handlerCalled)
	}
}

func TestSecurityAcceptsExactOriginAndDerivedCSRFToken(t *testing.T) {
	handlerCalled := false
	handler := Security(config.Config{
		ConsoleOrigins: []string{"https://console.example.com"},
		CSRFEnabled:    true,
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	const sessionToken = "opaque-session"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", nil)
	req.Header.Set("Origin", "https://console.example.com")
	req.Header.Set("X-CSRF-Token", adminauthsvc.CSRFToken(sessionToken))
	req.AddCookie(&http.Cookie{Name: adminauthsvc.SessionCookieName, Value: sessionToken})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || !handlerCalled {
		t.Fatalf("status=%d handlerCalled=%v", rr.Code, handlerCalled)
	}
	if rr.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID")
	}
}
