// Command e2e exercises the GCP OpenTelemetry plugin against a real Temporal namespace.
//
// Run it in worker mode inside a Cloud Run worker pool, or in starter mode from a trusted
// environment that can access the same Temporal namespace. Credentials are read from an
// environment variable or file and are never logged.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/gcp"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	workerMode   = "worker"
	starterMode  = "starter"
	workflowName = "GCPPluginE2EWorkflow"
)

type config struct {
	mode          string
	address       string
	namespace     string
	taskQueue     string
	apiKey        string
	shutdownAfter time.Duration
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Printf("e2e_failed error=%q", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	switch cfg.mode {
	case workerMode:
		return runWorker(ctx, cfg)
	case starterMode:
		return runStarter(ctx, cfg)
	default:
		return fmt.Errorf("unsupported E2E_MODE %q", cfg.mode)
	}
}

func loadConfig() (config, error) {
	cfg := config{
		mode:      envOrDefault("E2E_MODE", workerMode),
		address:   os.Getenv("TEMPORAL_ADDRESS"),
		namespace: os.Getenv("TEMPORAL_NAMESPACE"),
		taskQueue: os.Getenv("TEMPORAL_TASK_QUEUE"),
	}
	for name, value := range map[string]string{
		"TEMPORAL_ADDRESS":    cfg.address,
		"TEMPORAL_NAMESPACE":  cfg.namespace,
		"TEMPORAL_TASK_QUEUE": cfg.taskQueue,
	} {
		if strings.TrimSpace(value) == "" {
			return config{}, fmt.Errorf("%s is required", name)
		}
	}

	apiKey := os.Getenv("TEMPORAL_API_KEY")
	if path := os.Getenv("TEMPORAL_API_KEY_FILE"); path != "" {
		contents, err := os.ReadFile(path)
		if err != nil {
			return config{}, fmt.Errorf("reading TEMPORAL_API_KEY_FILE: %w", err)
		}
		apiKey = string(contents)
	}
	cfg.apiKey = strings.TrimSpace(apiKey)
	if cfg.apiKey == "" {
		return config{}, errors.New("TEMPORAL_API_KEY or TEMPORAL_API_KEY_FILE is required")
	}
	if value := os.Getenv("E2E_SHUTDOWN_AFTER"); value != "" {
		shutdownAfter, err := time.ParseDuration(value)
		if err != nil || shutdownAfter <= 0 {
			return config{}, fmt.Errorf("E2E_SHUTDOWN_AFTER must be a positive duration")
		}
		cfg.shutdownAfter = shutdownAfter
	}
	return cfg, nil
}

func runWorker(ctx context.Context, cfg config) error {
	if err := waitForCollector(ctx, "127.0.0.1:4317", 60*time.Second); err != nil {
		return err
	}
	log.Printf("collector_ready endpoint=%q", gcp.DefaultOTLPEndpoint)

	otelPlugin, err := gcp.NewOpenTelemetryPlugin(ctx, gcp.OpenTelemetryPluginOptions{})
	if err != nil {
		return fmt.Errorf("creating GCP OpenTelemetry plugin: %w", err)
	}
	log.Printf(
		"plugin_ready service_name=%q endpoint=%q",
		otelPlugin.ServiceName(),
		otelPlugin.Endpoint(),
	)

	temporalClient, err := client.DialContext(ctx, client.Options{
		HostPort:    cfg.address,
		Namespace:   cfg.namespace,
		Credentials: client.NewAPIKeyStaticCredentials(cfg.apiKey),
		Plugins:     []client.Plugin{otelPlugin},
	})
	if err != nil {
		shutdownPlugin(otelPlugin)
		return fmt.Errorf("dialing Temporal: %w", err)
	}

	temporalWorker := worker.New(temporalClient, cfg.taskQueue, worker.Options{})
	temporalWorker.RegisterWorkflowWithOptions(e2eWorkflow, workflow.RegisterOptions{Name: workflowName})
	temporalWorker.RegisterActivity(e2eActivity)
	if err := temporalWorker.Start(); err != nil {
		temporalClient.Close()
		shutdownPlugin(otelPlugin)
		return fmt.Errorf("starting worker: %w", err)
	}
	log.Printf("worker_ready namespace=%q task_queue=%q", cfg.namespace, cfg.taskQueue)

	signalCtx, stopSignals := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	shutdownReason := "signal"
	if cfg.shutdownAfter > 0 {
		timer := time.NewTimer(cfg.shutdownAfter)
		defer timer.Stop()
		select {
		case <-signalCtx.Done():
		case <-timer.C:
			shutdownReason = "timer"
		}
	} else {
		<-signalCtx.Done()
	}
	log.Printf("shutdown_started reason=%q", shutdownReason)

	temporalWorker.Stop()
	log.Print("worker_stopped")
	temporalClient.Close()
	log.Print("temporal_client_closed")

	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 3*time.Second)
	flushErr := otelPlugin.ForceFlush(flushCtx)
	cancelFlush()
	if flushErr != nil {
		log.Printf("plugin_force_flush_failed error=%q", flushErr)
	} else {
		log.Print("plugin_force_flush_complete")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	shutdownErr := otelPlugin.Shutdown(shutdownCtx)
	cancelShutdown()
	if shutdownErr != nil {
		log.Printf("plugin_shutdown_failed error=%q", shutdownErr)
	} else {
		log.Print("plugin_shutdown_complete")
	}
	return errors.Join(flushErr, shutdownErr)
}

func runStarter(ctx context.Context, cfg config) error {
	temporalClient, err := client.DialContext(ctx, client.Options{
		HostPort:    cfg.address,
		Namespace:   cfg.namespace,
		Credentials: client.NewAPIKeyStaticCredentials(cfg.apiKey),
	})
	if err != nil {
		return fmt.Errorf("dialing Temporal: %w", err)
	}
	defer temporalClient.Close()

	workflowID := fmt.Sprintf("sdk-go-gcp-otel-e2e-%d", time.Now().UTC().UnixNano())
	input := "cloud-run-worker-pool"
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: cfg.taskQueue,
	}, workflowName, input)
	if err != nil {
		return fmt.Errorf("starting workflow: %w", err)
	}
	log.Printf("workflow_started workflow_id=%q run_id=%q", run.GetID(), run.GetRunID())

	var result string
	if err := run.Get(ctx, &result); err != nil {
		return fmt.Errorf("waiting for workflow: %w", err)
	}
	want := "sdk-go-otel-e2e:" + input
	if result != want {
		return fmt.Errorf("unexpected workflow result: got %q, want %q", result, want)
	}
	log.Printf(
		"workflow_completed workflow_id=%q run_id=%q result=%q",
		run.GetID(),
		run.GetRunID(),
		result,
	)
	return nil
}

func e2eWorkflow(ctx workflow.Context, input string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})
	var result string
	if err := workflow.ExecuteActivity(ctx, e2eActivity, input).Get(ctx, &result); err != nil {
		return "", err
	}
	return result, nil
}

func e2eActivity(ctx context.Context, input string) (string, error) {
	activity.GetLogger(ctx).Info("E2E activity completed")
	return "sdk-go-otel-e2e:" + input, nil
}

func waitForCollector(ctx context.Context, address string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(waitCtx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("waiting for collector at %s: %w", address, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func shutdownPlugin(otelPlugin *gcp.OpenTelemetryPlugin) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := otelPlugin.Shutdown(shutdownCtx); err != nil {
		log.Printf("plugin_shutdown_failed error=%q", err)
	}
}

func envOrDefault(name string, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}
