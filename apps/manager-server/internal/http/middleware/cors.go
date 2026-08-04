package middleware

import (
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
)

const exposedMetadataHeaders = "X-Server-Version, X-Server-Commit, X-Server-Build-Date, X-CPA-Version, X-CPA-Commit, X-CPA-Build-Date, X-CPA-Support-Plugin, X-Request-ID, ETag, Retry-After, Content-Disposition, Content-Length, Content-Range"

func WithCORS(cfg config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteCORS(cfg, w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func WriteCORS(cfg config.Config, w http.ResponseWriter, r *http.Request) {
	origins := cfg.CORSOrigins
	if len(cfg.ConsoleOrigins) > 0 {
		origins = cfg.ConsoleOrigins
	}
	if len(origins) == 0 {
		return
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	allowed := ""
	for _, candidate := range origins {
		candidate = strings.TrimSpace(candidate)
		if candidate == origin || candidate == "*" {
			allowed = candidate
			break
		}
	}
	if allowed == "*" && origin != "" {
		// Credentialed CORS cannot use a wildcard response origin. Treat a
		// configured wildcard as "reflect any request origin" instead.
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	} else if allowed == "*" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else if origin != "" && allowed == origin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CPA-API-Key, X-CSRF-Token, X-Request-ID, Idempotency-Key, Content-Range, If-Match")
	w.Header().Set("Access-Control-Expose-Headers", exposedMetadataHeaders)
	if len(cfg.ConsoleOrigins) > 0 {
		w.Header().Set("Access-Control-Max-Age", "300")
	}
}

// OriginAllowed applies the separated-console exact-origin policy. Legacy
// CORSOrigins may still contain * for direct/lightweight panels, but a
// configured ConsoleOrigins list never accepts a wildcard or reflection.
func OriginAllowed(cfg config.Config, r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return len(cfg.ConsoleOrigins) == 0
	}
	origins := cfg.CORSOrigins
	strict := len(cfg.ConsoleOrigins) > 0
	if strict {
		origins = cfg.ConsoleOrigins
	}
	for _, candidate := range origins {
		candidate = strings.TrimSpace(candidate)
		if candidate == origin || (!strict && candidate == "*") {
			return true
		}
	}
	return false
}
