package app

// Implement, then have the other agent review it.
//
// One CLI implements; the OTHER one reads what changed, read-only, and says
// APPROVE or CHANGES with findings; on CHANGES the findings go back to the
// implementer on its own session, and the reviewer looks again, up to a cap.
// Every step is an ordinary CLI run, so it also shows in the Coding list.
//
// What "what changed" means is fixed when the implement run starts: a snapshot
// of the whole working tree (tracked, untracked, not ignored) made through a
// throwaway git index, so the repository's own index, stash and status are
// untouched, and edits that were already there before the run are not counted
// as its work. The review reads base snapshot -> current snapshot.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

// ReviewFinding is one problem the reviewer named.
type ReviewFinding struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text"`
}

// DiffStat is how much a change touched.
type DiffStat struct {
	Files int      `json:"files"`
	Add   int      `json:"add"`
	Del   int      `json:"del"`
	Paths []string `json:"paths,omitempty"`
}

// ReviewStep is one run in the track: the implementation, a review, or a fix.
type ReviewStep struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // implement, review, fix
	Cycle int    `json:"cycle"`
	Agent string `json:"agent"`
	Run   string `json:"run,omitempty"`
	// State is pending, running, done, failed or cancelled.
	State    string          `json:"state"`
	Started  time.Time       `json:"started"`
	Ended    *time.Time      `json:"ended,omitempty"`
	In       int             `json:"in"`
	Out      int             `json:"out"`
	CostUSD  float64         `json:"costUsd"`
	Text     string          `json:"text,omitempty"`
	Verdict  string          `json:"verdict,omitempty"` // APPROVE or CHANGES, on a review
	Findings []ReviewFinding `json:"findings,omitempty"`
	Diff     *DiffStat       `json:"diff,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// ImplementReview is one implement -> cross-review track.
type ImplementReview struct {
	ID          string `json:"id"`
	Cwd         string `json:"cwd"`
	Prompt      string `json:"prompt"`
	Implementer string `json:"implementer"`
	Reviewer    string `json:"reviewer"`
	Cycles      int    `json:"cycles"`
	// Debate and FromRun say what started it: a debate's conclusion, or a
	// finished run whose changes are reviewed.
	Debate  string `json:"debate,omitempty"`
	FromRun string `json:"fromRun,omitempty"`
	// Base is the working-tree snapshot (a git tree) before the change, Head
	// the one the last review read; BaseHead is the commit at the start.
	Base     string `json:"base,omitempty"`
	BaseHead string `json:"baseHead,omitempty"`
	Head     string `json:"head,omitempty"`
	// State is running, approved, changes (changes left), failed or cancelled.
	State   string       `json:"state"`
	Steps   []ReviewStep `json:"steps"`
	Started time.Time    `json:"started"`
	Ended   *time.Time   `json:"ended,omitempty"`
	Error   string       `json:"error,omitempty"`
}

// ImplementReviewRequest starts a track. Give Prompt to have Implementer do
// it first, or FromRun to review what a finished run already changed.
type ImplementReviewRequest struct {
	Cwd         string `json:"cwd"`
	Prompt      string `json:"prompt"`
	Implementer string `json:"implementer"`
	Reviewer    string `json:"reviewer"`
	// Cycles is how many times the findings go back for a fix: 0 reviews
	// only. Negative means the default, 2.
	Cycles  int    `json:"cycles"`
	Debate  string `json:"debate"`
	FromRun string `json:"fromRun"`
}

// ---- git: what changed ----

func gitRun(dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(firstNonEmpty(errb.String(), err.Error())))
	}
	return out.String(), nil
}

// gitHead is the commit dir is at, "" outside a repository or with none yet.
func gitHead(dir string) string {
	out, err := gitRun(dir, nil, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitSnapshot is a git tree of dir's whole working tree, untracked files
// included and ignored ones not, made in a throwaway index: nothing in the
// repository's own index or status moves.
func gitSnapshot(dir string) (string, error) {
	if _, err := gitRun(dir, nil, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", errors.New("not a git repository")
	}
	tmp, err := os.MkdirTemp("", "superleo-snap-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if gitHead(dir) != "" {
		if _, err := gitRun(dir, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	if _, err := gitRun(dir, env, "add", "-A", "--", ":/"); err != nil {
		return "", err
	}
	out, err := gitRun(dir, env, "write-tree")
	return strings.TrimSpace(out), err
}

// gitChange is the diff between two snapshots: the text and how much it is.
func gitChange(dir, base, head string) (string, DiffStat, error) {
	var st DiffStat
	num, err := gitRun(dir, nil, "diff", "--numstat", "--no-color", "--no-ext-diff", base, head)
	if err != nil {
		return "", st, err
	}
	st = parseNumstat(num)
	text, err := gitRun(dir, nil, "diff", "--no-color", "--no-ext-diff", base, head)
	return text, st, err
}

func parseNumstat(s string) DiffStat {
	var st DiffStat
	for _, l := range strings.Split(s, "\n") {
		f := strings.Split(strings.TrimRight(l, "\r"), "\t")
		if len(f) < 3 {
			continue
		}
		st.Files++
		if n, err := strconv.Atoi(f[0]); err == nil {
			st.Add += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			st.Del += n
		}
		st.Paths = append(st.Paths, f[2])
	}
	return st
}

// ---- read-only ----

// cliReadOnlyArgs makes req unable to change files, as far as the CLI can be
// made to: claude runs in plan mode with its editing tools disallowed, codex in
// its read-only sandbox. Any other CLI has no switch for it here, and its
// prompt only asks.
func cliReadOnlyArgs(req *agentexec.Request, agent string) {
	switch agent {
	case "claude":
		req.PermissionMode = agentexec.PermissionDefault
		// --disallowedTools is variadic: as a separate word it would swallow
		// the prompt, which comes next in argv, as one more tool name.
		req.ExtraArgs = append(req.ExtraArgs, "--permission-mode", "plan",
			"--disallowedTools=Edit,Write,MultiEdit,NotebookEdit")
	case "codex":
		req.PermissionMode = agentexec.PermissionDefault
		req.Sandbox = true
		// -c rather than --sandbox: `codex exec resume` takes no --sandbox.
		req.ExtraArgs = append(req.ExtraArgs, "--skip-git-repo-check", "-c", `sandbox_mode="read-only"`)
	}
}

// cliCanBeReadOnly is whether cliReadOnlyArgs can enforce it for agent.
func cliCanBeReadOnly(agent string) bool { return agent == "claude" || agent == "codex" }

// readOnlyNote is what a debate or review prompt says about not editing: an
// order for every CLI, and for one that cannot be held to it, a warning.
func readOnlyNote(agent string) string {
	n := "You are working READ-ONLY. Do not edit, create, move or delete any file, and do not run any command that changes the repository or the machine."
	if !cliCanBeReadOnly(agent) {
		n += " (This tool cannot be locked read-only, so this instruction is the only guard: obey it strictly.)"
	}
	return n
}

// ---- verdict ----

var (
	verdictRe = regexp.MustCompile(`(?im)^[\s>*_\x60#-]*VERDICT\s*[:：]\s*[*_\x60]*\s*(APPROVED?|CHANGES(?:[ _]REQUESTED)?|REJECT(?:ED)?)`)
	bulletRe  = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+(.*)$`)
	findingRe = regexp.MustCompile(`^([^\s:]+?)(?::(\d+)(?:\s*[-–]\s*\d+)?)?\s*(?:—|–|--|-|:)\s+(.+)$`)
)

// parseVerdict reads the reviewer's last "VERDICT: …" and the "- file:line —
// why" lines after it. verdict is "" when there is none.
func parseVerdict(text string) (verdict string, findings []ReviewFinding) {
	ms := verdictRe.FindAllStringSubmatchIndex(text, -1)
	if len(ms) == 0 {
		return "", nil
	}
	m := ms[len(ms)-1]
	word := strings.ToUpper(text[m[2]:m[3]])
	if strings.HasPrefix(word, "APPROVE") {
		verdict = "APPROVE"
	} else {
		verdict = "CHANGES"
	}
	for _, line := range strings.Split(text[m[1]:], "\n") {
		b := bulletRe.FindStringSubmatch(line)
		if b == nil {
			continue
		}
		body := strings.TrimSpace(strings.Trim(b[1], "*_ "))
		if body == "" {
			continue
		}
		f := ReviewFinding{Text: body}
		clean := strings.NewReplacer("`", "", "**", "").Replace(body)
		if fm := findingRe.FindStringSubmatch(clean); fm != nil && strings.ContainsAny(fm[1], "./") {
			f.File, f.Text = fm[1], strings.TrimSpace(fm[3])
			if n, err := strconv.Atoi(fm[2]); err == nil {
				f.Line = n
			}
		}
		findings = append(findings, f)
	}
	if verdict == "APPROVE" {
		return verdict, nil
	}
	return verdict, findings
}

