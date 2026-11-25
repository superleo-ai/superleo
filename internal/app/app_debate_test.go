package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/agentexec"
	"github.com/liliang-cn/superleo/internal/backend"
)

func TestTheJudgesMarkdownSplitsIntoItsFourSections(t *testing.T) {
	text := "Some preamble.\n\n## Verdict\nThe cache is not thread-safe: `items` is a plain map.\n\n## Agreed\n- Get and Set touch `items` without a lock (cache.go:21,31)\n- `hits++` in Get is a write on a read path\n\n## Disagreements\n- Whether Hits() races | right: codex | it reads hits unguarded (cache.go:35)\n- Does expiry matter? **right: unresolved** neither checked the clock\n- Should this be sync.Map — Right: Claude — a mutex is simpler\n\n## Next steps\n1. Add a sync.RWMutex to Cache\n2. Take the write lock in Get because of `hits++`\n   (that is a write)\n"
	c := parseConclusion(text, []string{"claude", "codex"})
	if !strings.HasPrefix(c.Verdict, "The cache is not thread-safe") {
		t.Fatalf("verdict %q", c.Verdict)
	}
	if len(c.Agreed) != 2 || !strings.Contains(c.Agreed[1], "hits++") {
		t.Fatalf("agreed %q", c.Agreed)
	}
	if len(c.Disagreements) != 3 {
		t.Fatalf("disagreements %+v", c.Disagreements)
	}
	if d := c.Disagreements[0]; d.Who != "codex" || !strings.HasPrefix(d.Text, "Whether Hits() races") || strings.Contains(d.Text, "right:") {
		t.Fatalf("first %+v", d)
	}
	if c.Disagreements[1].Who != "unresolved" {
		t.Fatalf("second %+v", c.Disagreements[1])
	}
	if c.Disagreements[2].Who != "claude" {
		t.Fatalf("third %+v", c.Disagreements[2])
	}
	if len(c.NextSteps) != 2 || !strings.Contains(c.NextSteps[1], "(that is a write)") {
		t.Fatalf("next %q", c.NextSteps)
	}
}

func TestAReviewEndsWithAgreeDisagreeRevisedLines(t *testing.T) {
	got := parseStances("I checked it.\n\nAGREE: the map is unguarded\n- DISAGREE: Hits is safe because it is atomic\n**REVISED:** it is unsafe in three places\nagree me later\nAGREE:\n")
	if len(got) != 3 || got[0].Kind != "agree" || got[1].Kind != "disagree" || got[2].Kind != "revised" || got[2].Text != "it is unsafe in three places" {
		t.Fatalf("%+v", got)
	}
}

func TestAVerdictAndItsFindingsAreRead(t *testing.T) {
	v, f := parseVerdict("Looked fine at first.\nVERDICT: CHANGES\n- cache.go:21 — Get still reads the map without the lock\n- `cache.go:31-33` — Set holds the lock too long\n- no file here, just a remark\n")
	if v != "CHANGES" || len(f) != 3 {
		t.Fatalf("%q %+v", v, f)
	}
	if f[0].File != "cache.go" || f[0].Line != 21 || f[0].Text != "Get still reads the map without the lock" {
		t.Fatalf("%+v", f[0])
	}
	if f[1].File != "cache.go" || f[1].Line != 31 {
		t.Fatalf("%+v", f[1])
	}
	if f[2].File != "" || f[2].Text != "no file here, just a remark" {
		t.Fatalf("%+v", f[2])
	}
	if v, f := parseVerdict("**VERDICT: APPROVE**\n- nit: none"); v != "APPROVE" || len(f) != 0 {
		t.Fatalf("%q %+v", v, f)
	}
	if v, _ := parseVerdict("I think it is fine."); v != "" {
		t.Fatalf("a verdict was invented: %q", v)
	}
	// The last VERDICT wins: a reviewer quoting the format first is not it.
	if v, _ := parseVerdict("Format: VERDICT: APPROVE or VERDICT: CHANGES\n\nVERDICT: CHANGES\n- a.go:1 — x"); v != "CHANGES" {
		t.Fatalf("%q", v)
	}
}

