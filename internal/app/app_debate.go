package app

// Debate: two agent CLIs look at one project, question each other's answers,
// and a judge writes the conclusion.
//
//  1. Position: each agent investigates the project read-only and answers,
//     citing file:line, ending with its key claims.
//  2. Review rounds: each is handed the other's latest answer verbatim, checks
//     those claims against the code, and ends with AGREE / DISAGREE / REVISED
//     lines. Both go at once; each keeps its own CLI session across rounds.
//  3. Conclusion: the judge reads the whole transcript and writes Verdict,
//     Agreed, Disagreements and Next steps.
//
// Every step is an ordinary CLI run (it shows in the Coding list too), and no
// step may edit a file: cliReadOnlyArgs holds claude and codex to it. A step
// that fails is marked so and the debate goes on with what it has. The
// conclusion can be handed to one agent to implement and the other to review
// (app_review.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DebateRequest starts a debate.
type DebateRequest struct {
	Topic string `json:"topic"`
	Cwd   string `json:"cwd"`
	// Agents are the two CLIs, default claude and codex; Models, if given,
	// is one model per agent in the same order.
	Agents []string `json:"agents"`
	Models []string `json:"models"`
	// Rounds of review, 1 to 3, default 2.
	Rounds int `json:"rounds"`
	// Judge is "queen" (the queen's engine, the default), or a CLI.
	Judge string `json:"judge"`
}

// DebateStance is one AGREE / DISAGREE / REVISED line of a review.
type DebateStance struct {
	Kind string `json:"kind"` // agree, disagree, revised
	Text string `json:"text"`
}

// DebateStep is one agent's turn in one phase.
type DebateStep struct {
	ID    string `json:"id"`
	Phase string `json:"phase"` // position, review, conclusion
	// Round is 0 for the position and the conclusion, 1.. for reviews.
	Round int    `json:"round"`
	Agent string `json:"agent"`
	// Side is which of the two agents this is, 0 or 1: two agents may be the
	// same CLI.
	Side     int            `json:"side"`
	Run      string         `json:"run,omitempty"`
	State    string         `json:"state"` // running, done, failed, cancelled
	Started  time.Time      `json:"started"`
	Ended    *time.Time     `json:"ended,omitempty"`
	In       int            `json:"in"`
	Out      int            `json:"out"`
	CostUSD  float64        `json:"costUsd"`
	Text     string         `json:"text,omitempty"`
	Stances  []DebateStance `json:"stances,omitempty"`
	Error    string         `json:"error,omitempty"`
	ReadOnly bool           `json:"readOnly"` // enforced by the CLI, not only asked for
}

// DebateDisagreement is one point they still differ on, and who is right.
type DebateDisagreement struct {
	Text string `json:"text"`
	// Who is the agent the judge found right, or "unresolved", or "" if the
	// judge named no one.
	Who string `json:"who,omitempty"`
}

// DebateConclusion is the judge's answer, parsed.
type DebateConclusion struct {
	Verdict       string               `json:"verdict"`
	Agreed        []string             `json:"agreed"`
	Disagreements []DebateDisagreement `json:"disagreements"`
	NextSteps     []string             `json:"nextSteps"`
	Raw           string               `json:"raw"`
	Judge         string               `json:"judge"`
}