// ---- the track ----

const reviewDiffInPrompt = 60 << 10

func (a *App) reviewOtherAgent(implementer string) string {
	if implementer == "codex" {
		return "claude"
	}
	return "codex"
}

// StartImplementReview starts a track in the background.
func (a *App) StartImplementReview(req ImplementReviewRequest) (ImplementReview, error) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Implementer = strings.TrimSpace(req.Implementer)
	req.Reviewer = strings.TrimSpace(req.Reviewer)
	if req.Cycles < 0 {
		req.Cycles = 2
	}
	if req.Cycles > 3 {
		req.Cycles = 3
	}
	var from *CLIRun
	if req.FromRun != "" {
		r, err := a.CLIRunDetail(req.FromRun)
		if err != nil {
			return ImplementReview{}, err
		}
		// A review covers the whole conversation: from where its first run
		// began, with what its newest turn reported.
		cs := a.cliStore()
		cs.mu.Lock()
		var oldest, newest *CLIRun
		for _, x := range cs.runs {
			if x.Thread != r.Thread {
				continue
			}
			if oldest == nil || x.Started.Before(oldest.Started) {
				oldest = x
			}
			if newest == nil || x.Started.After(newest.Started) {
				newest = x
			}
		}
		if oldest != nil {
			r.Base, r.Summary, r.State = oldest.Base, newest.Summary, newest.State
		}
		cs.mu.Unlock()
		if r.State == "running" {
			return ImplementReview{}, errors.New("that run is still going")
		}
		from = &r
		if req.Cwd == "" {
			req.Cwd = r.Cwd
		}
		if req.Implementer == "" {
			req.Implementer = r.Agent
		}
		if req.Prompt == "" {
			req.Prompt = r.Prompt
		}
	}
	if req.Implementer == "" {
		req.Implementer = "claude"
	}
	if req.Reviewer == "" {
		req.Reviewer = a.reviewOtherAgent(req.Implementer)
	}
	if from == nil && req.Prompt == "" {
		return ImplementReview{}, errors.New("there is nothing to implement")
	}
	for _, ag := range []string{req.Implementer, req.Reviewer} {
		if _, _, ok := splitRemoteCLI(ag); ok {
			return ImplementReview{}, fmt.Errorf("%s: a review runs on this machine's CLIs", ag)
		}
		if a.stepRun == nil {
			if _, _, err := a.cliProvider(ag); err != nil {
				return ImplementReview{}, err
			}
		}
	}
	dir, err := a.reviewCwd(req.Cwd, req.Implementer)
	if err != nil {
		return ImplementReview{}, err
	}
	rv := &ImplementReview{ID: uuid.NewString(), Cwd: dir, Prompt: req.Prompt, Implementer: req.Implementer,
		Reviewer: req.Reviewer, Cycles: req.Cycles, Debate: req.Debate, FromRun: req.FromRun,
		State: "running", Started: time.Now(), Steps: []ReviewStep{}}
	if from != nil {
		rv.BaseHead = from.Base
	}
	s := a.debateStoreOf()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.reviews[rv.ID] = rv
	s.cancels[rv.ID] = cancel
	if d := s.debates[req.Debate]; d != nil {
		d.Reviews = append(d.Reviews, rv.ID)
	}
	out := *rv
	if d := s.debates[req.Debate]; d != nil {
		s.saveLocked()
	}
	s.mu.Unlock()
	a.pushReview(rv)
	if req.Debate != "" {
		a.pushDebateID(req.Debate)
	}
	go a.runReview(ctx, rv, from)
	return out, nil
}