func TestClaudeAndCodexAreLockedReadOnlyOthersAreOnlyAsked(t *testing.T) {
	c := agentexec.Request{PermissionMode: agentexec.PermissionBypass}
	cliReadOnlyArgs(&c, "claude")
	if c.PermissionMode != agentexec.PermissionDefault || !strings.Contains(strings.Join(c.ExtraArgs, " "), "--permission-mode plan") ||
		!strings.Contains(strings.Join(c.ExtraArgs, " "), "--disallowedTools=Edit,Write,MultiEdit,NotebookEdit") {
		t.Fatalf("claude %+v", c)
	}
	x := agentexec.Request{PermissionMode: agentexec.PermissionBypass}
	cliReadOnlyArgs(&x, "codex")
	if !x.Sandbox || !strings.Contains(strings.Join(x.ExtraArgs, " "), `sandbox_mode="read-only"`) {
		t.Fatalf("codex %+v", x)
	}
	o := agentexec.Request{PermissionMode: agentexec.PermissionBypass}
	cliReadOnlyArgs(&o, "pi")
	if len(o.ExtraArgs) != 0 || cliCanBeReadOnly("pi") {
		t.Fatalf("pi %+v", o)
	}
	if !strings.Contains(readOnlyNote("pi"), "only guard") || strings.Contains(readOnlyNote("claude"), "only guard") {
		t.Fatal("the prompt does not say which CLIs cannot be locked")
	}
}

// fakeSteps is a stepRunner that records what it is asked and answers by a script.
type fakeSteps struct {
	mu    sync.Mutex
	reqs  []stepReq
	reply func(r stepReq, n int) (stepOut, error)
}

func (f *fakeSteps) run(ctx context.Context, r stepReq) (stepOut, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	n := len(f.reqs)
	f.mu.Unlock()
	id := "run-" + string(rune('a'+n))
	if r.OnStart != nil {
		r.OnStart(CLIRun{ID: id})
	}
	out, err := f.reply(r, n)
	if out.Run.ID == "" {
		out.Run.ID = id
	}
	if out.Run.State == "" {
		out.Run.State = "done"
	}
	return out, err
}

func waitDebate(t *testing.T, a *App, id string) Debate {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if d, _ := a.DebateDetail(id); d.State != "running" {
			return d
		}
	}
	t.Fatal("the debate did not finish")
	return Debate{}
}

func newDebateApp(t *testing.T, dir string, f *fakeSteps) *App {
	t.Helper()
	t.Setenv("SUPERLEO_HOME", t.TempDir())
	a := &App{settings: &backend.Settings{WorkspaceDir: dir}, stepRun: f.run}
	return a
}

const fakeConclusion = "## Verdict\nNot safe.\n\n## Agreed\n- map unguarded\n\n## Disagreements\n- Hits | right: codex | atomic or not\n\n## Next steps\n- add a mutex\n"

