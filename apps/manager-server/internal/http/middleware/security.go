package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
)

// Security is the outer boundary for the separated console. It keeps legacy
// wildcard CORS behaviour intact when no console allow-list is configured,
// while enforcing exact Origin + synchronizer-token checks for cookie-backed
// browser mutations as soon as CPAMP_CONSOLE_ORIGINS is set.
func Security(cfg config.Config, next http.Handler) http.Handler {
	return RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if len(cfg.ConsoleOrigins) > 0 && origin != "" && !OriginAllowed(cfg, r) {
			response.Error(w, http.StatusForbidden, errors.New("origin is not allowed"))
			return
		}

		if isMutation(r.Method) && hasSessionCookie(r) && cfg.CSRFEnabled {
			if origin == "" || !OriginAllowed(cfg, r) {
				response.Error(w, http.StatusForbidden, errors.New("request origin is required"))
				return
			}
			want := adminauthsvc.CSRFToken(SessionToken(r))
			got := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
			if want == "" || got == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
				response.Error(w, http.StatusForbidden, errors.New("invalid csrf token"))
				return
			}
		}
		next.ServeHTTP(w, r)
	}))
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hasSessionCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	cookie, err := r.Cookie(adminauthsvc.SessionCookieName)
	return err == nil && strings.TrimSpace(cookie.Value) != ""
}