func (a *App) reviewCwd(cwd, agent string) (string, error) {
	var ext backend.ExternalAgents
	if a.stepRun == nil {
		var err error
		if _, ext, err = a.cliProvider(agent); err != nil {
			return "", err
		}
	}
	return a.cliCwd(cwd, ext.Roots)
}

// ImplementReviews lists tracks newest first.
func (a *App) ImplementReviews() []ImplementReview {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []ImplementReview{}
	for _, r := range s.reviews {
		c := *r
		c.Steps = nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// ImplementReviewDetail is one track with its steps.
func (a *App) ImplementReviewDetail(id string) (ImplementReview, error) {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.reviews[id]
	if r == nil {
		return ImplementReview{}, fmt.Errorf("no review %s", id)
	}
	c := *r
	c.Steps = append([]ReviewStep{}, r.Steps...)
	return c, nil
}

// CancelImplementReview stops a track and the run it is on.
func (a *App) CancelImplementReview(id string) string {
	s := a.debateStoreOf()
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return "that review is not running"
	}
	cancel()
	return "ok"
}

// ImplementReviewDiff is the change the last review read: base -> head.
func (a *App) ImplementReviewDiff(id string) (string, error) {
	rv, err := a.ImplementReviewDetail(id)
	if err != nil {
		return "", err
	}
	head := rv.Head
	if rv.Base == "" {
		return "", errors.New("no base was recorded for this review")
	}
	if head == "" {
		if head, err = gitSnapshot(rv.Cwd); err != nil {
			return "", err
		}
	}
	text, _, err := gitChange(rv.Cwd, rv.Base, head)
	return text, err
}

// mutateReview changes a track under the lock and pushes it to the pages.
func (a *App) mutateReview(rv *ImplementReview, f func(*ImplementReview)) {
	s := a.debateStoreOf()
	s.mu.Lock()
	f(rv)
	s.saveLocked()
	s.mu.Unlock()
	a.pushReview(rv)
}

func (a *App) pushReview(rv *ImplementReview) {
	s := a.debateStoreOf()
	s.mu.Lock()
	raw, _ := json.Marshal(rv)
	s.mu.Unlock()
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		a.emit("review:update", m)
	}
}

