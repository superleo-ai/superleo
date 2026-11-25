package backend

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func noModelSettings(t *testing.T, engine string) *Settings {
	home := t.TempDir()
	t.Setenv("SUPERLEO_HOME", home)
	t.Setenv("SUPERLEO_NO_BROWSER", "1")
	s := &Settings{WorkspaceDir: home + "/workspace", MaxRounds: 3, Headless: true}
	if engine != "" {
		s.Hive.PromoteQueenEngine(engine, false)
	}
	return s
}

func TestServiceBuildsWithoutAModelWhenAnEngineThinks(t *testing.T) {
	svc, err := NewService(noModelSettings(t, "codex"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if svc.HasModel() {
		t.Fatal("a service with no key says it has a model")
	}
	if svc.ToolGate() == nil || svc.Notices() == nil {
		t.Fatal("the gate and notices are what a keyless service is for")
	}
	if _, err := svc.StreamTurn(context.Background(), "s", "hi", nil, nil); !errors.Is(err, ErrNoModel) {
		t.Fatalf("a turn on its own model: %v", err)
	}
	if _, err := svc.StreamLong(context.Background(), "goal", LongRunOptions{}, nil); !errors.Is(err, ErrNoModel) {
		t.Fatalf("a long run on its own model: %v", err)
	}
}

func TestServiceNeedsAModelWhenNoEngineThinks(t *testing.T) {
	if _, err := NewService(noModelSettings(t, "")); err == nil {
		t.Fatal("no key and no engine built a service")
	}
}

// With the engine set, a keyless service runs an agent turn through it: the
// engine's answer is the turn's answer, and a one-shot JSON read is parsed.
func TestKeylessServiceThinksThroughTheEngine(t *testing.T) {
	svc, err := NewService(noModelSettings(t, "codex"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if svc.CanThink() {
		t.Fatal("thinks before an engine is set")
	}
	if _, err := svc.Stream(context.Background(), "s1", "hi", nil, nil); !errors.Is(err, ErrNoModel) {
		t.Fatalf("no engine: %v", err)
	}
	var asked []string
	svc.SetEngine(func(_ context.Context, session, prompt string) (string, error) {
		asked = append(asked, prompt)
		if strings.Contains(prompt, "JSON") {
			return "```json\n{\"action\":\"give_up\"}\n```", nil
		}
		return "engine says hi", nil
	})
	if !svc.CanThink() || svc.HasModel() {
		t.Fatal("CanThink/HasModel wrong")
	}
	out, err := svc.Stream(context.Background(), "s1", "say hi", nil, nil)
	if err != nil || !strings.Contains(out, "engine says hi") {
		t.Fatalf("%q %v", out, err)
	}
	if len(asked) == 0 || !strings.Contains(asked[0], "say hi") {
		t.Fatalf("asked %q", asked)
	}
	var got struct{ Action string }
	if err := svc.extractJSON(context.Background(), "decide", mcpActionSchema(), &got); err != nil || got.Action != "give_up" {
		t.Fatalf("%+v %v", got, err)
	}
	rep, err := svc.StreamLong(context.Background(), "do a long thing", LongRunOptions{TaskID: "t1"}, nil)
	if err != nil || !rep.Done || !strings.Contains(rep.Text, "engine says hi") {
		t.Fatalf("%+v %v", rep, err)
	}
}
