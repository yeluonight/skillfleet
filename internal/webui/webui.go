// Package webui ships the compiled React WebUI alongside the server
// binary. The actual bundle lives under embed/dist/ and is copied
// there by `make web-embed` before `go build`. A small stub
// index.html ships with the repository so `go build` succeeds even
// when the JS toolchain hasn't been run yet — the stub explains the
// situation in plain English when an operator opens the page.
//
// Routing model (v2.0 §5.6):
//
//	/api/*    -> handled by internal/api before this handler runs
//	/agent/*  -> handled by internal/agentapi (phase 2+)
//	/health   -> handled by internal/api
//	<other>   -> served from the embedded fs, with SPA fallback to
//	             index.html so hash-router refreshes don't 404.
package webui

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed embed/dist
var bundle embed.FS

// FS returns the embedded WebUI filesystem rooted at the dist
// directory (i.e. `index.html` is at the root).
func FS() fs.FS {
	sub, err := fs.Sub(bundle, "embed/dist")
	if err != nil {
		// Compile-time embed guarantees the directory exists, so
		// this branch is impossible. Panic-style fallback makes
		// the bug obvious if a future refactor breaks the invariant.
		panic("webui: embed/dist subtree missing: " + err.Error())
	}
	return sub
}

// Handler returns an http.Handler that serves the embedded WebUI with
// SPA fallback: any GET to an unknown path returns index.html so
// client-side routing can resolve it. Non-GET methods receive 405.
//
// Callers MUST register this handler last (or on a catch-all pattern
// like "GET /") so the specific /api, /agent, /health routes take
// precedence.
func Handler() http.Handler {
	uifs := FS()
	fileServer := http.FileServer(http.FS(uifs))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Normalise: stripping the leading slash makes the path
		// usable as an fs.FS key. The root "" maps to index.html
		// implicitly via the file server, but we still need the
		// path string for the existence check below.
		upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if upath == "" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Refuse paths that escape the dist root. path.Clean above
		// drops ".." segments at the root but a request like
		// "/../../etc/passwd" would still be served via the file
		// server's own checks; rejecting up-front keeps the contract
		// explicit and matches the SafeFS spirit (v2.0 §1.3).
		if strings.HasPrefix(upath, "../") || strings.Contains(upath, "/../") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Real file in the bundle? Serve it (the file server handles
		// content-type, ETag, range requests etc.).
		if f, err := uifs.Open(upath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// SPA fallback. We do NOT fall back for asset-shaped paths
		// (anything containing a "." in the last segment) so a
		// missing /assets/foo-XYZ.js fails fast as 404 rather than
		// silently returning HTML.
		last := upath
		if i := strings.LastIndex(upath, "/"); i >= 0 {
			last = upath[i+1:]
		}
		if strings.Contains(last, ".") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		serveIndex(w, r, uifs)
	})
}

// serveIndex streams the embedded index.html with appropriate caching
// headers. The file is tiny and changes every build, so we tell the
// browser to revalidate every time.
func serveIndex(w http.ResponseWriter, r *http.Request, uifs fs.FS) {
	f, err := uifs.Open("index.html")
	if err != nil {
		http.Error(w, "index.html missing from bundle", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.Copy(w, f)
}