// beginStep adds a running step and returns its index.
func (a *App) beginReviewStep(rv *ImplementReview, kind string, cycle int, agent string) int {
	var i int
	a.mutateReview(rv, func(r *ImplementReview) {
		r.Steps = append(r.Steps, ReviewStep{ID: uuid.NewString(), Kind: kind, Cycle: cycle, Agent: agent, State: "running", Started: time.Now()})
		i = len(r.Steps) - 1
	})
	return i
}

func (a *App) endReviewStep(rv *ImplementReview, i int, out stepOut, err error, extra func(*ReviewStep)) {
	a.mutateReview(rv, func(r *ImplementReview) {
		st := &r.Steps[i]
		now := time.Now()
		st.Ended = &now
		st.Run = firstNonEmpty(out.Run.ID, st.Run)
		st.In, st.Out, st.CostUSD = out.Run.In, out.Run.Out, out.Run.CostUSD
		st.Text = out.Answer
		switch {
		case err != nil && errors.Is(err, context.Canceled):
			st.State, st.Error = "cancelled", "stopped"
		case err != nil:
			st.State, st.Error = "failed", err.Error()
		case out.Run.State == "cancelled":
			st.State, st.Error = "cancelled", firstNonEmpty(out.Run.Error, "stopped")
		case out.Run.State == "failed":
			st.State, st.Error = "failed", firstNonEmpty(out.Run.Error, "it failed")
		default:
			st.State = "done"
		}
		if extra != nil && st.State == "done" {
			extra(st)
		}
	})
}

