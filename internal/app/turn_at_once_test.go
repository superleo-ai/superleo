package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

// busyWith tracks a turn running in a conversation, the way a call turn on the
// queen is, and returns what ends it.
func busyWith(a *App, requestID, sessionID string) func() {
	_, cancel := context.WithCancel(context.Background())
	a.trackRun(requestID, cancel)
	a.runSession(requestID, sessionID)
	return func() { cancel(); a.untrackRun(requestID) }
}

// The live case: a call turn running on the queen, and a typed message in the
// same conversation. It is answered now, by the queen, while the call turn is
// still going — not after it, and never by a worker, which has no agent link
// and told the person the Mac was not connected.
func TestATypedTurnIsAnsweredWhileTheQueenIsStillBusy(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.QueenEngine = ""
	const conv = "hive-console-web"
	end := busyWith(h.a, "call-1", conv)
	defer end()

	id := h.a.SendChat(conv, "顺便把 Mac 上的 opencode 也更新一下", nil)
	if name, _ := h.ev.wait(t, id); name != "chat:done" {
		t.Fatalf("ended with %s", name)
	}
	if got := h.owned(); len(got) != 1 || got[0].session != conv || got[0].standIn != "" {
		t.Fatalf("not the queen's own turn: %v", got)
	}
	if !h.a.sessionBusy(conv) {
		t.Fatal("the call turn ended; the test no longer shows the overlap")
	}
}

// With a queen engine whose session is busy with the first turn, the engine
// answers this one too, at once, in a session of its own — not SuperLeo's
// model under a note saying the engine could not take it.
func TestABusyQueenEngineAnswersInASessionOfItsOwn(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	const conv = "hive-console-web"
	end := busyWith(h.a, "call-1", conv)
	defer end()

	id := h.a.SendChat(conv, "现在 hive 里有哪些成员？", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["engine"] != "claude" {
		t.Fatalf("%s %v", name, done)
	}
	if got := h.owned(); len(got) != 0 {
		t.Fatalf("SuperLeo's model stood in: %v", got)
	}
}

// Several asks in a busy conversation, a call turn among them, all start at
// once, in the order they were asked.
func TestEveryTurnInABusyConversationStartsAtOnce(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	h.a.settings.Hive.QueenEngine = ""
	const conv = "c1"
	end := busyWith(h.a, "first", conv)
	defer end()

	ids := []string{
		h.a.SendChat(conv, "two", nil),
		h.a.SendChat(conv, "three", nil),
		h.a.SendCallChat(conv, "four", nil),
	}
	for _, id := range ids {
		if name, _ := h.ev.wait(t, id); name != "chat:done" {
			t.Fatalf("%s ended with %s", id, name)
		}
	}
	got := h.owned()
	if len(got) != 3 || got[0].message != "two" || got[1].message != "three" || got[2].message != "four" {
		t.Fatalf("order: %v", got)
	}
}

// With the conversation free, a person's turn goes to the queen engine.
func TestAFreeConversationGoesToTheQueenEngine(t *testing.T) {
	h := newEngineHarness(t, fakeClaudeTurn, 0)
	end := busyWith(h.a, "elsewhere", "c2")
	defer end()

	id := h.a.SendChat("hive-console-web", "hi", nil)
	name, done := h.ev.wait(t, id)
	if name != "chat:done" || done["engine"] != "claude" {
		t.Fatalf("%s %v", name, done)
	}
	if len(h.owned()) != 0 {
		t.Fatalf("SuperLeo's model answered instead: %v", h.owned())
	}
}

// A turn started while another engine turn in the conversation is still
// answering hears that question: "明天呢？" means nothing without "今天有什么日程？".
func TestAnEngineTurnHearsTheQuestionStillBeingAnswered(t *testing.T) {
	a := &App{}
	now := time.Now()
	a.pendingAsks.Store("first", pendingAsk{session: "c", message: "今天有什么日程？", at: now.Add(-2 * time.Second)})
	a.pendingAsks.Store("elsewhere", pendingAsk{session: "other", message: "不相干", at: now})
	a.pendingAsks.Store("second", pendingAsk{session: "c", message: "明天呢？", at: now})
	got := a.stillAsked("c", "second")
	if len(got) != 1 || got[0].Content != "今天有什么日程？" || got[0].Role != "user" {
		t.Fatalf("handed %v", got)
	}
	if !strings.Contains(handoffText(got), "Person: 今天有什么日程？") {
		t.Fatalf("not in the handoff: %q", handoffText(got))
	}
}
