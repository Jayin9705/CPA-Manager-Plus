package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
)

const maxLoginBodyBytes int64 = 16 * 1024

const (
	authLoginTimeout   = 5 * time.Second
	authSessionTimeout = 2 * time.Second
)

type Handler struct {
	App *app.Context
}

type authResponse struct {
	Authenticated bool      `json:"authenticated"`
	User          *authUser `json:"user,omitempty"`
	ExpiresAt     int64     `json:"expiresAt,omitempty"`
	CsrfToken     string    `json:"csrfToken,omitempty"`
}

type authUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.MethodNotAllowed(w)
		return
	}
	if h.App == nil || h.App.AdminAuthService == nil {
		writeUnavailable(w)
		return
	}
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := decodeSingleJSON(w, r, &request); err != nil {
		response.Error(w, http.StatusBadRequest, errors.New("invalid login request"))
		return
	}
	if len(request.Email) > 320 || len(request.Password) > 4096 {
		response.Error(w, http.StatusBadRequest, errors.New("invalid login request"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), authLoginTimeout)
	defer cancel()
	result, err := h.App.AdminAuthService.Login(ctx, request.Email, request.Password, clientIP(r))
	if err != nil {
		var rateLimitErr *adminauthsvc.RateLimitError
		switch {
		case errors.As(err, &rateLimitErr):
			retrySeconds := int64(math.Ceil(rateLimitErr.RetryAfter.Seconds()))
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
			response.Error(w, http.StatusTooManyRequests, errors.New("too many login attempts"))
		case errors.Is(err, adminauthsvc.ErrInvalidCredential):
			response.JSON(w, http.StatusUnauthorized, map[string]any{
				"authenticated": false,
				"error":         "invalid email or password",
			})
		default:
			writeUnavailable(w)
		}
		return
	}

	setSessionCookie(w, r, result.Token, result.Session, result.ExpiresAt, request.Remember, h.App.Config.CookieSecure)
	writeAuthenticated(w, result.Session, result.Token)
}

func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.MethodNotAllowed(w)
		return
	}
	if h.App == nil || h.App.AdminAuthService == nil {
		writeUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authSessionTimeout)
	defer cancel()
	session, err := h.App.AdminAuthService.Session(ctx, middleware.SessionToken(r))
	if err != nil {
		if errors.Is(err, adminauthsvc.ErrInvalidSession) {
			clearSessionCookie(w, r, h.App.Config.CookieSecure)
			response.JSON(w, http.StatusUnauthorized, authResponse{Authenticated: false})
			return
		}
		writeUnavailable(w)
		return
	}
	writeAuthenticated(w, session, middleware.SessionToken(r))
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.MethodNotAllowed(w)
		return
	}
	clearSessionCookie(w, r, h.App.Config.CookieSecure)
	if h.App == nil || h.App.AdminAuthService == nil {
		writeUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authSessionTimeout)
	defer cancel()
	if err := h.App.AdminAuthService.Logout(ctx, middleware.SessionToken(r)); err != nil {
		writeUnavailable(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeSingleJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON value")
		}
		return err
	}
	return nil
}

func writeAuthenticated(w http.ResponseWriter, session adminauthsvc.Session, sessionToken string) {
	response.JSON(w, http.StatusOK, authResponse{
		Authenticated: true,
		User: &authUser{
			ID:    session.AdminID,
			Email: session.AdminEmail,
		},
		ExpiresAt: session.ExpiresAtMS,
		CsrfToken: adminauthsvc.CSRFToken(sessionToken),
	})
}

func writeUnavailable(w http.ResponseWriter) {
	response.Error(w, http.StatusServiceUnavailable, errors.New("authentication service unavailable"))
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, session adminauthsvc.Session, expiresAt time.Time, remember bool, forceSecure bool) {
	cookie := &http.Cookie{
		Name:     adminauthsvc.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   forceSecure || requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	}
	if remember {
		ttlMilliseconds := session.ExpiresAtMS - session.IssuedAtMS
		maxAge := int(math.Ceil(float64(ttlMilliseconds) / float64(time.Second/time.Millisecond)))
		if maxAge < 1 {
			maxAge = 1
		}
		cookie.MaxAge = maxAge
		cookie.Expires = expiresAt.UTC()
	}
	http.SetCookie(w, cookie)
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, forceSecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminauthsvc.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   forceSecure || requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func requestIsSecure(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if r == nil {
		return false
	}
	forwardedProto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(forwardedProto), "https")
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remoteHost := remoteHost(r.RemoteAddr)
	remoteIP := net.ParseIP(remoteHost)
	if remoteIP != nil && remoteIP.IsLoopback() {
		if forwarded := firstForwardedIP(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
		if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	return remoteHost
}

func firstForwardedIP(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		candidate := strings.TrimSpace(part)
		if net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	return ""
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		return host
	}
	return strings.Trim(strings.TrimSpace(remoteAddr), "[]")
}
