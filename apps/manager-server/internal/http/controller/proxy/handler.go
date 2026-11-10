package proxy

import (
	"errors"
	"net/http"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
)

type Handler struct {
	App *app.Context
}

func (h *Handler) Management(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizeAdmin(w, r, h.App.AdminAuthService) {
		return
	}
	middleware.StripSessionCookie(r)
	r.Header.Del("Authorization")
	if proxysvc.IsCPAPluginManagementPath(r.URL.Path) {
		h.App.ProxyService.ProxyPluginManagement(w, r, response.Error)
		return
	}
	h.App.ProxyService.ProxyManagement(w, r, response.Error)
}

func (h *Handler) Playground(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizeAdmin(w, r, h.App.AdminAuthService) {
		return
	}
	middleware.StripSessionCookie(r)
	// The browser session authenticates this Manager request. The CPA API key
	// is carried separately in X-CPA-API-Key and is replaced by the proxy.
	r.Header.Del("Authorization")
	h.App.ProxyService.ProxyPlayground(w, r, response.Error)
}

func (h *Handler) ModelList(w http.ResponseWriter, r *http.Request) {
	middleware.StripSessionCookie(r)
	h.App.ProxyService.ProxyModelList(w, r, response.Error, response.MethodNotAllowed)
}

// V8APIKeysConfig forwards CPA v8 API-key group reads for verified admins only.
// Callers' own credentials are never passed through to the v8 management API.
func (h *Handler) V8APIKeysConfig(w http.ResponseWriter, r *http.Request) {
	ok, err := h.App.AdminAuthService.VerifyHeader(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		response.Error(w, http.StatusUnauthorized, errors.New("invalid admin key"))
		return
	}
	h.App.ProxyService.ProxyV8APIKeysConfig(w, r, response.Error, response.MethodNotAllowed)
}

func (h *Handler) CPAResource(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if !middleware.AuthorizeAdmin(w, r, h.App.AdminAuthService) {
			return
		}
		middleware.StripSessionCookie(r)
		r.Header.Del("Authorization")
		h.App.ProxyService.ProxyPluginResource(w, r, response.Error)
		return
	default:
		response.MethodNotAllowed(w)
		return
	}

	sessionOK, err := h.App.AdminAuthService.VerifySession(r.Context(), middleware.SessionToken(r))
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	headerOK := false
	if !sessionOK {
		headerOK, err = h.App.AdminAuthService.VerifyHeader(r.Context(), r.Header.Get("Authorization"))
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
	}
	middleware.StripSessionCookie(r)
	// Only a verified Manager session or compatibility admin key authorizes
	// substituting the saved CPA management key. Otherwise preserve the
	// caller's own authorization so public plugin resources cannot gain
	// Manager privileges through this proxy.
	if sessionOK || headerOK {
		r.Header.Del("Authorization")
		h.App.ProxyService.ProxyPluginResource(w, r, response.Error)
		return
	}
	h.App.ProxyService.ProxyPluginResourceWithCallerAuth(w, r, response.Error)
}
