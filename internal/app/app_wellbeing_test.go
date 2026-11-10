package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// The Vita app's sync, pointed at /api/vita, reports here as it did to its
// own server, and what it sent is what the desktop and the tools read.
func TestVitaReportsToSuperLeoAsToItsOwnServer(t *testing.T) {
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{}
	mux := http.NewServeMux()
	vitaRoutes(mux, a)

	body, _ := json.Marshal(map[string]any{"days": []any{
		map[string]any{"date": "2026-11-09", "resting_hr": 56.0, "recovery": 81.0},
	}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/vita/api/days", bytes.NewReader(body)))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"stored":1`)) {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/vita/api/status", nil))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"days":1`)) {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/vita/api/days", bytes.NewReader([]byte(`{"days":[{"date":"yesterday"}]}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad date was taken: %d", rec.Code)
	}
	today, err := a.WellbeingToday()
	if err != nil || today.Day["resting_hr"] != 56.0 {
		t.Fatalf("today %+v %v", today, err)
	}
}

// The queen's engines may read health, and only read it.
func TestTheQueenMayReadHealth(t *testing.T) {
	for _, name := range wellbeingMCPTools {
		if !slices.Contains(queenMCPTools, name) {
			t.Errorf("%s is not open to a queen token", name)
		}
	}
}
