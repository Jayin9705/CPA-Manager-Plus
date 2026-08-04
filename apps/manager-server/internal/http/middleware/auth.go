package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/security"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
)

type SessionVerifier interface {
	VerifySession(ctx context.Context, token string) (bool, error)
}

type adminHeaderVerifier interface {
	VerifyHeader(ctx context.Context, authorizationHeader string) (bool, error)
}

type panelHeaderVerifier interface {
	VerifyPanelHeader(ctx context.Context, authorizationHeader string) (bool, error)
	PanelUsesExternalManagementKey(ctx context.Context) (bool, error)
}

const adminAuthVerifyTimeout = 2 * time.Second

// AuthorizeAdmin and AuthorizePanel intentionally share the same session
// policy. The two names are retained so controllers do not accidentally drift
// into different authentication schemes again.
func AuthorizeAdmin(w http.ResponseWriter, r *http.Request, verifier SessionVerifier) bool {
	return authorizeSession(w, r, verifier, false)
}

func AuthorizePanel(w http.ResponseWriter, r *http.Request, verifier SessionVerifier) bool {
	return authorizeSession(w, r, verifier, true)
}

func authorizeSession(w http.ResponseWriter, r *http.Request, verifier SessionVerifier, panel bool) bool {
	if verifier == nil {
		response.Error(w, http.StatusServiceUnavailable, errors.New("authentication service unavailable"))
		return false
	}
	verifyCtx, cancel := context.WithTimeout(r.Context(), adminAuthVerifyTimeout)
	defer cancel()

	var headerErr error
	authorizationHeader := r.Header.Get("Authorization")
	if strings.TrimSpace(authorizationHeader) != "" {
		var headerOK bool
		if panel {
			if headerVerifier, ok := verifier.(panelHeaderVerifier); ok {
				headerOK, headerErr = headerVerifier.VerifyPanelHeader(verifyCtx, authorizationHeader)
			}
		} else if headerVerifier, ok := verifier.(adminHeaderVerifier); ok {
			headerOK, headerErr = headerVerifier.VerifyHeader(verifyCtx, authorizationHeader)
		}
		if headerOK {
			return true
		}
	}

	ok, err := verifier.VerifySession(verifyCtx, SessionToken(r))
	if err != nil {
		response.Error(w, http.StatusServiceUnavailable, errors.New("authentication service unavailable"))
		return false
	}
	if ok {
		return true
	}
	if headerErr != nil {
		response.Error(w, http.StatusInternalServerError, headerErr)
		return false
	}
	if panel {
		if headerVerifier, ok := verifier.(panelHeaderVerifier); ok {
			external, externalErr := headerVerifier.PanelUsesExternalManagementKey(verifyCtx)
			if externalErr != nil {
				response.Error(w, http.StatusInternalServerError, externalErr)
				return false
			}
			if external {
				response.Error(w, http.StatusUnauthorized, errors.New("invalid management key"))
				return false
			}
		}
	}
	response.Error(w, http.StatusUnauthorized, errors.New("invalid session"))
	return false
}

// SessionToken accepts the browser cookie first and a Bearer session token as
// a fallback for non-browser clients. Legacy Admin Keys are verified through
// the separate header verifier before the value reaches the session backend.
func SessionToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if cookie, err := r.Cookie(adminauthsvc.SessionCookieName); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token
		}
	}
	return security.ExtractBearerToken(r.Header.Get("Authorization"))
}

// StripSessionCookie prevents the Manager Server's browser credential from
// being forwarded to the CPA upstream while preserving unrelated cookies.
func StripSessionCookie(r *http.Request) {
	if r == nil {
		return
	}
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != adminauthsvc.SessionCookieName {
			r.AddCookie(cookie)
		}
	}
}