func (a *App) finishReview(rv *ImplementReview, state, msg string) {
	a.mutateReview(rv, func(r *ImplementReview) {
		now := time.Now()
		r.Ended, r.State, r.Error = &now, state, msg
	})
	s := a.debateStoreOf()
	s.mu.Lock()
	if c := s.cancels[rv.ID]; c != nil {
		c()
	}
	delete(s.cancels, rv.ID)
	s.mu.Unlock()
}

func (a *App) onStartStep(rv *ImplementReview, i int) func(CLIRun) {
	return func(r CLIRun) {
		a.mutateReview(rv, func(x *ImplementReview) { x.Steps[i].Run = r.ID })
	}
}

// runReview is the track itself.
func (a *App) runReview(ctx context.Context, rv *ImplementReview, from *CLIRun) {
	// Where the implementer's session is, to send fixes to: its first run,
	// or the finished run being reviewed.
	implRun := ""
	implAnswer := ""
	if from != nil {
		implRun = from.ID
		implAnswer = firstNonEmpty(from.Summary, a.lastAnswer(from.ID))
	} else {
		base, err := gitSnapshot(rv.Cwd)
		if err != nil {
			a.finishReview(rv, "failed", "could not record the base: "+err.Error())
			return
		}
		a.mutateReview(rv, func(r *ImplementReview) { r.Base, r.BaseHead = base, gitHead(r.Cwd) })
		i := a.beginReviewStep(rv, "implement", 0, rv.Implementer)
		out, err := a.runStepOf(ctx, stepReq{Agent: rv.Implementer, Cwd: rv.Cwd, Prompt: rv.Prompt, Origin: "review",
			OnStart: a.onStartStep(rv, i)})
		a.endReviewStep(rv, i, out, err, nil)
		if ctx.Err() != nil {
			a.finishReview(rv, "cancelled", "stopped")
			return
		}
		if st := a.reviewStepAt(rv, i); st.State != "done" {
			a.finishReview(rv, "failed", "the implementation did not finish: "+st.Error)
			return
		}
		implRun, implAnswer = out.Run.ID, out.Answer
	}
	if rv.Base == "" {
		// Review of a finished run: from the commit it began at, or HEAD.
		baseHead := rv.BaseHead
		if baseHead == "" {
			baseHead = gitHead(rv.Cwd)
		}
		if baseHead == "" {
			a.finishReview(rv, "failed", "there is no commit to compare with")
			return
		}
		tree, err := gitRun(rv.Cwd, nil, "rev-parse", baseHead+"^{tree}")
		if err != nil {
			a.finishReview(rv, "failed", err.Error())
			return
		}
		a.mutateReview(rv, func(r *ImplementReview) { r.Base, r.BaseHead = strings.TrimSpace(tree), baseHead })
	}

	reviewRun := ""
	var last ReviewStep
	for cycle := 1; ; cycle++ {
		head, err := gitSnapshot(rv.Cwd)
		if err != nil {
			a.finishReview(rv, "failed", err.Error())
			return
		}
		text, stat, err := gitChange(rv.Cwd, rv.Base, head)
		if err != nil {
			a.finishReview(rv, "failed", err.Error())
			return
		}
		a.mutateReview(rv, func(r *ImplementReview) { r.Head = head })
		if strings.TrimSpace(text) == "" {
			i := a.beginReviewStep(rv, "review", cycle, rv.Reviewer)
			a.endReviewStep(rv, i, stepOut{}, errors.New("nothing changed since the base: there is nothing to review"), nil)
			a.finishReview(rv, "failed", "nothing changed")
			return
		}
		prompt := reviewerPrompt(rv, cycle, implAnswer, text, stat, head, reviewRun != "")
		i := a.beginReviewStep(rv, "review", cycle, rv.Reviewer)
		out, err := a.runStepOf(ctx, stepReq{Agent: rv.Reviewer, Cwd: rv.Cwd, Prompt: prompt, Prev: reviewRun, ReadOnly: true,
			Origin: "review", Fresh: reviewerPrompt(rv, cycle, implAnswer, text, stat, head, false), OnStart: a.onStartStep(rv, i)})
		if out.Run.ID != "" {
			reviewRun = out.Run.ID
		}
		a.endReviewStep(rv, i, out, err, func(st *ReviewStep) {
			st.Verdict, st.Findings = parseVerdict(out.Answer)
			s := stat
			st.Diff = &s
		})
		if ctx.Err() != nil {
			a.finishReview(rv, "cancelled", "stopped")
			return
		}
		last = a.reviewStepAt(rv, i)
		if last.State != "done" {
			a.finishReview(rv, "failed", "the review did not finish: "+last.Error)
			return
		}
		switch last.Verdict {
		case "APPROVE":
			a.finishReview(rv, "approved", "")
			return
		case "":
			a.finishReview(rv, "changes", "the reviewer gave no VERDICT line")
			return
		}
		if cycle > rv.Cycles {
			a.finishReview(rv, "changes", "")
			return
		}
		// Findings go back to the implementer, on its own session.
		fi := a.beginReviewStep(rv, "fix", cycle, rv.Implementer)
		fix, err := a.runStepOf(ctx, stepReq{Agent: rv.Implementer, Cwd: rv.Cwd, Prompt: fixPrompt(rv, last), Prev: implRun,
			Origin: "review", Fresh: fixPrompt(rv, last) + "\n\nThe task was:\n" + rv.Prompt, OnStart: a.onStartStep(rv, fi)})
		a.endReviewStep(rv, fi, fix, err, nil)
		if ctx.Err() != nil {
			a.finishReview(rv, "cancelled", "stopped")
			return
		}
		if st := a.reviewStepAt(rv, fi); st.State != "done" {
			a.finishReview(rv, "failed", "the fix did not finish: "+st.Error)
			return
		}
		if fix.Run.ID != "" {
			implRun = fix.Run.ID
		}
		implAnswer = fix.Answer
	}
}

