package app

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// One workspace file, as bytes, for a phone.
//
// An answer links what an agent made by its path in the workspace
// ("outputs/report.pptx"). The desktop opens that on the machine itself; a
// phone has to fetch it. ReadWorkspaceFileDataURL could, but it answers base64
// inside JSON: a 40MB deck becomes a 54MB string held in memory twice on each
// side. This route streams the file as it is.
//
// The path comes from a link a model wrote, so only a regular file inside the
// workspace is served: nothing that resolves outside it, symlinks included,
// and nothing bigger than the cap.

// workspaceFilePath is the route. Under /api, so gated like the rest (auth.go).
const workspaceFilePath = "/api/workspace/file"

// maxWorkspaceFileBytes caps one download.
const maxWorkspaceFileBytes = 50 << 20

var (
	errWorkspaceUnset   = errors.New("workspace not configured")
	errOutsideWorkspace = errors.New("outside the workspace")
	errNotAFile         = errors.New("not a file")
	errTooBig           = errors.New("file too large")
)

// resolveWorkspaceFile turns a link's path into a file inside root: relative
// ("outputs/a.pptx", "./a.pptx"), file:// or absolute inside root. It answers
// the real path, symlinks resolved, and refuses anything that lands outside.
func resolveWorkspaceFile(root, p string) (string, os.FileInfo, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil, errWorkspaceUnset
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, errWorkspaceUnset
	}
	p = strings.TrimSpace(p)
	if strings.HasPrefix(strings.ToLower(p), "file://") {
		u, err := url.Parse(p)
		if err != nil {
			return "", nil, fmt.Errorf("bad path")
		}
		p = u.Path
	}
	if p == "" || strings.ContainsRune(p, 0) {
		return "", nil, errNotAFile
	}
	full := p
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)
	if !within(root, full) && !within(realRoot, full) {
		return "", nil, errOutsideWorkspace
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", nil, err
	}
	if !within(realRoot, real) {
		return "", nil, errOutsideWorkspace
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errNotAFile
	}
	if info.Size() > maxWorkspaceFileBytes {
		return "", nil, errTooBig
	}
	return real, info, nil
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// readableWorkspaceFile is the same line for the RPCs that read a workspace
// file (ReadWorkspaceFile, ReadWorkspaceFileDataURL): the sandbox checks the
// path as written, and a symlink inside the workspace would otherwise lead
// out of it.
func (a *App) readableWorkspaceFile(path string) bool {
	_, _, err := resolveWorkspaceFile(a.workspaceDir(), path)
	return err == nil
}

// handleWorkspaceFile serves GET /api/workspace/file?path=<path>.
func (a *App) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	real, info, err := resolveWorkspaceFile(a.workspaceDir(), r.URL.Query().Get("path"))
	if err != nil {
		code := http.StatusBadRequest
		msg := err.Error()
		switch {
		case errors.Is(err, os.ErrNotExist):
			code, msg = http.StatusNotFound, "file not found"
		case errors.Is(err, errOutsideWorkspace):
			code = http.StatusForbidden
		case errors.Is(err, errTooBig):
			code = http.StatusRequestEntityTooLarge
		case errors.Is(err, errWorkspaceUnset):
			code = http.StatusServiceUnavailable
		}
		http.Error(w, msg, code)
		return
	}
	f, err := os.Open(real)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	name := filepath.Base(real)
	w.Header().Set("Content-Type", mimeTypeFor(name))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// mimeTypeFor guesses a content type from the extension.
func mimeTypeFor(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}
