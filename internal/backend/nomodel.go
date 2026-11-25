package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
)

// ErrNoModel is what anything that needs SuperLeo's own model gets back when
// the service was built without one: a CLI queen engine is thinking for the
// person, and there is no API key behind the agent.
var ErrNoModel = errors.New("no model of its own: add an API model under Settings, or let a CLI engine answer")

// HasModel is whether these settings name a complete API model.
func (s *Settings) HasModel() bool {
	return s != nil &&
		strings.TrimSpace(s.LLMKey) != "" &&
		strings.TrimSpace(s.LLMBaseURL) != "" &&
		strings.TrimSpace(s.LLMModel) != ""
}

// EngineThinks is whether a CLI queen engine answers the person's words, so
// the service can be built, and be useful, without a model of its own.
func (s *Settings) EngineThinks() bool {
	if s == nil || s.Hive.Role == HiveRoleWorker {
		return false
	}
	for _, e := range s.Hive.QueenEnginesOn() {
		if e != QueenEngineSuperLeo {
			return true
		}
	}
	return false
}

// EngineTurnFunc runs one prompt through the CLI queen engine and returns its
// answer. session names the conversation it belongs to ("" for none). The app
// supplies it (queen_engine_oneshot.go); a service built without a model of
// its own thinks through it.
type EngineTurnFunc func(ctx context.Context, session, prompt string) (string, error)

// engineHook is the engine a no-model service thinks through, set after the
// service is built, and read by whatever runs on its generator.
type engineHook struct {
	fn atomic.Pointer[EngineTurnFunc]
}

func (h *engineHook) get() EngineTurnFunc {
	if h == nil {
		return nil
	}
	if p := h.fn.Load(); p != nil {
		return *p
	}
	return nil
}

// HasModel is whether the agent has a model to run on. False for a service
// built for a CLI engine alone; every entry point that would call the model
// checks it first, so nothing spins on a generator that cannot answer.
func (s *Service) HasModel() bool { return s != nil && !s.noModel }

// SetEngine gives a service with no model of its own the CLI engine to think
// through: agent runs (scheduled prompts, standing agents, channels, dashboards,
// long runs) then end in one engine turn, and one-shot text jobs go to it.
// A service with a model ignores it.
func (s *Service) SetEngine(fn EngineTurnFunc) {
	if s == nil || s.engine == nil {
		return
	}
	if fn == nil {
		s.engine.fn.Store(nil)
		return
	}
	s.engine.fn.Store(&fn)
}

// CanThink is whether anything here can answer a prompt: the service's own
// model, or the CLI engine standing in for it.
func (s *Service) CanThink() bool {
	return s != nil && (!s.noModel || s.engine.get() != nil)
}

// oneShot asks for one text answer: the own model when there is one, the
// engine otherwise. Only the engine side is used by a no-model service; with a
// model the callers keep to their own generators.
func (s *Service) oneShot(ctx context.Context, session, prompt string) (string, error) {
	if s == nil {
		return "", ErrNoModel
	}
	fn := s.engine.get()
	if fn == nil {
		return "", ErrNoModel
	}
	return fn(ctx, session, prompt)
}

// noModelLLM stands in for the brain when there is none. agent-go wants a
// generator to build. Its tool-calling entry points, which are the agent loop,
// answer with the engine's reply to the last thing said, as a final answer with
// no tool calls (the engine has the tools itself); everything else refuses.
type noModelLLM struct{ engine *engineHook }

func (noModelLLM) Generate(context.Context, string, *domain.GenerationOptions) (string, error) {
	return "", ErrNoModel
}

func (noModelLLM) Stream(context.Context, string, *domain.GenerationOptions, func(string)) error {
	return ErrNoModel
}

// lastUser is the newest thing the person (or the schedule) said in a loop's
// messages: all the engine is handed, since its own session holds the rest.
func lastUser(messages []domain.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

func (l noModelLLM) GenerateWithTools(ctx context.Context, messages []domain.Message, _ []domain.ToolDefinition, _ *domain.GenerationOptions) (*domain.GenerationResult, error) {
	fn := l.engine.get()
	if fn == nil {
		return nil, ErrNoModel
	}
	prompt := lastUser(messages)
	if strings.TrimSpace(prompt) == "" {
		return nil, ErrNoModel
	}
	out, err := fn(ctx, ChatSessionFrom(ctx), prompt)
	if err != nil {
		return nil, err
	}
	return &domain.GenerationResult{Content: out, Model: "queen-engine", Provider: "cli"}, nil
}

func (l noModelLLM) StreamWithTools(ctx context.Context, messages []domain.Message, tools []domain.ToolDefinition, opts *domain.GenerationOptions, cb domain.ToolCallCallback) error {
	res, err := l.GenerateWithTools(ctx, messages, tools, opts)
	if err != nil {
		return err
	}
	if cb != nil {
		return cb(res)
	}
	return nil
}

func (noModelLLM) GenerateStructured(context.Context, string, interface{}, *domain.GenerationOptions) (*domain.StructuredResult, error) {
	return nil, ErrNoModel
}

func (noModelLLM) RecognizeIntent(context.Context, string) (*domain.IntentResult, error) {
	return nil, ErrNoModel
}

// streamLongOnEngine is a long run with no model of its own: the goal is one
// engine turn, and the CLI does its own working through it in one go. A task
// that needs the supervisor's segments needs an API model.
func (s *Service) streamLongOnEngine(ctx context.Context, goal string, o LongRunOptions, emit func(ev *agent.Event)) (*LongRunReport, error) {
	if s.engine.get() == nil {
		return nil, ErrNoModel
	}
	session := "long-run"
	if o.TaskID != "" {
		session = "long-run:" + o.TaskID
	}
	began := time.Now()
	out, err := s.oneShot(WithChatSession(ctx, session), session, goal)
	if err != nil {
		return nil, err
	}
	if emit != nil {
		emit(&agent.Event{Type: agent.EventTypeComplete, Content: out})
	}
	return &LongRunReport{TaskID: o.TaskID, Done: true, Stop: "done", Segments: 1, Text: out,
		Duration: time.Since(began).Round(time.Second).String()}, nil
}

// extractJSONOnEngine is extractJSON for a service with no model: the CLI
// engine is asked for the one JSON object.
func (s *Service) extractJSONOnEngine(ctx context.Context, prompt string, schema map[string]interface{}, out interface{}) error {
	sch, _ := json.Marshal(schema)
	ask := prompt + "\n\nAnswer with one JSON object only, no prose and no code fence, matching this JSON schema:\n" + string(sch)
	raw, err := s.oneShot(ctx, "", ask)
	if err != nil {
		return err
	}
	body := strings.TrimSpace(raw)
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	if body == "" {
		return errors.New("the engine returned nothing")
	}
	return json.Unmarshal([]byte(body), out)
}
