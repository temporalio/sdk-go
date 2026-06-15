package google_adk_agents_test

import (
	"context"

	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// newAgentFactory returns an AgentFactory that wires a real adk-go agent and
// runner around the given model. A fresh in-memory session service is built per
// turn (per Activity invocation), exactly as a production factory would isolate
// per-turn state. This is 100% native adk-go: the plugin reconstructs it
// worker-side from the agent name alone.
func newAgentFactory(name string, m model.LLM) adk.AgentFactory {
	return func(_ context.Context) (*adk.AgentRunner, error) {
		ag, err := llmagent.New(llmagent.Config{
			Name:        name,
			Description: "integration-test agent",
			Model:       m,
		})
		if err != nil {
			return nil, err
		}
		svc := session.InMemoryService()
		r, err := runner.New(runner.Config{AppName: name, Agent: ag, SessionService: svc})
		if err != nil {
			return nil, err
		}
		return &adk.AgentRunner{Runner: r, SessionService: svc, AppName: name}, nil
	}
}

// userMessage builds a user-role content with a single text part.
func userMessage(text string) *genai.Content {
	return genai.NewContentFromText(text, genai.RoleUser)
}

// modelMessage builds a model-role content the MockModel will reply with.
func modelMessage(text string) *genai.Content {
	return genai.NewContentFromText(text, genai.RoleModel)
}

// containsText reports whether any event carries the given text in a part.
func containsText(events []*session.Event, want string) bool {
	for _, ev := range events {
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text == want {
				return true
			}
		}
	}
	return false
}
