package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hiveWith(engine string) backend.HiveSettings {
	var h backend.HiveSettings
	if engine != "" {
		h.SetQueenChain([]backend.QueenEngineEntry{{Engine: engine, Enabled: true}})
	}
	return h
}

// A codex queen runs on the person's plan: the panel gets the plan's limits
// from the newest session log, and a window that has rolled over reads zero.
func TestCodexSubscriptionLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Unix(1_800_000_000, 0)
	dir := filepath.Join(home, ".codex", "sessions", "2026", "11", "25")
	writeLog(t, filepath.Join(dir, "rollout-2026-11-25T01-00-00-abc.jsonl"),
		`{"timestamp":"2026-11-25T01:00:00Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":null}}`,
		`{"timestamp":"2026-11-25T01:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"primary":{"used_percent":23.0,"window_minutes":300,"resets_at":1800003600},"secondary":{"used_percent":41.0,"window_minutes":10080,"resets_at":1799000000}}}}`,
		`{"timestamp":"2026-11-25T01:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"x"}}`,
	)
	u := engineUsageFor(hiveWith("codex"), "", now)
	if u.Mode != "subscription" || u.CLI != "codex" || len(u.Windows) != 2 {
		t.Fatalf("usage: %+v", u)
	}
	if w := u.Windows[0]; w.Label != "5h" || w.Percent != 23 || w.ResetsAt != 1800003600 {
		t.Fatalf("5h window: %+v", w)
	}
	// The weekly window's reset time is in the past: it rolled over.
	if w := u.Windows[1]; w.Label != "week" || w.Percent != 0 || w.ResetsAt != 0 {
		t.Fatalf("weekly window: %+v", w)
	}
}

// Claude Code keeps no limits on disk; its token count for today comes from
// the log, each message once however many lines it was written in.
func TestClaudeSubscriptionTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	old := now.AddDate(0, 0, -3).UTC().Format(time.RFC3339)
	writeLog(t, filepath.Join(home, ".claude", "projects", "-p", "s.jsonl"),
		`{"type":"assistant","timestamp":"`+ts+`","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":9999}}}`,
		`{"type":"assistant","timestamp":"`+ts+`","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":100,"cache_read_input_tokens":9999}}}`,
		`{"type":"assistant","timestamp":"`+ts+`","message":{"id":"m2","usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"type":"assistant","timestamp":"`+old+`","message":{"id":"m3","usage":{"input_tokens":500,"output_tokens":500}}}`,
	)
	u := engineUsageFor(hiveWith("claude"), "", now)
	if u.Mode != "subscription" || u.CLI != "claude" || u.Tokens != 133 || len(u.Windows) != 0 {
		t.Fatalf("usage: %+v", u)
	}
}

// SuperLeo's own model, a stand-in answer, and engines with no plan figures
// are all the meter's business.
func TestOwnKeyUsesTheMeter(t *testing.T) {
	for _, c := range []struct{ engine, now string }{
		{"", ""}, {"codex", queenNowSuperLeo}, {"pi", ""},
	} {
		if u := engineUsageFor(hiveWith(c.engine), c.now, time.Now()); u.Mode != "key" {
			t.Fatalf("%+v: %+v", c, u)
		}
	}
	if u := engineUsageFor(hiveWith("codex.dell"), "", time.Now()); u.Mode != "subscription" || !u.Remote {
		t.Fatalf("remote: %+v", u)
	}
}

// A day's Claude Code log runs past the tail other readers stop at; every
// turn in it counts, and lines written after a read are added on the next.
func TestClaudeTokensReadWholeLogThenOnlyNewLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	ts := now.UTC().Format(time.RFC3339)
	turn := func(id string, n int) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","usage":{"input_tokens":` + strconv.Itoa(n) + `,"output_tokens":0}}}`
	}
	pad := `{"type":"user","message":{"content":"` + strings.Repeat("x", 1<<20) + `"}}`
	lines := []string{turn("first", 100)}
	for i := 0; i < 6; i++ {
		lines = append(lines, pad)
	}
	lines = append(lines, turn("last", 10))
	path := filepath.Join(home, ".claude", "projects", "-p", "s.jsonl")
	writeLog(t, path, lines...)
	if got := claudeTokensToday(now); got != 110 {
		t.Fatalf("whole log: %d, want 110", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(turn("last", 15) + "\n" + turn("more", 5))
	f.Close()
	if got := claudeTokensToday(now); got != 115 {
		t.Fatalf("after an append with a line still being written: %d, want 115", got)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n")
	f.Close()
	if got := claudeTokensToday(now); got != 120 {
		t.Fatalf("once the line is done: %d, want 120", got)
	}
}
