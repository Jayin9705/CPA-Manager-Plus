//go:build !api_only

package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/buildinfo"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestServerCompatHealthInfoAndPanel(t *testing.T) {
	cfg := testutil.NewConfig(t)
	handler, _ := newCompatHandler(t, cfg, nil)

	healthRR := testutil.Request(t, handler, http.MethodGet, "/health", "", "")
	testutil.RequireStatus(t, healthRR, http.StatusOK)
	var health struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	testutil.DecodeJSON(t, healthRR, &health)
	if !health.OK || health.Service == "" {
		t.Fatalf("health response = %#v", health)
	}
	if got := healthRR.Header().Get("X-Server-Version"); got != buildinfo.Version {
		t.Fatalf("X-Server-Version = %q, want %q", got, buildinfo.Version)
	}
	if got := healthRR.Header().Get("X-Server-Build-Date"); got != buildinfo.BuildDate {
		t.Fatalf("X-Server-Build-Date = %q, want %q", got, buildinfo.BuildDate)
	}

	infoRR := testutil.Request(t, handler, http.MethodGet, "/usage-service/info", "", "")
	testutil.RequireStatus(t, infoRR, http.StatusOK)
	var info struct {
		Service    string `json:"service"`
		Mode       string `json:"mode"`
		StartedAt  int64  `json:"startedAt"`
		Configured bool   `json:"configured"`
	}
	testutil.DecodeJSON(t, infoRR, &info)
	if info.Service != serviceID || info.Mode != "embedded" || info.StartedAt <= 0 || info.Configured {
		t.Fatalf("info response = %#v", info)
	}

	rootRR := testutil.Request(t, handler, http.MethodGet, "/", "", "")
	testutil.RequireStatus(t, rootRR, http.StatusTemporaryRedirect)
	if rootRR.Header().Get("Location") != "/management.html" {
		t.Fatalf("root location = %q", rootRR.Header().Get("Location"))
	}

	panelRR := testutil.Request(t, handler, http.MethodGet, "/management.html", "", "")
	testutil.RequireStatus(t, panelRR, http.StatusOK)
	if !strings.Contains(panelRR.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("panel content type = %q", panelRR.Header().Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(panelRR.Body.String()), "<html") {
		t.Fatalf("panel body does not look like html")
	}
	if got, want := panelRR.Header().Get("Content-Length"), strconv.Itoa(panelRR.Body.Len()); got != want {
		t.Fatalf("panel content length = %q, want %q", got, want)
	}
}

func TestServerCompatPanelPathOverridesEmbeddedPanel(t *testing.T) {
	cfg := testutil.NewConfig(t)
	panelPath := filepath.Join(t.TempDir(), "management.html")
	customPanel := "<html><body>custom panel</body></html>"
	if err := osWriteFile(panelPath, []byte(customPanel)); err != nil {
		t.Fatalf("write panel: %v", err)
	}
	cfg.PanelPath = panelPath
	handler, _ := newCompatHandler(t, cfg, nil)

	rr := testutil.Request(t, handler, http.MethodGet, "/management.html", "", "")
	testutil.RequireStatus(t, rr, http.StatusOK)
	if rr.Body.String() != customPanel {
		t.Fatalf("panel body = %q", rr.Body.String())
	}
	if got, want := rr.Header().Get("Content-Length"), strconv.Itoa(len(customPanel)); got != want {
		t.Fatalf("panel content length = %q, want %q", got, want)
	}
}

func TestServerCompatPanelPathServesSplitAssets(t *testing.T) {
	root := t.TempDir()
	panelPath := filepath.Join(root, "management.html")
	assetPath := filepath.Join(root, "management-assets", "assets", "app.js")
	if err := osWriteFile(panelPath, []byte("<html></html>")); err != nil {
		t.Fatalf("write panel: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatalf("create asset directory: %v", err)
	}
	if err := osWriteFile(assetPath, []byte("console.log('ok');")); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	cfg := testutil.NewConfig(t)
	cfg.PanelPath = panelPath
	handler, _ := newCompatHandler(t, cfg, nil)

	rr := testutil.Request(t, handler, http.MethodGet, "/management-assets/assets/app.js", "", "")
	testutil.RequireStatus(t, rr, http.StatusOK)
	if rr.Body.String() != "console.log('ok');" {
		t.Fatalf("asset body = %q", rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache control = %q", got)
	}
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