// Debate is one debate with everything said.
type Debate struct {
	ID         string   `json:"id"`
	Topic      string   `json:"topic"`
	Cwd        string   `json:"cwd"`
	Agents     []string `json:"agents"`
	Models     []string `json:"models,omitempty"`
	Rounds     int      `json:"rounds"`
	Judge      string   `json:"judge"`      // as asked: "queen" or a CLI
	JudgeAgent string   `json:"judgeAgent"` // the CLI that judges
	// State is running, done, failed or cancelled.
	State      string            `json:"state"`
	Steps      []DebateStep      `json:"steps"`
	Conclusion *DebateConclusion `json:"conclusion,omitempty"`
	// Reviews are the implement -> review tracks started from the conclusion.
	Reviews []string   `json:"reviews,omitempty"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// ---- parsing ----

var (
	stanceRe  = regexp.MustCompile(`(?i)^[\s>*_\x60#-]*(DISAGREE|AGREE|REVISED)\s*[*_\x60]*\s*[:：]\s*(.*)$`)
	sectionRe = regexp.MustCompile(`(?i)^\s{0,3}(?:#{1,6}\s*)?[*_]*\s*(?:\d+[.)]\s*)?(verdict|agreed|disagreements?|next steps?)\s*[*_]*\s*:?\s*[*_]*\s*$`)
	whoRe     = regexp.MustCompile(`(?i)\b(?:right|correct|winner)\s*[:：]\s*[*_\x60]*([A-Za-z0-9_.-]+)[*_\x60]*`)
)

// parseStances reads the AGREE / DISAGREE / REVISED lines of a review.
func parseStances(text string) []DebateStance {
	var out []DebateStance
	for _, line := range strings.Split(text, "\n") {
		m := stanceRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		body := strings.TrimSpace(strings.Trim(m[2], "*_` "))
		if body == "" {
			continue
		}
		out = append(out, DebateStance{Kind: strings.ToLower(m[1]), Text: body})
	}
	return out
}

func bulletsOf(body []string) []string {
	var items []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			items = append(items, strings.Join(para, " "))
			para = nil
		}
	}
	sawBullet := false
	for _, l := range body {
		if b := bulletRe.FindStringSubmatch(l); b != nil {
			sawBullet = true
			flush()
			if t := strings.TrimSpace(b[1]); t != "" {
				items = append(items, t)
			}
			continue
		}
		if t := strings.TrimSpace(l); t == "" {
			if !sawBullet {
				flush()
			}
		} else if sawBullet && len(items) > 0 {
			items[len(items)-1] += " " + t // a bullet's wrapped line
		} else {
			para = append(para, t)
		}
	}
	flush()
	return items
}

// parseConclusion splits the judge's markdown into its four sections.
func parseConclusion(text string, agents []string) DebateConclusion {
	c := DebateConclusion{Raw: strings.TrimSpace(text), Agreed: []string{}, Disagreements: []DebateDisagreement{}, NextSteps: []string{}}
	sections := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(text, "\n") {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(m[1])
			if strings.HasPrefix(cur, "disagree") {
				cur = "disagreements"
			} else if strings.HasPrefix(cur, "next") {
				cur = "next"
			}
			continue
		}
		if cur != "" {
			sections[cur] = append(sections[cur], line)
		}
	}
	c.Verdict = strings.TrimSpace(strings.Join(sections["verdict"], "\n"))
	c.Agreed = append(c.Agreed, bulletsOf(sections["agreed"])...)
	c.NextSteps = append(c.NextSteps, bulletsOf(sections["next"])...)
	for _, item := range bulletsOf(sections["disagreements"]) {
		c.Disagreements = append(c.Disagreements, parseDisagreement(item, agents))
	}
	return c
}

func parseDisagreement(item string, agents []string) DebateDisagreement {
	d := DebateDisagreement{Text: item}
	clean := strings.NewReplacer("**", "", "`", "").Replace(item)
	if m := whoRe.FindStringSubmatchIndex(clean); m != nil {
		who := strings.Trim(clean[m[2]:m[3]], ".,;")
		d.Who = who
		for _, a := range agents {
			if strings.EqualFold(a, who) {
				d.Who = a
			}
		}
		rest := strings.TrimSpace(clean[:m[0]] + clean[m[1]:])
		parts := []string{}
		for _, p := range strings.Split(rest, "|") {
			if p = strings.Trim(strings.TrimSpace(p), "—–-: "); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			d.Text = strings.Join(parts, " — ")
		}
	} else if strings.Contains(strings.ToLower(clean), "unresolved") {
		d.Who = "unresolved"
	}
	return d
}

// ---- starting ----

