package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A picture pasted into a terminal Claude Code session, with a prompt or in
// a message typed while a turn ran, shows up in the log as a reference that
// reads back the picture itself.
func TestClaudeSessionPictures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "-p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-25T01:00:00Z","message":{"role":"user","content":[{"type":"text","text":"[Image #1] look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"UE5H"}}]}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-25T01:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"attachment","uuid":"q1","timestamp":"2026-11-25T01:00:02Z","attachment":{"type":"queued_command","origin":{"kind":"human"},"prompt":[{"type":"text","text":"[Image #2] and this"},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"SlBH"}}]}}`,
		`{"type":"attachment","uuid":"h1","timestamp":"2026-11-25T01:00:03Z","attachment":{"type":"hook_success","content":"x"}}`,
		`{"type":"attachment","uuid":"q2","timestamp":"2026-11-25T01:00:04Z","attachment":{"type":"queued_command","origin":{"kind":"task-notification"},"prompt":"not the person"}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	log, err := readSessionLog("claude", "s1")
	if err != nil {
		t.Fatal(err)
	}
	var you []CLIRunEvent
	for _, e := range log.Events {
		if e.Kind == "you" {
			you = append(you, e)
		}
	}
	if len(you) != 2 {
		t.Fatalf("what the person said: %+v", you)
	}
	if you[0].Text != "[Image #1] look" || !reflect.DeepEqual(you[0].Images, []string{"u1/1"}) {
		t.Fatalf("prompt with a picture: %+v", you[0])
	}
	if you[1].Text != "[Image #2] and this" || !reflect.DeepEqual(you[1].Images, []string{"q1/1"}) {
		t.Fatalf("message typed mid-turn: %+v", you[1])
	}

	for ref, want := range map[string]string{"u1/1": "data:image/png;base64,UE5H", "q1/1": "data:image/jpeg;base64,SlBH"} {
		got, err := readSessionImage("claude", "s1", ref)
		if err != nil || got != want {
			t.Fatalf("%s: %q, %v", ref, got, err)
		}
	}
	for _, ref := range []string{"u1/0", "a1/0", "nope/1", "u1", "u1/x"} {
		if _, err := readSessionImage("claude", "s1", ref); err == nil {
			t.Fatalf("%s read as a picture", ref)
		}
	}
}