func (a *App) reviewStepAt(rv *ImplementReview, i int) ReviewStep {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	return rv.Steps[i]
}

func reviewerPrompt(rv *ImplementReview, cycle int, report, diff string, stat DiffStat, head string, again bool) string {
	var b strings.Builder
	if again {
		fmt.Fprintf(&b, "%s\n\nThe implementer (%s) has addressed your findings. Review the change again.\n", readOnlyNote(rv.Reviewer), rv.Implementer)
	} else {
		fmt.Fprintf(&b, "You are reviewing a change that another engineer (%s) just made in this repository. %s\n\nThe task they were given:\n%s\n", rv.Implementer, readOnlyNote(rv.Reviewer), rv.Prompt)
		if strings.TrimSpace(report) != "" {
			fmt.Fprintf(&b, "\nTheir own report of what they did:\n%s\n", cliClip(report, 6<<10))
		}
	}
	fmt.Fprintf(&b, "\nThe change is the difference between two snapshots of the working tree. You can read it again with `git diff %s %s` (read-only). %d files, +%d -%d. Open the surrounding code whenever the diff alone is not enough.\n", rv.Base, head, stat.Files, stat.Add, stat.Del)
	d := diff
	note := ""
	if len(d) > reviewDiffInPrompt {
		d = d[:reviewDiffInPrompt]
		note = "\n(diff cut here; run the git command above for the rest)"
	}
	fmt.Fprintf(&b, "\n```diff\n%s\n```%s\n", d, note)
	b.WriteString("\nCheck correctness, regressions, missed edge cases, concurrency and error handling, and that the task is actually done. Do not praise; do not restate the diff.\n" +
		"Reply with the first line exactly `VERDICT: APPROVE` or `VERDICT: CHANGES`. After CHANGES, one line per problem, each as:\n- path/to/file.ext:LINE — what is wrong and what to do\nNo other bullets.")
	return b.String()
}