// StartDebate starts a debate in the background.
func (a *App) StartDebate(req DebateRequest) (Debate, error) {
	req.Topic = strings.TrimSpace(req.Topic)
	if req.Topic == "" {
		return Debate{}, errors.New("the question is empty")
	}
	agents := []string{}
	for _, ag := range req.Agents {
		if ag = strings.TrimSpace(ag); ag != "" {
			agents = append(agents, ag)
		}
	}
	if len(agents) == 0 {
		agents = []string{"claude", "codex"}
	}
	if len(agents) != 2 {
		return Debate{}, errors.New("a debate needs two agents")
	}
	models := []string{"", ""}
	for i := range models {
		if i < len(req.Models) {
			models[i] = strings.TrimSpace(req.Models[i])
		}
	}
	rounds := req.Rounds
	if rounds == 0 {
		rounds = 2
	}
	if rounds < 1 || rounds > 3 {
		return Debate{}, errors.New("rounds are 1 to 3")
	}
	judge := strings.TrimSpace(req.Judge)
	if judge == "" {
		judge = "queen"
	}
	for _, ag := range append(append([]string{}, agents...), judge) {
		if ag == "queen" {
			continue
		}
		if _, _, ok := splitRemoteCLI(ag); ok {
			return Debate{}, fmt.Errorf("%s: a debate runs on this machine's CLIs", ag)
		}
		if a.stepRun == nil {
			if _, _, err := a.cliProvider(ag); err != nil {
				return Debate{}, err
			}
		}
	}
	dir, err := a.reviewCwd(req.Cwd, agents[0])
	if err != nil {
		return Debate{}, err
	}
	d := &Debate{ID: uuid.NewString(), Topic: req.Topic, Cwd: dir, Agents: agents, Models: models, Rounds: rounds,
		Judge: judge, JudgeAgent: a.judgeAgent(judge, agents), State: "running", Started: time.Now(), Steps: []DebateStep{}}
	s := a.debateStoreOf()
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.debates[d.ID] = d
	s.cancels[d.ID] = cancel
	s.saveLocked()
	out := *d
	s.mu.Unlock()
	a.pushDebate(d)
	go a.runDebate(ctx, d, 0)
	return out, nil
}

// judgeAgent is the CLI that judges: the one asked for, or for "queen" the
// first of the queen's engines that is a CLI here, else the first debater.
func (a *App) judgeAgent(judge string, agents []string) string {
	if judge != "queen" {
		return judge
	}
	a.mu.Lock()
	var engines []string
	if a.settings != nil {
		engines = a.settings.Hive.QueenEnginesOn()
	}
	a.mu.Unlock()
	for _, e := range engines {
		if e == "" || e == "superleo" {
			continue
		}
		if _, _, remote := splitRemoteCLI(e); remote {
			continue
		}
		if a.stepRun != nil {
			return e
		}
		if _, _, err := a.cliProvider(e); err == nil {
			return e
		}
	}
	return agents[0]
}

// Debates lists debates newest first, without their steps.
func (a *App) Debates() []Debate {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Debate{}
	for _, d := range s.debates {
		c := *d
		c.Steps, c.Conclusion = nil, nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// DebateDetail is one debate with everything said.
func (a *App) DebateDetail(id string) (Debate, error) {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.debates[id]
	if d == nil {
		return Debate{}, fmt.Errorf("no debate %s", id)
	}
	return copyDebate(d), nil
}

func copyDebate(d *Debate) Debate {
	c := *d
	c.Steps = append([]DebateStep{}, d.Steps...)
	c.Reviews = append([]string{}, d.Reviews...)
	return c
}

// CancelDebate stops a debate and every step it has going.
func (a *App) CancelDebate(id string) string {
	s := a.debateStoreOf()
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return "that debate is not running"
	}
	cancel()
	return "ok"
}

// ContinueDebate runs one more round of review and writes a new conclusion.
func (a *App) ContinueDebate(id string) (Debate, error) {
	s := a.debateStoreOf()
	s.mu.Lock()
	d := s.debates[id]
	if d == nil {
		s.mu.Unlock()
		return Debate{}, fmt.Errorf("no debate %s", id)
	}
	if d.State == "running" {
		s.mu.Unlock()
		return Debate{}, errors.New("this debate is still going")
	}
	if !hasDone(d, "position") {
		s.mu.Unlock()
		return Debate{}, errors.New("nobody took a position, so there is nothing to continue")
	}
	d.Rounds++
	d.State, d.Error, d.Ended = "running", "", nil
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[d.ID] = cancel
	s.saveLocked()
	out := copyDebate(d)
	round := d.Rounds
	s.mu.Unlock()
	a.pushDebate(d)
	go a.runDebate(ctx, d, round)
	return out, nil
}

func hasDone(d *Debate, phase string) bool {
	for _, st := range d.Steps {
		if st.Phase == phase && st.State == "done" && strings.TrimSpace(st.Text) != "" {
			return true
		}
	}
	return false
}

// ---- the flow ----

func (a *App) mutateDebate(d *Debate, f func(*Debate)) {
	s := a.debateStoreOf()
	s.mu.Lock()
	f(d)
	s.saveLocked()
	s.mu.Unlock()
	a.pushDebate(d)
}

func (a *App) pushDebateID(id string) {
	s := a.debateStoreOf()
	s.mu.Lock()
	d := s.debates[id]
	s.mu.Unlock()
	if d != nil {
		a.pushDebate(d)
	}
}

func (a *App) pushDebate(d *Debate) {
	s := a.debateStoreOf()
	s.mu.Lock()
	raw, _ := json.Marshal(d)
	s.mu.Unlock()
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		a.emit("debate:update", m)
	}
}

