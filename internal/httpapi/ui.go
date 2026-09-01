package httpapi

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// RegisterEmbeddedUI installs the Vue distribution as the fallback on the
// same Gin engine and listener as /api/v1. Asset integrity is verified during
// the package phase before the listener is acquired.
func RegisterEmbeddedUI(engine *gin.Engine, assets fs.FS) {
	engine.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(http.StatusNotFound)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		contents, err := fs.ReadFile(assets, "web/dist/"+name)
		if err != nil {
			// Only extensionless application routes may fall back to the SPA
			// shell. Returning index.html for a missing JavaScript or stylesheet
			// makes browsers reject the response because its MIME type is HTML.
			if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
				c.Status(http.StatusNotFound)
				return
			}
			name = "index.html"
			contents, err = fs.ReadFile(assets, "web/dist/index.html")
		}
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if name == "index.html" {
			// The shell contains content-hashed asset URLs, so it must not be
			// reused across application upgrades.
			c.Header("Cache-Control", "no-store")
		} else if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(contents))
	})
}
