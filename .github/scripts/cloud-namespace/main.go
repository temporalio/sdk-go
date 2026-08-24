// cloud-namespace creates and deletes isolated Temporal Cloud namespaces for CI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cloudservice "go.temporal.io/cloud-sdk/api/cloudservice/v1"
	cloudnamespace "go.temporal.io/cloud-sdk/api/namespace/v1"
	cloudoperation "go.temporal.io/cloud-sdk/api/operation/v1"
	"go.temporal.io/cloud-sdk/cloudclient"
)

const (
	cloudNamespaceRegion = "aws-ca-central-1"
	commandTimeout       = 10 * time.Minute
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if !((len(args) == 1 && args[0] == "create") || (len(args) == 2 && args[0] == "delete")) {
		return errors.New("usage: cloud-namespace create|delete <namespace>")
	}
	apiKey, err := requireEnv("TEMPORAL_CLIENT_CLOUD_API_KEY")
	if err != nil {
		return err
	}
	apiVersion, err := requireEnv("TEMPORAL_CLIENT_CLOUD_API_VERSION")
	if err != nil {
		return err
	}
	client, err := cloudclient.New(cloudclient.Options{APIKey: apiKey, APIVersion: apiVersion})
	if err != nil {
		return fmt.Errorf("create Cloud Operations client: %w", err)
	}
	defer func() { _ = client.Close() }()
	if args[0] == "create" {
		return create(ctx, client.CloudService())
	}
	return deleteNamespace(ctx, client.CloudService(), args[1])
}

func create(ctx context.Context, client cloudservice.CloudServiceClient) error {
	runID, err := requireEnv("GITHUB_RUN_ID")
	if err != nil {
		return err
	}
	runAttempt, err := requireEnv("GITHUB_RUN_ATTEMPT")
	if err != nil {
		return err
	}
	jobIndex, err := requireEnv("GITHUB_JOB_INDEX")
	if err != nil {
		return err
	}
	namespace := fmt.Sprintf("sdk-go-ci-%s-%s-%s", runID, runAttempt, jobIndex)
	caPath, err := requireEnv("TEMPORAL_CLOUD_CLIENT_CA_PATH")
	if err != nil {
		return err
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read client CA: %w", err)
	}
	result, err := client.CreateNamespace(ctx, &cloudservice.CreateNamespaceRequest{
		Spec: &cloudnamespace.NamespaceSpec{
			Name:          namespace,
			RetentionDays: 1,
			MtlsAuth:      &cloudnamespace.MtlsAuthSpec{AcceptedClientCa: ca, Enabled: true},
			Replicas:      []*cloudnamespace.ReplicaSpec{{Region: cloudNamespaceRegion}},
		},
	})
	if err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}
	if output := os.Getenv("GITHUB_OUTPUT"); output != "" {
		file, err := os.OpenFile(filepath.Clean(output), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
		}
		if _, err = fmt.Fprintf(file, "namespace=%s\n", result.Namespace); err != nil {
			file.Close()
			return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
		}
		if err = file.Close(); err != nil {
			return fmt.Errorf("close GITHUB_OUTPUT: %w", err)
		}
	}
	return waitForOperation(ctx, client, result.AsyncOperation)
}

func deleteNamespace(ctx context.Context, client cloudservice.CloudServiceClient, namespace string) error {
	existing, err := client.GetNamespace(ctx, &cloudservice.GetNamespaceRequest{Namespace: namespace})
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	result, err := client.DeleteNamespace(ctx, &cloudservice.DeleteNamespaceRequest{
		Namespace:       namespace,
		ResourceVersion: existing.Namespace.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	return waitForOperation(ctx, client, result.AsyncOperation)
}

func waitForOperation(ctx context.Context, client cloudservice.CloudServiceClient, operation *cloudoperation.AsyncOperation) error {
	for {
		result, err := client.GetAsyncOperation(ctx, &cloudservice.GetAsyncOperationRequest{AsyncOperationId: operation.Id})
		if err != nil {
			return fmt.Errorf("get Cloud operation %s: %w", operation.Id, err)
		}
		operation = result.AsyncOperation
		switch operation.State {
		case cloudoperation.AsyncOperation_STATE_FULFILLED:
			return nil
		case cloudoperation.AsyncOperation_STATE_FAILED,
			cloudoperation.AsyncOperation_STATE_CANCELLED,
			cloudoperation.AsyncOperation_STATE_REJECTED:
			return fmt.Errorf("Cloud operation %s %s: %s", operation.Id, operation.State.String(), operation.FailureReason)
		}
		delay := operation.CheckDuration.AsDuration()
		if delay < time.Second {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Cloud operation %s: %w", operation.Id, ctx.Err())
		case <-time.After(delay):
		}
	}
}

func requireEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