func (a *App) finishDebate(d *Debate, state, msg string) {
	a.mutateDebate(d, func(d *Debate) {
		now := time.Now()
		d.State, d.Error, d.Ended = state, msg, &now
	})
	s := a.debateStoreOf()
	s.mu.Lock()
	if c := s.cancels[d.ID]; c != nil {
		c()
	}
	delete(s.cancels, d.ID)
	s.mu.Unlock()
}

// runDebate goes through the phases. round is where to pick up: 0 from the
// start, otherwise the one review round to add before concluding again.
func (a *App) runDebate(ctx context.Context, d *Debate, round int) {
	agents := d.Agents
	if round == 0 {
		a.debatePhase(ctx, d, "position", 0, agents)
		if ctx.Err() != nil {
			a.finishDebate(d, "cancelled", "stopped")
			return
		}
		if !hasDone(d, "position") {
			a.finishDebate(d, "failed", "neither agent gave a position")
			return
		}
		for r := 1; r <= d.Rounds; r++ {
			a.debatePhase(ctx, d, "review", r, agents)
			if ctx.Err() != nil {
				a.finishDebate(d, "cancelled", "stopped")
				return
			}
		}
	} else {
		a.debatePhase(ctx, d, "review", round, agents)
		if ctx.Err() != nil {
			a.finishDebate(d, "cancelled", "stopped")
			return
		}
	}
	i := a.beginDebateStep(d, "conclusion", 0, 0, d.JudgeAgent, true)
	out, err := a.runStepOf(ctx, stepReq{Agent: d.JudgeAgent, Model: a.modelFor(d, d.JudgeAgent), Cwd: d.Cwd, ReadOnly: true,
		Prompt: judgePrompt(d), Origin: "debate", OnStart: a.onDebateStart(d, i)})
	a.endDebateStep(d, i, out, err)
	if ctx.Err() != nil {
		a.finishDebate(d, "cancelled", "stopped")
		return
	}
	st := a.debateStepAt(d, i)
	if st.State != "done" {
		a.finishDebate(d, "failed", "the judge did not finish: "+st.Error)
		return
	}
	c := parseConclusion(st.Text, d.Agents)
	c.Judge = d.JudgeAgent
	a.mutateDebate(d, func(d *Debate) { d.Conclusion = &c })
	a.finishDebate(d, "done", "")
}

func (a *App) modelFor(d *Debate, agent string) string {
	for i, ag := range d.Agents {
		if ag == agent && i < len(d.Models) {
			return d.Models[i]
		}
	}
	return ""
}

func (a *App) debateStepAt(d *Debate, i int) DebateStep {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.Steps[i]
}

func (a *App) beginDebateStep(d *Debate, phase string, round, side int, agent string, readOnly bool) int {
	var i int
	a.mutateDebate(d, func(d *Debate) {
		d.Steps = append(d.Steps, DebateStep{ID: uuid.NewString(), Phase: phase, Round: round, Agent: agent, Side: side,
			State: "running", Started: time.Now(), ReadOnly: readOnly && cliCanBeReadOnly(agent)})
		i = len(d.Steps) - 1
	})
	return i
}

func (a *App) onDebateStart(d *Debate, i int) func(CLIRun) {
	return func(r CLIRun) {
		a.mutateDebate(d, func(d *Debate) { d.Steps[i].Run = r.ID })
	}
}

func (a *App) endDebateStep(d *Debate, i int, out stepOut, err error) {
	a.mutateDebate(d, func(d *Debate) {
		st := &d.Steps[i]
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
		case strings.TrimSpace(out.Answer) == "":
			st.State, st.Error = "failed", "it answered nothing"
		default:
			st.State = "done"
			if st.Phase == "review" {
				st.Stances = parseStances(out.Answer)
			}
		}
	})
}

