package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testLifeService(t *testing.T) *Service {
	t.Helper()
	return &Service{store: newLifeStore(filepath.Join(t.TempDir(), "life.json"))}
}

func TestAddScheduleStoresRow(t *testing.T) {
	s := testLifeService(t)

	rec, err := s.AddSchedule("  和设计过稿  ", " 2026-09-03T09:30:00+08:00 ", " 会议室 ",
		[]string{" 小张 ", "", "  "})
	if err != nil {
		t.Fatalf("AddSchedule: %v", err)
	}
	if got := rec["title"]; got != "和设计过稿" {
		t.Errorf("title not trimmed: %q", got)
	}
	if got := rec["start_at"]; got != "2026-09-03T09:30:00+08:00" {
		t.Errorf("start_at not trimmed: %q", got)
	}
	// A blank participant is dropped rather than stored as an empty name.
	parts, ok := rec["participants"].([]string)
	if !ok || len(parts) != 1 || parts[0] != "小张" {
		t.Errorf("participants = %#v, want [小张]", rec["participants"])
	}
	if rec["id"] == "" || rec["id"] == nil {
		t.Error("id not assigned")
	}

	life := s.Life()
	if len(life.Schedules) != 1 {
		t.Fatalf("Life().Schedules = %d rows, want 1", len(life.Schedules))
	}
	if life.Schedules[0]["title"] != "和设计过稿" {
		t.Errorf("stored row does not match returned row: %#v", life.Schedules[0])
	}
}

// The tool schema has always declared both required. Enforcing it in the shared
// method is what stops a `ui` button, which never sees that schema, from
// writing a calendar entry with no time on it.
func TestAddScheduleRejectsMissingFields(t *testing.T) {
	for _, tc := range []struct{ name, title, startAt string }{
		{"no title", "   ", "2026-09-03T09:30:00+08:00"},
		{"no start", "标题", "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testLifeService(t)
			if _, err := s.AddSchedule(tc.title, tc.startAt, "", nil); err == nil {
				t.Fatal("expected an error")
			}
			if got := len(s.Life().Schedules); got != 0 {
				t.Errorf("rejected entry was still stored (%d rows)", got)
			}
		})
	}
}

// Empty lists must serialize as `[]`, not `null`: the rows go to the Life panel
// and to the model as JSON, and both read participants as a list.
func TestListFieldsAreNeverNil(t *testing.T) {
	s := testLifeService(t)
	sch, err := s.AddSchedule("标题", "2026-09-03T09:30:00+08:00", "", nil)
	if err != nil {
		t.Fatalf("AddSchedule: %v", err)
	}
	if p, ok := sch["participants"].([]string); !ok || p == nil {
		t.Errorf("participants = %#v, want an empty slice", sch["participants"])
	}
}

// A nil Service must answer rather than panic: the App methods call straight
// through before the agent has finished booting.
func TestLifeWritesOnUnbuiltService(t *testing.T) {
	var s *Service
	if _, err := s.AddSchedule("t", "now", "", nil); err == nil {
		t.Error("expected an error from a nil service")
	}
}

func TestForgetRemovesOnlyWhatWasNamed(t *testing.T) {
	s := testLifeService(t)
	a, _ := s.AddSchedule("standup", "2026-10-06T09:00:00+02:00", "", nil)
	b, _ := s.AddSchedule("weekly", "2026-10-08T13:30:00+02:00", "Zoom", nil)

	if err := s.Forget("schedule", a["id"].(string)); err != nil {
		t.Fatal(err)
	}
	life := s.Life()
	if len(life.Schedules) != 1 || life.Schedules[0]["id"] != b["id"] {
		t.Fatalf("schedules left: %v", life.Schedules)
	}
	if err := s.Forget("schedule", "nope"); err == nil {
		t.Fatal("forgetting what is not there succeeded")
	}
	for _, kind := range []string{"reminder", "record", "person"} {
		if err := s.Forget(kind, "x"); err == nil {
			t.Fatalf("%s has no delete here, but it was accepted", kind)
		}
	}
}

// Notes and people moved to noted. What an older build saved under "records"
// and "persons" stays in the file through later saves, untouched, until it is
// moved there by hand.
func TestOldNotesAndPeopleSurviveASave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "life.json")
	old := `{"schedules":[],"records":[{"id":"r1","body":"GUI ships"}],"persons":{"Rene":{"relation":"colleague"}},"reminders":[]}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{store: newLifeStore(path)}
	s.store.load()
	if _, err := s.AddSchedule("standup", "2026-10-06T09:00:00+02:00", "", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if rs, _ := got["records"].([]any); len(rs) != 1 {
		t.Errorf("records lost: %s", raw)
	}
	if ps, _ := got["persons"].(map[string]any); ps["Rene"] == nil {
		t.Errorf("persons lost: %s", raw)
	}
	if ss, _ := got["schedules"].([]any); len(ss) != 1 {
		t.Errorf("schedule not saved: %s", raw)
	}
}
