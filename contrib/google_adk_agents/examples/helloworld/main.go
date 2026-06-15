// Command helloworld is the runnable form of the plugin README's hello-world:
// it registers a native adk-go agent, starts a Temporal worker, and drives one
// durable turn. Set GOOGLE_API_KEY and run a Temporal dev server
// (`temporal server start-dev`) before running it.
package main

import (
	"context"
	"log"
	"os"

	"go.temporal.io/sdk/client"
	adk "go.temporal.io/sdk/contrib/google_adk_agents"
	"go.temporal.io/sdk/worker"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	agentName = "assistant"
	taskQueue = "adk-agents"
)

// newAssistant is 100% native adk-go: it builds the model, agent, and runner.
// API keys live here, in the worker process — never in workflow or Activity
// inputs. The plugin calls this once per turn from inside the turn Activity.
func newAssistant() adk.AgentFactory {
	return func(ctx context.Context) (*adk.AgentRunner, error) {
		m, err := gemini.NewModel(ctx, "gemini-2.0-flash", &genai.ClientConfig{
			APIKey:  os.Getenv("GOOGLE_API_KEY"),
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, err
		}
		ag, err := llmagent.New(llmagent.Config{
			Name:        agentName,
			Description: "a helpful assistant",
			Model:       m,
		})
		if err != nil {
			return nil, err
		}
		svc := session.InMemoryService()
		r, err := runner.New(runner.Config{AppName: agentName, Agent: ag, SessionService: svc})
		if err != nil {
			return nil, err
		}
		return &adk.AgentRunner{Runner: r, SessionService: svc, AppName: agentName}, nil
	}
}

func main() {
	ctx := context.Background()

	// Pass the plugin's data converter so genai/session types round-trip
	// losslessly and stay readable in workflow history.
	c, err := client.Dial(client.Options{DataConverter: adk.NewDataConverter()})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	reg := adk.NewAgentRegistry()
	reg.Register(agentName, newAssistant())

	w := worker.New(c, taskQueue, worker.Options{})
	adk.RegisterActivities(w, reg, adk.Options{})
	adk.RegisterWorkflow(w)
	if err := w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	// Start a durable session and send one turn, awaiting its result.
	in := adk.NewAgentSessionInput(adk.Options{}, agentName, agentName, "user-1", "session-1")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{TaskQueue: taskQueue}, adk.AgentSessionWorkflow, in)
	if err != nil {
		log.Fatal(err)
	}

	handle, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   run.GetID(),
		UpdateName:   adk.UpdateSendMessageAndWait,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args: []interface{}{adk.TurnRequest{
			Message: genai.NewContentFromText("Say hello in one sentence.", genai.RoleUser),
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	var res adk.TurnResult
	if err := handle.Get(ctx, &res); err != nil {
		log.Fatal(err)
	}

	for _, ev := range res.Events {
		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" {
					log.Printf("%s: %s", ev.Author, p.Text)
				}
			}
		}
	}
}
