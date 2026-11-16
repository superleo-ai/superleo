package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

func workspaceFixture(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "workspace")
	outside = filepath.Join(base, "secret")
	for _, d := range []string{filepath.Join(root, "outputs"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "outputs", "我的理财 2026.pptx"), "deck")
	write(filepath.Join(root, "chart.png"), "png")
	write(filepath.Join(outside, "settings.json"), `{"key":"x"}`)
	if err := os.Symlink(filepath.Join(outside, "settings.json"), filepath.Join(root, "leak.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outdir")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestResolveWorkspaceFile(t *testing.T) {
	root, outside := workspaceFixture(t)
	ok := []string{
		"outputs/我的理财 2026.pptx",
		"./outputs/我的理财 2026.pptx",
		"chart.png",
		filepath.Join(root, "chart.png"),
		"file://" + filepath.Join(root, "chart.png"),
		"file://" + (&url.URL{Path: filepath.Join(root, "outputs", "我的理财 2026.pptx")}).EscapedPath(),
	}
	for _, p := range ok {
		if _, _, err := resolveWorkspaceFile(root, p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	refused := map[string]error{
		"../secret/settings.json":                    errOutsideWorkspace,
		filepath.Join(outside, "settings.json"):      errOutsideWorkspace,
		"file://" + filepath.Join(outside, "x.json"): errOutsideWorkspace,
		"leak.json":            errOutsideWorkspace,
		"outdir/settings.json": errOutsideWorkspace,
		"outputs":              errNotAFile,
		"":                     errNotAFile,
		"missing.pdf":          os.ErrNotExist,
	}
	for p, want := range refused {
		if _, _, err := resolveWorkspaceFile(root, p); !errors.Is(err, want) {
			t.Errorf("%q: got %v, want %v", p, err, want)
		}
	}
	if _, _, err := resolveWorkspaceFile("", "chart.png"); !errors.Is(err, errWorkspaceUnset) {
		t.Errorf("no workspace: %v", err)
	}
}

func TestResolveWorkspaceFileTooBig(t *testing.T) {
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxWorkspaceFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, _, err := resolveWorkspaceFile(root, "big.bin"); !errors.Is(err, errTooBig) {
		t.Fatalf("got %v", err)
	}
}

func TestHandleWorkspaceFile(t *testing.T) {
	root, _ := workspaceFixture(t)
	a := &App{settings: &backend.Settings{WorkspaceDir: root}}
	get := func(p string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		a.handleWorkspaceFile(rr, httptest.NewRequest(http.MethodGet, workspaceFilePath+"?path="+url.QueryEscape(p), nil))
		return rr
	}
	rr := get("outputs/我的理财 2026.pptx")
	if rr.Code != http.StatusOK || rr.Body.String() != "deck" {
		t.Fatalf("pptx: %d %q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "presentation") {
		t.Errorf("content type %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "filename") {
		t.Errorf("disposition %q", cd)
	}
	for p, code := range map[string]int{
		"leak.json":               http.StatusForbidden,
		"../secret/settings.json": http.StatusForbidden,
		"missing.png":             http.StatusNotFound,
		"outputs":                 http.StatusBadRequest,
	} {
		if rr := get(p); rr.Code != code || strings.Contains(rr.Body.String(), `"key"`) {
			t.Errorf("%q: %d %q", p, rr.Code, rr.Body.String())
		}
	}
	rr = httptest.NewRecorder()
	a.handleWorkspaceFile(rr, httptest.NewRequest(http.MethodPost, workspaceFilePath+"?path=chart.png", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rr.Code)
	}
	if !gatedPath(workspaceFilePath) {
		t.Errorf("%s is not behind authentication", workspaceFilePath)
	}
}

func TestReadWorkspaceFileRPCsStayInside(t *testing.T) {
	root, _ := workspaceFixture(t)
	a := &App{settings: &backend.Settings{WorkspaceDir: root}}
	if !a.readableWorkspaceFile("chart.png") {
		t.Error("chart.png refused")
	}
	for _, p := range []string{"leak.json", "outdir/settings.json", "../secret/settings.json"} {
		if a.readableWorkspaceFile(p) {
			t.Errorf("%q readable", p)
		}
		if got := a.ReadWorkspaceFileDataURL(p); got != "" {
			t.Errorf("%q: %q", p, got)
		}
	}
}