// debatePhase runs one step for each agent at once and waits for them all.
func (a *App) debatePhase(ctx context.Context, d *Debate, phase string, round int, agents []string) {
	var wg sync.WaitGroup
	for idx, agent := range agents {
		other := agents[1-idx]
		var otherText string
		var prev string
		a.debateRead(d, func(d *Debate) {
			otherText = latestAnswer(d, 1-idx)
			prev = latestRun(d, idx)
		})
		if phase == "review" && strings.TrimSpace(otherText) == "" {
			continue // nothing from the other side to check
		}
		var req stepReq
		model := ""
		if idx < len(d.Models) {
			model = d.Models[idx]
		}
		switch phase {
		case "position":
			req = stepReq{Prompt: positionPrompt(d, agent)}
		default:
			req = stepReq{Prompt: reviewPrompt(d, agent, other, otherText, round), Prev: prev,
				Fresh: reviewPrompt(d, agent, other, otherText, round) + "\n\n(You have no earlier session here, so form your own view of the question first.)"}
		}
		req.Agent, req.Model, req.Cwd, req.ReadOnly, req.Origin = agent, model, d.Cwd, true, "debate"
		i := a.beginDebateStep(d, phase, round, idx, agent, true)
		req.OnStart = a.onDebateStart(d, i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := a.runStepOf(ctx, req)
			a.endDebateStep(d, i, out, err)
		}()
	}
	wg.Wait()
}

func (a *App) debateRead(d *Debate, f func(*Debate)) {
	s := a.debateStoreOf()
	s.mu.Lock()
	defer s.mu.Unlock()
	f(d)
}

// latestAnswer is the newest finished answer of one side.
func latestAnswer(d *Debate, side int) string {
	for i := len(d.Steps) - 1; i >= 0; i-- {
		st := d.Steps[i]
		if st.Phase != "conclusion" && st.State == "done" && st.Side == side && strings.TrimSpace(st.Text) != "" {
			return st.Text
		}
	}
	return ""
}

// latestRun is the newest run of one side, to continue its session from.
func latestRun(d *Debate, side int) string {
	for i := len(d.Steps) - 1; i >= 0; i-- {
		st := d.Steps[i]
		if st.Phase != "conclusion" && st.Run != "" && st.Side == side {
			return st.Run
		}
	}
	return ""
}

// ---- prompts ----

func positionPrompt(d *Debate, agent string) string {
	return fmt.Sprintf(`You are one of two engineers independently examining the project in the current directory. Another engineer is doing the same and you will later check each other's work.

%s

The question:
%s

Investigate the code, then answer. Be concrete: cite evidence as path/to/file:LINE. End your answer with a list of your key claims, one per line, each as:
CLAIM: <the claim> (file:line)`, readOnlyNote(agent), d.Topic)
}

func reviewPrompt(d *Debate, agent, other, otherText string, round int) string {
	return fmt.Sprintf(`%s

This is review round %d of a debate about the project in the current directory. The question was:
%s

Here is the latest answer of the other engineer (%s), verbatim:
-----
%s
-----

Do not take it on trust: open the files it cites and check each claim against the code. Say where it is right, where it is wrong or incomplete, and where you now change your own view.

End your reply with a block of lines, each starting with one of these words and a colon:
AGREE: <a claim you checked and hold to be true>
DISAGREE: <a claim> because <the reason, with file:line>
REVISED: <your own updated position, one line>`, readOnlyNote(agent), round, d.Topic, other, cliClip(otherText, 24<<10))
}

func judgePrompt(d *Debate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Two engineers (%s and %s) debated a question about the project in the current directory. You are the judge. %s You may open files to settle a point.\n\nThe question:\n%s\n\nThe transcript:\n", d.Agents[0], d.Agents[1], readOnlyNote(d.JudgeAgent), d.Topic)
	for _, st := range d.Steps {
		if st.Phase == "conclusion" || st.State != "done" {
			continue
		}
		label := "position"
		if st.Phase == "review" {
			label = fmt.Sprintf("review, round %d", st.Round)
		}
		fmt.Fprintf(&b, "\n##### %s — %s\n%s\n", st.Agent, label, cliClip(st.Text, 12<<10))
	}
	fmt.Fprintf(&b, `
Write the conclusion in markdown with exactly these four sections, as headings:

## Verdict
The answer to the question, in a short paragraph.

## Agreed
- one bullet per point both sides hold, with evidence

## Disagreements
- one bullet per point they still differ on, each as: <the point> | right: <%s, %s or unresolved> | <why, with file:line evidence>

## Next steps
- one bullet per concrete action, in order

Keep every bullet to one or two sentences, and the verdict to a short paragraph.`, d.Agents[0], d.Agents[1])
	return b.String()
}