func TestADebateTakesPositionsInParallelThenReviewsEachOthersLatestAnswerAndConcludes(t *testing.T) {
	dir := t.TempDir()
	// Both positions must be in flight together: each waits for the other.
	var posWait sync.WaitGroup
	posWait.Add(2)
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		switch {
		case strings.Contains(r.Prompt, "independently examining"):
			posWait.Done()
			posWait.Wait()
			return stepOut{Answer: "POSITION by " + r.Agent + "\nCLAIM: x (cache.go:1)"}, nil
		case strings.Contains(r.Prompt, "review round"):
			return stepOut{Answer: "checked.\nAGREE: a\nDISAGREE: b because c (cache.go:9)\nREVISED: d"}, nil
		default:
			return stepOut{Answer: fakeConclusion}, nil
		}
	}
	a := newDebateApp(t, dir, f)
	d, err := a.StartDebate(DebateRequest{Topic: "is this cache thread-safe?", Cwd: dir, Rounds: 1, Judge: "pi"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitDebate(t, a, d.ID)
	if got.State != "done" || got.Conclusion == nil || got.Conclusion.Verdict != "Not safe." || got.Conclusion.Disagreements[0].Who != "codex" {
		t.Fatalf("%+v %+v", got.State, got.Conclusion)
	}
	if got.Agents[0] != "claude" || got.Agents[1] != "codex" || got.Rounds != 1 || got.JudgeAgent != "pi" {
		t.Fatalf("defaults %+v", got)
	}
	// 2 positions + 2 reviews + 1 conclusion, every one read-only.
	if len(got.Steps) != 5 || len(f.reqs) != 5 {
		t.Fatalf("%d steps %d requests", len(got.Steps), len(f.reqs))
	}
	for _, r := range f.reqs {
		if !r.ReadOnly || r.Cwd == "" {
			t.Fatalf("a step could write: %+v", r)
		}
	}
	// A review carries the other side's answer verbatim.
	for _, st := range got.Steps {
		if st.Phase != "review" {
			continue
		}
		other := map[string]string{"claude": "codex", "codex": "claude"}[st.Agent]
		var req stepReq
		for _, r := range f.reqs {
			if r.Agent == st.Agent && strings.Contains(r.Prompt, "review round") {
				req = r
			}
		}
		if !strings.Contains(req.Prompt, "POSITION by "+other+"\nCLAIM: x (cache.go:1)") || strings.Contains(req.Prompt, "POSITION by "+st.Agent+"\n") {
			t.Fatalf("%s was not given %s's answer:\n%s", st.Agent, other, req.Prompt)
		}
		if req.Prev == "" {
			t.Fatalf("%s's review did not continue its own session", st.Agent)
		}
		if len(st.Stances) != 3 || st.Stances[1].Kind != "disagree" {
			t.Fatalf("stances %+v", st.Stances)
		}
	}
	if !strings.Contains(f.reqs[4].Prompt, "POSITION by claude") || !strings.Contains(f.reqs[4].Prompt, "DISAGREE: b because c") {
		t.Fatalf("the judge was not handed the transcript:\n%s", f.reqs[4].Prompt)
	}
	if !strings.Contains(f.reqs[4].Prompt, "only guard") {
		t.Fatalf("a judge that cannot be locked read-only was not told so:\n%s", f.reqs[4].Prompt)
	}
}

func TestAFailedStepDoesNotStopTheDebate(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		switch {
		case strings.Contains(r.Prompt, "independently examining"):
			if r.Agent == "codex" {
				return stepOut{Run: CLIRun{State: "failed", Error: "auth"}}, nil
			}
			return stepOut{Answer: "claude says it is unsafe"}, nil
		case strings.Contains(r.Prompt, "review round"):
			return stepOut{Answer: "AGREE: ok"}, nil
		}
		return stepOut{Answer: fakeConclusion}, nil
	}
	a := newDebateApp(t, dir, f)
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir, Rounds: 2})
	got := waitDebate(t, a, d.ID)
	if got.State != "done" || got.Conclusion == nil {
		t.Fatalf("%s %s", got.State, got.Error)
	}
	var failed, reviews int
	for _, st := range got.Steps {
		if st.State == "failed" {
			failed++
			if st.Agent != "codex" || st.Error != "auth" {
				t.Fatalf("%+v", st)
			}
		}
		if st.Phase == "review" {
			reviews++
			// Round 1: claude has nothing from codex to check, so only codex reviews.
			if st.Round == 1 && st.Agent != "codex" {
				t.Fatalf("claude reviewed codex's missing answer: %+v", st)
			}
		}
	}
	if failed != 1 || reviews != 3 {
		t.Fatalf("failed %d reviews %d", failed, reviews)
	}
}

func TestNobodyTakingAPositionFailsTheDebate(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSteps{reply: func(r stepReq, n int) (stepOut, error) { return stepOut{}, context.DeadlineExceeded }}
	a := newDebateApp(t, dir, f)
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir})
	if got := waitDebate(t, a, d.ID); got.State != "failed" || len(f.reqs) != 2 {
		t.Fatalf("%s %d", got.State, len(f.reqs))
	}
}

func TestCancellingADebateStopsEveryLiveStep(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{}, 2)
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		started <- struct{}{}
		return stepOut{}, nil
	}
	f2 := func(ctx context.Context, r stepReq) (stepOut, error) {
		started <- struct{}{}
		<-ctx.Done()
		return stepOut{}, ctx.Err()
	}
	a := newDebateApp(t, dir, f)
	a.stepRun = f2
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir})
	<-started
	<-started
	if got := a.CancelDebate(d.ID); got != "ok" {
		t.Fatal(got)
	}
	got := waitDebate(t, a, d.ID)
	if got.State != "cancelled" {
		t.Fatalf("%s", got.State)
	}
	for _, st := range got.Steps {
		if st.State != "cancelled" {
			t.Fatalf("a step was left %s", st.State)
		}
	}
	if a.CancelDebate(d.ID) == "ok" {
		t.Fatal("cancelling a finished debate said ok")
	}
}

func TestContinueAddsOneRoundAndConcludesAgain(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		if strings.Contains(r.Prompt, "## Verdict") {
			return stepOut{Answer: fakeConclusion}, nil
		}
		return stepOut{Answer: "answer " + r.Agent}, nil
	}
	a := newDebateApp(t, dir, f)
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir, Rounds: 1})
	waitDebate(t, a, d.ID)
	if _, err := a.ContinueDebate(d.ID); err != nil {
		t.Fatal(err)
	}
	got := waitDebate(t, a, d.ID)
	if got.Rounds != 2 || got.State != "done" {
		t.Fatalf("%+v", got)
	}
	var round2, concl int
	for _, st := range got.Steps {
		if st.Phase == "review" && st.Round == 2 {
			round2++
		}
		if st.Phase == "conclusion" {
			concl++
		}
	}
	if round2 != 2 || concl != 2 {
		t.Fatalf("round2 %d conclusions %d", round2, concl)
	}
	if _, err := a.ContinueDebate("nope"); err == nil {
		t.Fatal("continued a debate that is not there")
	}
}

func TestDebatesAreKeptAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	f := &fakeSteps{reply: func(r stepReq, n int) (stepOut, error) { return stepOut{Answer: fakeConclusion}, nil }}
	a := newDebateApp(t, dir, f)
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir, Rounds: 1})
	waitDebate(t, a, d.ID)
	b := &App{settings: a.settings}
	got, err := b.DebateDetail(d.ID)
	if err != nil || got.Conclusion == nil || len(got.Steps) == 0 {
		t.Fatalf("%v %+v", err, got)
	}
	if list := b.Debates(); len(list) != 1 || list[0].Steps != nil {
		t.Fatalf("%+v", list)
	}
}

// ---- implement -> review ----

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func repoWithFile(t *testing.T) string {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	gitT(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "a.go")
	gitT(t, dir, "commit", "-qm", "init")
	return dir
}

func waitReview(t *testing.T, a *App, id string) ImplementReview {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if r, _ := a.ImplementReviewDetail(id); r.State != "running" {
			return r
		}
	}
	t.Fatal("the review did not finish")
	return ImplementReview{}
}

func TestTheOtherAgentReviewsTheDiffAndFindingsGoBackUntilApproved(t *testing.T) {
	dir := repoWithFile(t)
	// A change that was there before the run is not the run's work.
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reviews := 0
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		switch {
		case r.Agent == "claude" && r.Prev == "":
			os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc One() {}\n"), 0o644)
			return stepOut{Answer: "added One"}, nil
		case r.Agent == "claude":
			os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc One() {}\nfunc Two() {}\n"), 0o644)
			return stepOut{Answer: "fixed"}, nil
		}
		reviews++
		if reviews == 1 {
			return stepOut{Answer: "VERDICT: CHANGES\n- a.go:3 — One has no Two\n"}, nil
		}
		return stepOut{Answer: "VERDICT: APPROVE"}, nil
	}
	a := newDebateApp(t, dir, f)
	rv, err := a.StartImplementReview(ImplementReviewRequest{Cwd: dir, Prompt: "add One", Implementer: "claude", Cycles: -1})
	if err != nil {
		t.Fatal(err)
	}
	got := waitReview(t, a, rv.ID)
	if got.State != "approved" || got.Reviewer != "codex" || got.Cycles != 2 {
		t.Fatalf("%s %s %+v", got.State, got.Error, got)
	}
	kinds := []string{}
	for _, st := range got.Steps {
		kinds = append(kinds, st.Kind)
	}
	if strings.Join(kinds, ",") != "implement,review,fix,review" {
		t.Fatalf("%v", kinds)
	}
	// The reviewers were read-only; the implementer was not.
	for _, r := range f.reqs {
		if (r.Agent == "codex") != r.ReadOnly {
			t.Fatalf("read-only is wrong for %+v", r)
		}
	}
	first := f.reqs[1].Prompt
	if !strings.Contains(first, "+func One() {}") || strings.Contains(first, "scratch.txt") || strings.Contains(first, "mine") {
		t.Fatalf("the reviewer did not get base -> head, only the run's changes:\n%s", first)
	}
	if !strings.Contains(first, "git diff "+got.Base) {
		t.Fatal("the reviewer was not told how to read the diff again")
	}
	if fixp := f.reqs[2]; fixp.Prev != "run-b" || !strings.Contains(fixp.Prompt, "a.go:3 — One has no Two") {
		t.Fatalf("the findings did not go to the implementer's session: %+v", fixp)
	}
	if again := f.reqs[3]; again.Prev == "" || !strings.Contains(again.Prompt, "+func Two() {}") {
		t.Fatalf("the second review is not a follow-up on the new diff: %+v", again)
	}
	if got.Steps[1].Verdict != "CHANGES" || len(got.Steps[1].Findings) != 1 || got.Steps[1].Diff == nil || got.Steps[1].Diff.Files != 1 {
		t.Fatalf("%+v", got.Steps[1])
	}
	// The repository itself was left as the run left it: no stash, nothing staged.
	if gitT(t, dir, "stash", "list") != "" || gitT(t, dir, "diff", "--cached", "--name-only") != "" {
		t.Fatal("the snapshot touched the repository's index or stash")
	}
	diff, err := a.ImplementReviewDiff(rv.ID)
	if err != nil || !strings.Contains(diff, "+func Two() {}") || strings.Contains(diff, "scratch.txt") {
		t.Fatalf("%v\n%s", err, diff)
	}
}

