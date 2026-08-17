// cloud-namespace creates and deletes isolated Temporal Cloud namespaces for CI.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cloudservicev1grpc "buf.build/gen/go/temporalio/cloud-api/grpc/go/temporal/api/cloud/cloudservice/v1/cloudservicev1grpc"
	cloudservicev1 "buf.build/gen/go/temporalio/cloud-api/protocolbuffers/go/temporal/api/cloud/cloudservice/v1"
	namespacev1 "buf.build/gen/go/temporalio/cloud-api/protocolbuffers/go/temporal/api/cloud/namespace/v1"
	operationv1 "buf.build/gen/go/temporalio/cloud-api/protocolbuffers/go/temporal/api/cloud/operation/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const (
	cloudAPIAddress  = "saas-api.tmprl.cloud:443"
	operationTimeout = 10 * time.Minute
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	switch {
	case len(os.Args) == 2 && os.Args[1] == "create":
		return create(ctx)
	case len(os.Args) == 3 && os.Args[1] == "delete":
		return deleteNamespace(ctx, os.Args[2])
	default:
		return errors.New("usage: cloud-namespace create|delete <namespace>")
	}
}

func cloudClient() (cloudservicev1grpc.CloudServiceClient, func(), error) {
	apiKey, err := requireEnv("TEMPORAL_CLIENT_CLOUD_API_KEY")
	if err != nil {
		return nil, nil, err
	}
	apiVersion, err := requireEnv("TEMPORAL_CLIENT_CLOUD_API_VERSION")
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(
		cloudAPIAddress,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
		grpc.WithUnaryInterceptor(func(
			ctx context.Context,
			method string,
			req, reply any,
			cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption,
		) error {
			ctx = metadata.AppendToOutgoingContext(
				ctx,
				"authorization", "Bearer "+apiKey,
				"temporal-cloud-api-version", apiVersion,
			)
			return invoker(ctx, method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial Cloud Operations API: %w", err)
	}
	return cloudservicev1grpc.NewCloudServiceClient(conn), func() { _ = conn.Close() }, nil
}

func create(ctx context.Context) error {
	client, closeClient, err := cloudClient()
	if err != nil {
		return err
	}
	defer closeClient()
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
	result, err := client.CreateNamespace(ctx, &cloudservicev1.CreateNamespaceRequest{
		Spec: &namespacev1.NamespaceSpec{
			Name:          namespace,
			Regions:       []string{"aws-ca-central-1"},
			RetentionDays: 1,
			MtlsAuth:      &namespacev1.MtlsAuthSpec{AcceptedClientCa: ca, Enabled: true},
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

func deleteNamespace(ctx context.Context, namespace string) error {
	client, closeClient, err := cloudClient()
	if err != nil {
		return err
	}
	defer closeClient()
	existing, err := client.GetNamespace(ctx, &cloudservicev1.GetNamespaceRequest{Namespace: namespace})
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	result, err := client.DeleteNamespace(ctx, &cloudservicev1.DeleteNamespaceRequest{
		Namespace:       namespace,
		ResourceVersion: existing.Namespace.ResourceVersion,
	})
	if err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	return waitForOperation(ctx, client, result.AsyncOperation)
}

func waitForOperation(ctx context.Context, client cloudservicev1grpc.CloudServiceClient, operation *operationv1.AsyncOperation) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for {
		result, err := client.GetAsyncOperation(ctx, &cloudservicev1.GetAsyncOperationRequest{AsyncOperationId: operation.Id})
		if err != nil {
			return fmt.Errorf("get Cloud operation %s: %w", operation.Id, err)
		}
		operation = result.AsyncOperation
		switch operation.State {
		case operationv1.AsyncOperation_STATE_FULFILLED:
			return nil
		case operationv1.AsyncOperation_STATE_FAILED,
			operationv1.AsyncOperation_STATE_CANCELLED,
			operationv1.AsyncOperation_STATE_REJECTED:
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
