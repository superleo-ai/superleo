package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The phone's report goes on to vita with the token vita's MCP entry
// carries, and the status says whether vita is connected at all.
func TestThePhonesReportIsPassedOnToVita(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	a := &App{}

	st, err := a.WellbeingStatus()
	if err != nil || st["connected"] != false {
		t.Fatalf("without vita: %v %v", st, err)
	}
	if _, err := a.WellbeingReport([]map[string]any{{"date": "2026-11-09"}}); err == nil {
		t.Fatal("a report with no vita to take it was said to be stored")
	}

	var got struct {
		Days []map[string]any `json:"days"`
	}
	var auth string
	vita := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/days":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &got)
			_, _ = io.WriteString(w, `{"stored":1}`)
		case "/api/status":
			_, _ = io.WriteString(w, `{"days":12,"last":"2026-11-09"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer vita.Close()
	cfg := map[string]any{"mcpServers": map[string]any{"vita": map[string]any{
		"type": "http", "url": vita.URL + "/mcp",
		"headers": map[string]any{"Authorization": "Bearer ${VITA_TOKEN}"},
		"env":     map[string]any{"VITA_TOKEN": "tok-0123456789abcdef"},
	}}}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(home, "mcpServers.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := a.WellbeingReport([]map[string]any{{"date": "2026-11-09", "resting_hr": 56.0}})
	if err != nil || n != 1 || len(got.Days) != 1 || got.Days[0]["resting_hr"] != 56.0 {
		t.Fatalf("report: %d %v %+v", n, err, got)
	}
	if auth != "Bearer tok-0123456789abcdef" {
		t.Fatalf("vita was sent %q", auth)
	}
	st, err = a.WellbeingStatus()
	if err != nil || st["connected"] != true || st["days"] != 12.0 {
		t.Fatalf("status: %v %v", st, err)
	}
}
