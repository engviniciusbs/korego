package webui_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/engviniciusbs/korego/internal/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<html>home</html>")},
		"login/index.html":           {Data: []byte("<html>login</html>")},
		"404.html":                   {Data: []byte("<html>nada</html>")},
		"_next/static/chunks/app.js": {Data: []byte("console.log(1)")},
		"_next/static/css/app.css":   {Data: []byte("a{}")},
		"favicon.ico":                {Data: []byte("\x00\x00\x01\x00")},
		"data.json":                  {Data: []byte("{}")},
		"logo.svg":                   {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"font.woff2":                 {Data: []byte("wOF2")},
		"robots.txt":                 {Data: []byte("User-agent: *")},
	}
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func TestServesIndex(t *testing.T) {
	w := get(webui.NewHandler(testFS()), http.MethodGet, "/")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<html>home</html>", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestTrailingSlashRoute(t *testing.T) {
	w := get(webui.NewHandler(testFS()), http.MethodGet, "/login/")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<html>login</html>", w.Body.String())
}

func TestRedirectsDirWithoutSlash(t *testing.T) {
	w := get(webui.NewHandler(testFS()), http.MethodGet, "/login?next=%2Fx")
	assert.Equal(t, http.StatusMovedPermanently, w.Code)
	assert.Equal(t, "/login/?next=%2Fx", w.Header().Get("Location"))
}

func TestNotFoundPage404(t *testing.T) {
	w := get(webui.NewHandler(testFS()), http.MethodGet, "/nao-existe/")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "<html>nada</html>", w.Body.String())

	// Sem 404.html: texto simples, ainda 404.
	fsys := testFS()
	delete(fsys, "404.html")
	w = get(webui.NewHandler(fsys), http.MethodGet, "/nao-existe/")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStaticCacheHeaders(t *testing.T) {
	h := webui.NewHandler(testFS())
	w := get(h, http.MethodGet, "/_next/static/chunks/app.js")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
	assert.Equal(t, "text/javascript; charset=utf-8", w.Header().Get("Content-Type"))

	w = get(h, http.MethodGet, "/login/")
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
}

func TestContentTypes(t *testing.T) {
	h := webui.NewHandler(testFS())
	cases := map[string]string{
		"/_next/static/css/app.css": "text/css",
		"/data.json":                "application/json",
		"/logo.svg":                 "image/svg+xml",
		"/font.woff2":               "font/woff2",
		"/robots.txt":               "text/plain",
		"/favicon.ico":              "image/",
	}
	for p, want := range cases {
		w := get(h, http.MethodGet, p)
		require.Equal(t, http.StatusOK, w.Code, p)
		assert.Contains(t, w.Header().Get("Content-Type"), want, p)
	}
}

func TestEmptyDist503(t *testing.T) {
	w := get(webui.NewHandler(fstest.MapFS{".gitkeep": {}}), http.MethodGet, "/")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "Frontend não compilado. Rode `make web`.")
}

func TestMethodNotAllowed(t *testing.T) {
	h := webui.NewHandler(testFS())
	assert.Equal(t, http.StatusMethodNotAllowed, get(h, http.MethodPost, "/").Code)
	assert.Equal(t, http.StatusOK, get(h, http.MethodHead, "/").Code)
}

func TestPathTraversalBlocked(t *testing.T) {
	h := webui.NewHandler(testFS())
	for _, p := range []string{"/../go.mod", "/%2e%2e/x", "/..%2f..%2fgo.mod", "/login/../../go.mod", "//etc/passwd"} {
		w := get(h, http.MethodGet, p)
		assert.NotEqual(t, http.StatusOK, w.Code, p)
		assert.NotContains(t, w.Body.String(), "module ", p)
	}
	assert.Equal(t, http.StatusNotFound, get(h, http.MethodGet, "/%2e%2e/x").Code)
}
