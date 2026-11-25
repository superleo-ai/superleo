package app

import (
	"context"
	"strings"
	"testing"

	"github.com/liliang-cn/superleo/internal/backend"
)

// With no model of its own, a turn of any origin goes to the first engine
// switched on; SuperLeo's own agent is never one of them.
func TestNoModelEveryTurnGoesToTheEngine(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.ownTurnFn = nil // no model of its own
	h.a.settings.Hive.SetQueenChain(on("superleo", "claude", "codex"))
	for _, session := range []string{"scheduled", "hive-mail:queen", "hive:order-1", "dashboard:abc", "tile-k2-x1", "c1", ""} {
		if got := h.a.queenEngineFor(session, nil); got != "claude" {
			t.Errorf("%q went to %q, want claude", session, got)
		}
	}
	// A picture goes too, which with a model stays with SuperLeo's own agent.
	if got := h.a.queenEngineFor("c1", []string{"/tmp/x.png"}); got != "claude" {
		t.Errorf("picture went to %q", got)
	}
}

// With a model of its own, behaviour is as it was: only a person's turn goes
// to the engine, and the own flag and pictures keep it away.
func TestWithAModelOnlyAPersonsTurnGoesToTheEngine(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0) // ownTurnFn stands for a model
	h.a.settings.Hive.SetQueenChain(on("claude", "superleo"))
	if got := h.a.queenEngineFor("c1", nil); got != "claude" {
		t.Errorf("person went to %q", got)
	}
	for _, session := range []string{"scheduled", "hive-mail:queen", "hive:order-1"} {
		if got := h.a.queenEngineFor(session, nil); got != "" {
			t.Errorf("%q went to %q, want the own model", session, got)
		}
	}
	if got := h.a.queenEngineFor("c1", []string{"/tmp/x.png"}); got != "" {
		t.Errorf("picture went to %q, want the own model", got)
	}
	// SuperLeo first stays with SuperLeo.
	h.a.settings.Hive.SetQueenChain(on("superleo", "claude"))
	if got := h.a.queenEngineFor("c1", nil); got != "" {
		t.Errorf("superleo first went to %q", got)
	}
}

// A turn said in a call (own) still goes to the engine when there is no model.
func TestNoModelOwnTurnStillGoesToTheEngine(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.ownTurnFn = nil
	h.a.settings.Hive.SetQueenChain(on("claude"))
	f := &fakeEngines{}
	prev := queenStartRun
	queenStartRun = f.start
	t.Cleanup(func() { queenStartRun = prev })
	id := h.a.sendChat("hive-mail:queen", "mail", nil, true)
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatal(name)
	}
	if len(f.tried()) != 1 {
		t.Fatalf("tried %v", f.tried())
	}
}

func TestEngineOneShotAnswersAndHandsOn(t *testing.T) {
	h, f := chainHarness(t, on("superleo", "claude", "codex"), map[string]string{"claude": "cannot start"})
	h.a.ownTurnFn = nil
	out, err := h.a.engineOneShot(context.Background(), "scheduled", "say hi")
	if err != nil || out != "answer from codex" {
		t.Fatalf("%q %v", out, err)
	}
	if got := strings.Join(f.tried(), ","); got != "claude,codex" {
		t.Fatalf("tried %s", got)
	}
}

func TestEngineOneShotWithNoEngineSaysNoModel(t *testing.T) {
	h, _ := chainHarness(t, on("superleo"), nil)
	h.a.ownTurnFn = nil
	if _, err := h.a.engineOneShot(context.Background(), "", "x"); err != backend.ErrNoModel {
		t.Fatalf("%v", err)
	}
	if h.a.canThink() {
		t.Fatal("can think with only SuperLeo and no model")
	}
}

// A list saved with no model of its own keeps SuperLeo's agent switched off.
func TestSuperLeoIsSwitchedOffWithoutAModel(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.LLMKey, h.a.settings.LLMBaseURL, h.a.settings.LLMModel = "", "", ""
	h.a.settings.Hive.SetQueenChain(on("claude"))
	got, err := h.a.SetQueenEngineList(on("claude", "superleo"), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Engines {
		if e.ID == backend.QueenEngineSuperLeo && e.Enabled {
			t.Fatal("SuperLeo is switched on with no model")
		}
	}
}

func TestPictureNote(t *testing.T) {
	if pictureNote(nil) != "" {
		t.Fatal("note without pictures")
	}
	if n := pictureNote([]string{"/a/b.png"}); !strings.Contains(n, "/a/b.png") {
		t.Fatal(n)
	}
}