func fixPrompt(rv *ImplementReview, rev ReviewStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The reviewer (%s) found problems with your change:\n", rv.Reviewer)
	if len(rev.Findings) == 0 {
		b.WriteString(cliClip(rev.Text, 6<<10))
	}
	for _, f := range rev.Findings {
		switch {
		case f.File != "" && f.Line > 0:
			fmt.Fprintf(&b, "- %s:%d — %s\n", f.File, f.Line, f.Text)
		case f.File != "":
			fmt.Fprintf(&b, "- %s — %s\n", f.File, f.Text)
		default:
			fmt.Fprintf(&b, "- %s\n", f.Text)
		}
	}
	b.WriteString("\nFix each one that is right; if you think one is wrong, say why in your reply instead of changing the code. Make no unrelated changes. Reply with what you changed.")
	return b.String()
}

// ---- running one step ----

// stepReq is one CLI turn of a debate or a review.
type stepReq struct {
	Agent, Model, Cwd, Prompt string
	// Prev is a run whose session this turn continues; Fresh is the prompt to
	// start from scratch with if that session cannot be continued.
	Prev, Fresh string
	ReadOnly    bool
	Origin      string
	OnStart     func(CLIRun)
}

// stepOut is how a step ended: the run, and its answer.
type stepOut struct {
	Run    CLIRun
	Answer string
}

// stepRunner runs one step and waits for it. Tests swap it for a fake.
type stepRunner func(ctx context.Context, r stepReq) (stepOut, error)

func (a *App) runStepOf(ctx context.Context, r stepReq) (stepOut, error) {
	if a.stepRun != nil {
		return a.stepRun(ctx, r)
	}
	return a.runStepCLI(ctx, r)
}

// runStepCLI runs the step as a CLI run and waits for it to end; ctx ending
// stops the run.
func (a *App) runStepCLI(ctx context.Context, r stepReq) (stepOut, error) {
	if err := ctx.Err(); err != nil {
		return stepOut{}, err
	}
	done := make(chan CLIRun, 1)
	tune := func(o *cliStart) {
		o.ReadOnly = r.ReadOnly
		o.Origin = firstNonEmpty(r.Origin, o.Origin)
		o.Done = func(run CLIRun) {
			select {
			case done <- run:
			default:
			}
		}
	}
	var run CLIRun
	var err error
	if r.Prev != "" {
		run, err = a.followUpCLIRun(r.Prev, r.Prompt, tune)
		if err != nil && r.Fresh != "" && !strings.Contains(err.Error(), "still running") {
			r.Prev, r.Prompt = "", r.Fresh
			err = errors.New("fresh")
		}
	}
	if r.Prev == "" {
		o := cliStart{Agent: r.Agent, Prompt: r.Prompt, Cwd: r.Cwd, Model: r.Model, Origin: "a page"}
		tune(&o)
		run, err = a.startCLIRun(o)
	}
	if err != nil {
		return stepOut{}, err
	}
	if r.OnStart != nil {
		r.OnStart(run)
	}
	select {
	case <-ctx.Done():
		a.CancelCLIRun(run.ID)
		select {
		case fin := <-done:
			return stepOut{Run: fin, Answer: a.answerOf(fin)}, ctx.Err()
		case <-time.After(10 * time.Second):
		}
		return stepOut{Run: run}, ctx.Err()
	case fin := <-done:
		return stepOut{Run: fin, Answer: a.answerOf(fin)}, nil
	}
}

