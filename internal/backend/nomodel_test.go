package backend

import (
	"context"
	"errors"
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
