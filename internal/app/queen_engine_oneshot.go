package app

// With no model of its own, SuperLeo thinks through the queen's CLI engine
// for everything that is not a chat turn on screen: a schedule firing, an
// agent run for a channel or a dashboard, a long run, the sentence
// ScheduleFromText reads, an install read from a URL. The service is handed
// engineOneShot (backend.Service.SetEngine) and runs those on it. A chat turn
// takes routeToEngine, which streams to the window; this is the same engine,
// kit and runner (startCLIRun) with the answer waited for instead.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/liliang-cn/superleo/internal/backend"
)

// noModelEngines are the queen engines a turn may go to when this app has no
// model of its own: those switched on, in order, SuperLeo's own agent left
// out. Empty on a hive worker, which has no queen to think for.
func noModelEngines(s *backend.Settings) []string {
	if s == nil || s.Hive.Role == backend.HiveRoleWorker {
		return nil
	}
	var out []string
	for _, e := range s.Hive.QueenEnginesOn() {
		if e != backend.QueenEngineSuperLeo {
			out = append(out, e)
		}
	}
	return out
}

// canThink is whether anything here can answer: the app's own model, or a
// CLI engine switched on.
func (a *App) canThink() bool {
	if a.hasModel() {
		return true
	}
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return len(noModelEngines(s)) > 0
}

// engineOneShot runs one prompt on the queen's engines, each that cannot start
// handing it to the next, and returns the answer. session is the conversation
// it belongs to, "" for none.
func (a *App) engineOneShot(ctx context.Context, session, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("the prompt is empty")
	}
	a.mu.Lock()
	var s backend.Settings
	if a.settings != nil {
		s = *a.settings
	}
	a.mu.Unlock()
	engines := noModelEngines(&s)
	if len(engines) == 0 {
		return "", backend.ErrNoModel
	}
	var reasons []string
	for _, eng := range engines {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		out, err, handOn := a.engineOnce(ctx, &s, eng, session, prompt)
		if err == nil {
			return out, nil
		}
		if !handOn {
			return "", err
		}
		slog.Warn("queen engine: could not take a background turn", "engine", eng, "session", session, "reason", err)
		reasons = append(reasons, engineLabel(eng)+": "+err.Error())
	}
	return "", fmt.Errorf("no engine could answer: %s", strings.Join(reasons, "; "))
}

// engineOnce is one engine's run of the prompt. handOn is whether the next
// engine may be tried: it never got going.
func (a *App) engineOnce(ctx context.Context, s *backend.Settings, eng, session, prompt string) (answer string, err error, handOn bool) {
	kit, kerr := a.queenKitFor(eng, s)
	if kerr != nil {
		return "", kerr, true
	}
	done := make(chan CLIRun, 1)
	o := cliStart{Agent: eng, Prompt: prompt, Ask: !s.ExternalAgents.Unattended, Chat: session, Queen: kit, Origin: "background"}
	o.Watch = func(CLIRunEvent) {}
	o.Done = func(r CLIRun) {
		select {
		case done <- r:
		default:
		}
	}
	run, serr := queenStartRun(a, o)
	if serr != nil {
		return "", serr, true
	}
	select {
	case <-ctx.Done():
		a.CancelCLIRun(run.ID)
		return "", ctx.Err(), false
	case r := <-done:
		switch r.State {
		case "cancelled":
			return "", context.Canceled, false
		case "failed":
			return "", fmt.Errorf("%s did not finish: %s", engineLabel(eng), firstNonEmpty(r.Error, "it failed")), r.Tools == 0 && strings.TrimSpace(r.Summary) == ""
		}
		out := strings.TrimSpace(r.Summary)
		if out == "" {
			return "", fmt.Errorf("%s answered nothing", engineLabel(eng)), true
		}
		return out, nil, false
	}
}

// pictureNote tells an engine where the pictures that came with a turn are:
// the CLI has no attachment of its own here, but reads a file by its path.
func pictureNote(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			b.WriteString("\n- ")
			b.WriteString(p)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\n(Pictures came with this message; open and look at these files:" + b.String() + ")"
}

// askEngineTimeout bounds one AskEngine call: an engine starting up and
// reading a prompt, not a working session.
const askEngineTimeout = 3 * time.Minute

// AskEngine answers one prompt with the queen's CLI engine, for a caller with
// no model of its own to ask: the watchdog's drift judge. With a model of its
// own, the engine is still what answers; callers that have a model use it.
//
//	POST /api/rpc/AskEngine  ["…question…"]  →  "…answer…"
func (a *App) AskEngine(prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askEngineTimeout)
	defer cancel()
	return a.engineOneShot(ctx, "", prompt)
}