// answerOf is what a finished run said last.
func (a *App) answerOf(r CLIRun) string {
	if t := strings.TrimSpace(r.Summary); t != "" {
		return t
	}
	return a.lastAnswer(r.ID)
}

func (a *App) lastAnswer(id string) string {
	d, err := a.CLIRunDetail(id)
	if err != nil {
		return ""
	}
	for i := len(d.Events) - 1; i >= 0; i-- {
		if d.Events[i].Kind == "text" && strings.TrimSpace(d.Events[i].Text) != "" {
			return strings.TrimSpace(d.Events[i].Text)
		}
	}
	return ""
}

// ---- the store, shared with debates ----

const debatesKept = 100

type debateStore struct {
	mu      sync.Mutex
	debates map[string]*Debate
	reviews map[string]*ImplementReview
	cancels map[string]context.CancelFunc
}

type debateFile struct {
	Debates []*Debate          `json:"debates"`
	Reviews []*ImplementReview `json:"reviews"`
}

func debatesPath() string { return filepath.Join(backend.DataDir(), "debates.json") }

func (a *App) debateStoreOf() *debateStore {
	a.debateOnce.Do(func() {
		s := &debateStore{debates: map[string]*Debate{}, reviews: map[string]*ImplementReview{}, cancels: map[string]context.CancelFunc{}}
		s.load()
		a.debates = s
	})
	return a.debates
}

func (s *debateStore) load() {
	raw, err := os.ReadFile(debatesPath())
	if err != nil {
		return
	}
	var f debateFile
	if json.Unmarshal(raw, &f) != nil {
		return
	}
	now := time.Now()
	const gone = "SuperLeo stopped while this was running"
	for _, d := range f.Debates {
		if d.State == "running" {
			d.State, d.Error, d.Ended = "cancelled", gone, &now
			for i := range d.Steps {
				if d.Steps[i].State == "running" || d.Steps[i].State == "pending" {
					d.Steps[i].State = "cancelled"
				}
			}
		}
		s.debates[d.ID] = d
	}
	for _, r := range f.Reviews {
		if r.State == "running" {
			r.State, r.Error, r.Ended = "cancelled", gone, &now
			for i := range r.Steps {
				if r.Steps[i].State == "running" {
					r.Steps[i].State = "cancelled"
				}
			}
		}
		s.reviews[r.ID] = r
	}
}

// saveLocked writes the newest debates and tracks. Called with s.mu held.
func (s *debateStore) saveLocked() {
	var f debateFile
	for _, d := range s.debates {
		f.Debates = append(f.Debates, d)
	}
	for _, r := range s.reviews {
		f.Reviews = append(f.Reviews, r)
	}
	sort.Slice(f.Debates, func(i, j int) bool { return f.Debates[i].Started.After(f.Debates[j].Started) })
	sort.Slice(f.Reviews, func(i, j int) bool { return f.Reviews[i].Started.After(f.Reviews[j].Started) })
	if len(f.Debates) > debatesKept {
		for _, d := range f.Debates[debatesKept:] {
			delete(s.debates, d.ID)
		}
		f.Debates = f.Debates[:debatesKept]
	}
	if len(f.Reviews) > debatesKept {
		for _, r := range f.Reviews[debatesKept:] {
			delete(s.reviews, r.ID)
		}
		f.Reviews = f.Reviews[:debatesKept]
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return
	}
	tmp := debatesPath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, debatesPath())
	}
}