func TestChangesLeftWhenTheCapIsReached(t *testing.T) {
	dir := repoWithFile(t)
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		if r.Agent == "claude" {
			os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// "+string(rune('0'+n))+"\n"), 0o644)
			return stepOut{Answer: "did it"}, nil
		}
		return stepOut{Answer: "VERDICT: CHANGES\n- a.go:1 — no"}, nil
	}
	a := newDebateApp(t, dir, f)
	rv, _ := a.StartImplementReview(ImplementReviewRequest{Cwd: dir, Prompt: "do", Implementer: "claude", Cycles: 1})
	got := waitReview(t, a, rv.ID)
	// implement, review, fix, review: one fix, then it stops.
	if got.State != "changes" || len(got.Steps) != 4 {
		t.Fatalf("%s %d", got.State, len(got.Steps))
	}
}

func TestZeroCyclesIsReviewOnlyOfAFinishedRun(t *testing.T) {
	dir := repoWithFile(t)
	head := gitHead(dir)
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		return stepOut{Answer: "VERDICT: CHANGES\n- a.go:3 — bad"}, nil
	}
	a := newDebateApp(t, dir, f)
	s := a.cliStore()
	s.runs["r1"] = &CLIRun{ID: "r1", Thread: "r1", Agent: "codex", Prompt: "add a thing", Cwd: dir, Base: head, State: "done", Summary: "added", Started: time.Now()}
	// The finished run's change is in the working tree.
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nvar X = 1\n"), 0o644)
	rv, err := a.StartImplementReview(ImplementReviewRequest{FromRun: "r1", Cycles: 0})
	if err != nil {
		t.Fatal(err)
	}
	got := waitReview(t, a, rv.ID)
	if got.Implementer != "codex" || got.Reviewer != "claude" || got.State != "changes" || len(got.Steps) != 1 || got.Steps[0].Kind != "review" {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(f.reqs[0].Prompt, "+var X = 1") || !strings.Contains(f.reqs[0].Prompt, "added") {
		t.Fatalf("%s", f.reqs[0].Prompt)
	}
	if _, err := a.StartImplementReview(ImplementReviewRequest{FromRun: "nope"}); err == nil {
		t.Fatal("reviewed a run that is not there")
	}
}

func TestAReviewWithNothingChangedFailsInsteadOfApproving(t *testing.T) {
	dir := repoWithFile(t)
	f := &fakeSteps{reply: func(r stepReq, n int) (stepOut, error) { return stepOut{Answer: "did nothing"}, nil }}
	a := newDebateApp(t, dir, f)
	rv, _ := a.StartImplementReview(ImplementReviewRequest{Cwd: dir, Prompt: "do", Implementer: "claude"})
	if got := waitReview(t, a, rv.ID); got.State != "failed" || len(f.reqs) != 1 {
		t.Fatalf("%s %d", got.State, len(f.reqs))
	}
}

func TestDebateConclusionRunLinksTheTrackToTheDebate(t *testing.T) {
	dir := repoWithFile(t)
	f := &fakeSteps{}
	f.reply = func(r stepReq, n int) (stepOut, error) {
		if r.Agent == "claude" && !r.ReadOnly {
			os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n// mutex\n"), 0o644)
			return stepOut{Answer: "done"}, nil
		}
		if strings.Contains(r.Prompt, "reviewing a change") {
			return stepOut{Answer: "VERDICT: APPROVE"}, nil
		}
		return stepOut{Answer: fakeConclusion}, nil
	}
	a := newDebateApp(t, dir, f)
	d, _ := a.StartDebate(DebateRequest{Topic: "q", Cwd: dir, Rounds: 1})
	waitDebate(t, a, d.ID)
	rv, err := a.StartImplementReview(ImplementReviewRequest{Cwd: dir, Prompt: "Conclusion: add a mutex", Implementer: "claude", Debate: d.ID, Cycles: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitReview(t, a, rv.ID); got.State != "approved" {
		t.Fatalf("%s %s", got.State, got.Error)
	}
	if got, _ := a.DebateDetail(d.ID); len(got.Reviews) != 1 || got.Reviews[0] != rv.ID {
		t.Fatalf("%+v", got.Reviews)
	}
}
