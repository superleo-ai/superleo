package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Wellbeing and finance are on until named off; the browser and desktop
// control follow their own settings.
func TestFeaturesDefaultsAndSwitching(t *testing.T) {
	t.Setenv("SUPERLEO_BROWSER_MODE", "")
	t.Setenv("SUPERLEO_DESKTOP", "")
	s := &Settings{}
	got := s.Features()
	if got[FeatureBrowser] || got[FeatureDesktop] || !got[FeatureWellbeing] || !got[FeatureFinance] {
		t.Fatalf("defaults: %v", got)
	}
	s.SetFeature(FeatureFinance, false)
	s.SetFeature(FeatureFinance, false)
	if len(s.FeaturesOff) != 1 || s.FeatureOn(FeatureFinance) {
		t.Fatalf("finance off: %v", s.FeaturesOff)
	}
	s.SetFeature(FeatureFinance, true)
	if s.FeaturesOff != nil {
		t.Fatalf("finance back on: %v", s.FeaturesOff)
	}
	s.SetFeature(FeatureBrowser, true)
	if s.BrowserMode != BrowserModeOwn {
		t.Fatalf("browser on with no history: %q", s.BrowserMode)
	}
	s.BrowserLastMode = BrowserModeChrome
	s.SetFeature(FeatureBrowser, false)
	s.SetFeature(FeatureBrowser, true)
	if s.BrowserMode != BrowserModeChrome {
		t.Fatalf("browser on again: %q", s.BrowserMode)
	}
	if s.SetFeature("nope", true) {
		t.Fatal("an unknown feature was taken")
	}
}

// A feature switched off takes its MCP server out of what agent-go reads,
// and out of every lookup that finds it.
func TestSwitchedOffServerIsNotMounted(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "mcpServers.json")
	raw := `{"mcpServers":{"finance":{"type":"http","url":"http://f/mcp"},"vita":{"type":"http","url":"http://v/mcp"},"noted":{"type":"http","url":"http://n/mcp"}}}`
	if err := os.WriteFile(src, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (string, bool) { return "", false }
	t.Cleanup(func() { applyFeatures(&Settings{}) })

	applyFeatures(&Settings{})
	if got, err := resolveMCPConfig(src, lookup); err != nil || got != src {
		t.Fatalf("all on, the file is read as is: %q, %v", got, err)
	}

	applyFeatures(&Settings{FeaturesOff: []string{FeatureFinance}})
	got, err := resolveMCPConfig(src, lookup)
	if err != nil || got == src {
		t.Fatalf("finance off: %q, %v", got, err)
	}
	b, _ := os.ReadFile(got)
	if strings.Contains(string(b), `"finance"`) || !strings.Contains(string(b), `"vita"`) || !strings.Contains(string(b), `"noted"`) {
		t.Fatalf("resolved: %s", b)
	}
	if _, _, _, ok := findMCPServer([]string{src}, FinanceServer, lookup); ok {
		t.Fatal("finance found while off")
	}
	if _, _, _, ok := findMCPServer([]string{src}, VitaServer, lookup); !ok {
		t.Fatal("vita not found while on")
	}
}
