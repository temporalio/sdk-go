package google_adk_agents

import (
	"context"
	"iter"
	"sync"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// MockModel is a deterministic, network-free model.LLM for unit-testing
// workflows and agent factories that use this plugin. It returns its
// configured responses in order, one per GenerateContent call, then repeats
// the final response. Construct one in an AgentFactory to drive a real
// adk-go runner without calling a billed model API.
//
// MockModel is safe for concurrent use within a single turn Activity.
type MockModel struct {
	name      string
	responses []*model.LLMResponse

	mu    sync.Mutex
	calls int
}

// NewMockModel builds a MockModel that yields each of contents (as a model
// response) in turn. With no contents it yields an empty model message.
func NewMockModel(name string, contents ...*genai.Content) *MockModel {
	resps := make([]*model.LLMResponse, len(contents))
	for i, c := range contents {
		resps[i] = &model.LLMResponse{Content: c, TurnComplete: true}
	}
	return &MockModel{name: name, responses: resps}
}

// NewMockModelWithResponses builds a MockModel from full model responses,
// for tests that need to set Partial, function calls, or usage metadata.
func NewMockModelWithResponses(name string, responses ...*model.LLMResponse) *MockModel {
	return &MockModel{name: name, responses: responses}
}

// Name implements model.LLM.
func (m *MockModel) Name() string {
	if m.name == "" {
		return "mock-model"
	}
	return m.name
}

// GenerateContent implements model.LLM by replaying the configured responses.
func (m *MockModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		idx := m.calls
		m.calls++
		m.mu.Unlock()

		switch {
		case len(m.responses) == 0:
			yield(&model.LLMResponse{
				Content:      genai.NewContentFromText("", genai.RoleModel),
				TurnComplete: true,
			}, nil)
		case idx < len(m.responses):
			yield(m.responses[idx], nil)
		default:
			yield(m.responses[len(m.responses)-1], nil)
		}
	}
}

// Calls reports how many times GenerateContent has been invoked.
func (m *MockModel) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

var _ model.LLM = (*MockModel)(nil)
