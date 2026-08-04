package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func installTestAdminAuth(t testing.TB, server *Server) {
	t.Helper()
	previous := server.AppContext().AdminAuthService
	server.AppContext().AdminAuthService = testutil.NewAdminAuth(t)
	t.Cleanup(func() {
		_ = server.AppContext().AdminAuthService.Close()
		_ = previous.Close()
	})
}

func TestAuthLoginSessionAndLogoutRoutes(t *testing.T) {
	handler, _ := newCompatHandler(t, testutil.NewConfig(t), nil)

	login := httptest.NewRequest(http.MethodPost, "/usage-service/auth/login", strings.NewReader(
		`{"email":"  ADMIN@example.test ","password":"`+testutil.AdminPassword+`","remember":false}`,
	))
	login.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	handler.ServeHTTP(loginRR, login)
	testutil.RequireStatus(t, loginRR, http.StatusOK)

	var loginResponse struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
		ExpiresAt int64 `json:"expiresAt"`
	}
	if err := json.Unmarshal(loginRR.Body.Bytes(), &loginResponse); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !loginResponse.Authenticated || loginResponse.User.ID != 1 || loginResponse.User.Email != testutil.AdminEmail || loginResponse.ExpiresAt <= 0 {
		t.Fatalf("login response = %#v", loginResponse)
	}
	cookies := loginRR.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %#v", cookies)
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != "cpamp_session" || sessionCookie.Value == "" || !sessionCookie.HttpOnly || sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}
	if sessionCookie.MaxAge != 0 || !sessionCookie.Expires.IsZero() {
		t.Fatalf("non-remembered cookie persisted: %#v", sessionCookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/usage-service/auth/session", nil)
	sessionRequest.AddCookie(sessionCookie)
	sessionRR := httptest.NewRecorder()
	handler.ServeHTTP(sessionRR, sessionRequest)
	testutil.RequireStatus(t, sessionRR, http.StatusOK)

	logoutRequest := httptest.NewRequest(http.MethodPost, "/usage-service/auth/logout", nil)
	logoutRequest.AddCookie(sessionCookie)
	logoutRR := httptest.NewRecorder()
	handler.ServeHTTP(logoutRR, logoutRequest)
	testutil.RequireStatus(t, logoutRR, http.StatusNoContent)
	cleared := logoutRR.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != "cpamp_session" || cleared[0].MaxAge != -1 || !cleared[0].HttpOnly {
		t.Fatalf("logout cookie = %#v", cleared)
	}

	expiredRequest := httptest.NewRequest(http.MethodGet, "/usage-service/auth/session", nil)
	expiredRequest.AddCookie(sessionCookie)
	expiredRR := httptest.NewRecorder()
	handler.ServeHTTP(expiredRR, expiredRequest)
	testutil.RequireStatus(t, expiredRR, http.StatusUnauthorized)
	if !strings.Contains(expiredRR.Body.String(), `"authenticated":false`) {
		t.Fatalf("expired session body = %s", expiredRR.Body.String())
	}
}

func TestAuthRememberedCookieIsSecureBehindTLSProxyAndBearerSessionWorks(t *testing.T) {
	handler, _ := newCompatHandler(t, testutil.NewConfig(t), nil)
	login := httptest.NewRequest(http.MethodPost, "/usage-service/auth/login", strings.NewReader(
		`{"email":"`+testutil.AdminEmail+`","password":"`+testutil.AdminPassword+`","remember":true}`,
	))
	login.Header.Set("X-Forwarded-Proto", "https")
	loginRR := httptest.NewRecorder()
	handler.ServeHTTP(loginRR, login)
	testutil.RequireStatus(t, loginRR, http.StatusOK)
	cookies := loginRR.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 12*60*60 || cookie.Expires.IsZero() {
		t.Fatalf("remembered cookie = %#v", cookie)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/usage-service/auth/session", nil)
	sessionRequest.Header.Set("Authorization", "Bearer "+cookie.Value)
	sessionRR := httptest.NewRecorder()
	handler.ServeHTTP(sessionRR, sessionRequest)
	testutil.RequireStatus(t, sessionRR, http.StatusOK)
}

func TestAuthRejectsInvalidCredentialsAndLegacyAdminKey(t *testing.T) {
	handler, _ := newCompatHandler(t, testutil.NewConfig(t), nil)
	badLogin := testutil.Request(
		t,
		handler,
		http.MethodPost,
		"/usage-service/auth/login",
		`{"email":"missing@example.test","password":"wrong","remember":false}`,
		"",
	)
	testutil.RequireStatus(t, badLogin, http.StatusUnauthorized)
	if !strings.Contains(badLogin.Body.String(), `"authenticated":false`) || strings.Contains(badLogin.Body.String(), "missing@example.test") {
		t.Fatalf("invalid login body = %s", badLogin.Body.String())
	}

	legacy := testutil.Request(t, handler, http.MethodGet, "/status", "", "cpamp_old_admin_key")
	testutil.RequireStatus(t, legacy, http.StatusUnauthorized)
	if !strings.Contains(legacy.Body.String(), `"code":"invalid_session"`) {
		t.Fatalf("legacy credential body = %s", legacy.Body.String())
	}

	unknownField := testutil.Request(
		t,
		handler,
		http.MethodPost,
		"/usage-service/auth/login",
		`{"email":"admin@example.test","password":"wrong","unexpected":true}`,
		"",
	)
	testutil.RequireStatus(t, unknownField, http.StatusBadRequest)
}
