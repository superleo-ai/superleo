package backend

import (
	"context"
	"errors"
	"strings"

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

// HasModel is whether the agent has a model to run on. False for a service
// built for a CLI engine alone; every entry point that would call the model
// checks it first, so nothing spins on a generator that cannot answer.
func (s *Service) HasModel() bool { return s != nil && !s.noModel }

// noModelLLM stands in for the brain when there is none. agent-go wants a
// generator to build, and it is never called: the entry points refuse first.
type noModelLLM struct{}

func (noModelLLM) Generate(context.Context, string, *domain.GenerationOptions) (string, error) {
	return "", ErrNoModel
}

func (noModelLLM) Stream(context.Context, string, *domain.GenerationOptions, func(string)) error {
	return ErrNoModel
}

func (noModelLLM) GenerateWithTools(context.Context, []domain.Message, []domain.ToolDefinition, *domain.GenerationOptions) (*domain.GenerationResult, error) {
	return nil, ErrNoModel
}

func (noModelLLM) StreamWithTools(context.Context, []domain.Message, []domain.ToolDefinition, *domain.GenerationOptions, domain.ToolCallCallback) error {
	return ErrNoModel
}

func (noModelLLM) GenerateStructured(context.Context, string, interface{}, *domain.GenerationOptions) (*domain.StructuredResult, error) {
	return nil, ErrNoModel
}

func (noModelLLM) RecognizeIntent(context.Context, string) (*domain.IntentResult, error) {
	return nil, ErrNoModel
}
