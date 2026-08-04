package panel

import (
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const managementAssetsPrefix = "/management-assets/"

type Service struct {
	PanelPath string
	Embedded  fs.FS
}

func New(panelPath string, embedded fs.FS) *Service {
	return &Service{PanelPath: panelPath, Embedded: embedded}
}

func (s *Service) ServeManagementHTML(w http.ResponseWriter, r *http.Request, writeError func(http.ResponseWriter, int, error)) {
	if s.PanelPath != "" {
		if file, err := os.Open(s.PanelPath); err == nil {
			defer file.Close()
			info, statErr := file.Stat()
			if statErr != nil {
				writeError(w, http.StatusInternalServerError, statErr)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, "management.html", info.ModTime(), file)
			return
		}
	}
	data, err := fs.ReadFile(s.Embedded, "web/management.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	contentType := mime.TypeByExtension(".html")
	if !strings.Contains(contentType, "charset=") {
		contentType += "; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ServeManagementAssets serves the hashed JavaScript, CSS, and image files used by
// the split management panel build. The asset names are content-addressed by Vite,
// so they can safely be cached for a year.
func (s *Service) ServeManagementAssets(
	w http.ResponseWriter,
	r *http.Request,
	writeError func(http.ResponseWriter, int, error),
) {
	assetName := strings.TrimPrefix(r.URL.Path, managementAssetsPrefix)
	if assetName == r.URL.Path || !isSafeAssetPath(assetName) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	if s.PanelPath != "" {
		assetPath := filepath.Join(filepath.Dir(s.PanelPath), "management-assets", filepath.FromSlash(assetName))
		if file, err := os.Open(assetPath); err == nil {
			defer file.Close()
			info, statErr := file.Stat()
			if statErr != nil {
				writeError(w, http.StatusInternalServerError, statErr)
				return
			}
			contentType := mime.TypeByExtension(filepath.Ext(assetPath))
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			http.ServeContent(w, r, filepath.Base(assetPath), info.ModTime(), file)
			return
		}
	}

	assetFS, err := fs.Sub(s.Embedded, "web/management-assets")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	http.StripPrefix(managementAssetsPrefix, http.FileServer(http.FS(assetFS))).ServeHTTP(w, r)
}

func isSafeAssetPath(assetName string) bool {
	if assetName == "" || strings.ContainsRune(assetName, '\x00') || path.IsAbs(assetName) {
		return false
	}

	cleaned := path.Clean(assetName)
	return cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
