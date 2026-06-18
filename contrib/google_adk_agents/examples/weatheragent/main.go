// Copyright 2026 Google LLC, Temporal Technologies Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

// Command weatheragent is a runnable example: a Temporal worker + workflow that
// drives a real Google ADK gemini agent (with a function tool) through the
// google_adk_agents plugin. The ADK runner loop runs durably inside the workflow,
// while every model and tool call is executed worker-side as a Temporal Activity.
//
// Run it:
//
//  1. Start a local Temporal dev server:
//     temporal server start-dev
//  2. Get a Gemini API key from https://aistudio.google.com/apikey, then:
//     export GEMINI_API_KEY=...
//  3. From this directory:
//     go run .
//
// Expected output is a one-line answer the gemini model produced after calling the
// get_weather tool (dispatched as a Temporal Activity).
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	googleadk "go.temporal.io/sdk/contrib/google_adk_agents"
)

const (
	taskQueue = "adk-weather-example"
	modelName = "gemini-2.5-flash"
)

// newWeatherTool builds a plain ADK function tool. The plugin runs it worker-side
// inside the CallTool Activity; the model only ever sees its declaration.
func newWeatherTool() (tool.Tool, error) {
	return functiontool.New[map[string]any, map[string]any](
		functiontool.Config{Name: "get_weather", Description: "Look up the current weather for a city."},
		func(_ agent.ToolContext, args map[string]any) (map[string]any, error) {
			city, _ := args["city"].(string)
			// A real implementation would call a weather API. This handler runs
			// worker-side (inside an Activity), so network I/O is fine here.
			return map[string]any{"city": city, "forecast": "sunny", "tempC": 24}, nil
		},
	)
}

// WeatherWorkflow runs the ADK agent loop durably. Every model and tool call is
// short-circuited by the plugin into a Temporal Activity, so nothing
// non-deterministic runs in the workflow itself.
func WeatherWorkflow(ctx workflow.Context, question string) (string, error) {
	weather, err := newWeatherTool()
	if err != nil {
		return "", err
	}
	pl, err := googleadk.Plugin(googleadk.Options{TaskQueue: taskQueue})
	if err != nil {
		return "", err
	}

	// In-workflow the agent only needs a model that reports the right Name(): the
	// plugin intercepts the actual model call and runs it worker-side via the
	// ModelFactory (see main), where the API key lives. NewFakeModel().WithName is
	// a lightweight, deterministic name-carrying stand-in for the workflow side, so
	// no real model client is constructed inside the workflow.
	root, err := llmagent.New(llmagent.Config{
		Name:        "weather_assistant",
		Description: "answers weather questions",
		Model:       googleadk.NewFakeModel().WithName(modelName),
		Instruction: "Use get_weather to answer questions about the weather.",
		Tools:       []tool.Tool{weather},
	})
	if err != nil {
		return "", err
	}

	r, err := runner.New(runner.Config{
		AppName:           "weather-example",
		Agent:             root,
		SessionService:    session.InMemoryService(),
		PluginConfig:      runner.PluginConfig{Plugins: []*plugin.Plugin{pl}},
		AutoCreateSession: true,
	})
	if err != nil {
		return "", err
	}

	msg := genai.NewContentFromText(question, genai.RoleUser)
	var answer string
	for ev, rerr := range r.Run(googleadk.NewContext(ctx), "user-1", "session-1", msg, agent.RunConfig{}) {
		if rerr != nil {
			return "", rerr
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text != "" {
				answer = p.Text
			}
		}
	}
	return answer, nil
}

func main() {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("set GEMINI_API_KEY (get one at https://aistudio.google.com/apikey)")
	}

	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalf("dial Temporal (is `temporal server start-dev` running?): %v", err)
	}
	defer c.Close()

	// Worker-side registry: the real gemini model (with credentials) and the real
	// tool handler live here, behind the Activity boundary.
	weather, err := newWeatherTool()
	if err != nil {
		log.Fatal(err)
	}
	acts, err := googleadk.NewActivities(googleadk.Config{
		Models: map[string]googleadk.ModelFactory{
			modelName: func(ctx context.Context, name string) (model.LLM, error) {
				return gemini.NewModel(ctx, name, &genai.ClientConfig{APIKey: apiKey})
			},
		},
		Tools: []tool.Tool{weather},
	})
	if err != nil {
		log.Fatal(err)
	}

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(WeatherWorkflow)
	acts.Register(w)
	if err := w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	run, err := c.ExecuteWorkflow(context.Background(),
		client.StartWorkflowOptions{TaskQueue: taskQueue},
		WeatherWorkflow, "What's the weather in Paris?")
	if err != nil {
		log.Fatal(err)
	}
	var answer string
	if err := run.Get(context.Background(), &answer); err != nil {
		log.Fatal(err)
	}
	fmt.Println("Agent answer:", answer)
}
