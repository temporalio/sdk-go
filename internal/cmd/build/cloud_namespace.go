package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	cloudservice "go.temporal.io/cloud-sdk/api/cloudservice/v1"
	cloudnamespace "go.temporal.io/cloud-sdk/api/namespace/v1"
	cloudoperation "go.temporal.io/cloud-sdk/api/operation/v1"
	"go.temporal.io/cloud-sdk/cloudclient"
	"google.golang.org/grpc"
)

const (
	cloudNamespaceAPIVersion       = "v0.19.1"
	cloudNamespaceRegion           = "aws-ca-central-1"
	cloudOperationTimeout          = 10 * time.Minute
	minimumCloudOperationPollDelay = time.Second
)

const cloudNamespaceUsage = "cloud-namespace create <name> | cloud-namespace delete <namespace>"

type cloudNamespaceService interface {
	CreateNamespace(context.Context, *cloudservice.CreateNamespaceRequest, ...grpc.CallOption) (*cloudservice.CreateNamespaceResponse, error)
	GetAsyncOperation(context.Context, *cloudservice.GetAsyncOperationRequest, ...grpc.CallOption) (*cloudservice.GetAsyncOperationResponse, error)
	GetNamespace(context.Context, *cloudservice.GetNamespaceRequest, ...grpc.CallOption) (*cloudservice.GetNamespaceResponse, error)
	DeleteNamespace(context.Context, *cloudservice.DeleteNamespaceRequest, ...grpc.CallOption) (*cloudservice.DeleteNamespaceResponse, error)
}

type cloudNamespaceClientFactory func(cloudclient.Options) (cloudNamespaceService, io.Closer, error)
type cloudOperationSleeper func(context.Context, time.Duration) error

func newCloudNamespaceClient(options cloudclient.Options) (cloudNamespaceService, io.Closer, error) {
	client, err := cloudclient.New(options)
	if err != nil {
		return nil, nil, err
	}
	return client.CloudService(), client, nil
}

func runCloudNamespaceCommand(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	newClient cloudNamespaceClientFactory,
) error {
	if len(args) != 2 || (args[0] != "create" && args[0] != "delete") {
		return fmt.Errorf("usage: %s", cloudNamespaceUsage)
	}
	resourceName := strings.TrimSpace(args[1])
	if resourceName == "" {
		return fmt.Errorf("usage: %s", cloudNamespaceUsage)
	}
	apiKey := strings.TrimSpace(getenv("TEMPORAL_CLIENT_CLOUD_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("TEMPORAL_CLIENT_CLOUD_API_KEY is required")
	}

	var acceptedClientCA []byte
	var output *os.File
	if args[0] == "create" {
		caPath := strings.TrimSpace(getenv("TEMPORAL_CLOUD_CLIENT_CA_PATH"))
		if caPath == "" {
			return fmt.Errorf("TEMPORAL_CLOUD_CLIENT_CA_PATH is required")
		}
		var err error
		acceptedClientCA, err = os.ReadFile(caPath)
		if err != nil {
			return fmt.Errorf("read Cloud client CA: %w", err)
		}
		outputPath := strings.TrimSpace(getenv("GITHUB_OUTPUT"))
		if outputPath == "" {
			return fmt.Errorf("GITHUB_OUTPUT is required")
		}
		output, err = os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
		}
		defer func() { _ = output.Close() }()
	}

	service, closer, err := newClient(cloudclient.Options{
		APIKey:     apiKey,
		APIVersion: cloudNamespaceAPIVersion,
	})
	if err != nil {
		return fmt.Errorf("create Cloud Operations client: %w", err)
	}
	defer func() { _ = closer.Close() }()

	if args[0] == "create" {
		return createCloudNamespace(ctx, service, resourceName, acceptedClientCA, output, sleepWithContext)
	}
	return deleteCloudNamespace(ctx, service, resourceName, sleepWithContext)
}

func createCloudNamespace(
	ctx context.Context,
	service cloudNamespaceService,
	name string,
	acceptedClientCA []byte,
	output io.Writer,
	sleep cloudOperationSleeper,
) error {
	response, err := service.CreateNamespace(ctx, &cloudservice.CreateNamespaceRequest{
		Spec: &cloudnamespace.NamespaceSpec{
			Name:          name,
			RetentionDays: 1,
			MtlsAuth: &cloudnamespace.MtlsAuthSpec{
				AcceptedClientCa: acceptedClientCA,
				Enabled:          true,
			},
			Replicas: []*cloudnamespace.ReplicaSpec{{Region: cloudNamespaceRegion}},
		},
	})
	if err != nil {
		return fmt.Errorf("create Cloud namespace: %w", err)
	}
	if response.GetNamespace() == "" {
		return fmt.Errorf("create Cloud namespace returned an empty namespace")
	}
	// Record the namespace as soon as Cloud accepts it so CI cleanup can run even
	// if the asynchronous provisioning operation subsequently fails.
	if _, err := fmt.Fprintf(output, "namespace=%s\n", response.GetNamespace()); err != nil {
		return fmt.Errorf("record Cloud namespace %q for cleanup: %w", response.GetNamespace(), err)
	}
	return waitForCloudOperation(ctx, service, response.GetAsyncOperation(), sleep)
}

func deleteCloudNamespace(
	ctx context.Context,
	service cloudNamespaceService,
	namespace string,
	sleep cloudOperationSleeper,
) error {
	existing, err := service.GetNamespace(ctx, &cloudservice.GetNamespaceRequest{Namespace: namespace})
	if err != nil {
		return fmt.Errorf("get Cloud namespace %q: %w", namespace, err)
	}
	if existing.GetNamespace() == nil || existing.GetNamespace().GetResourceVersion() == "" {
		return fmt.Errorf("get Cloud namespace %q returned an empty resource version", namespace)
	}
	response, err := service.DeleteNamespace(ctx, &cloudservice.DeleteNamespaceRequest{
		Namespace:       namespace,
		ResourceVersion: existing.GetNamespace().GetResourceVersion(),
	})
	if err != nil {
		return fmt.Errorf("delete Cloud namespace %q: %w", namespace, err)
	}
	return waitForCloudOperation(ctx, service, response.GetAsyncOperation(), sleep)
}

func waitForCloudOperation(
	ctx context.Context,
	service cloudNamespaceService,
	operation *cloudoperation.AsyncOperation,
	sleep cloudOperationSleeper,
) error {
	if operation == nil || operation.GetId() == "" {
		return fmt.Errorf("Cloud operation response is missing an operation ID")
	}
	ctx, cancel := context.WithTimeout(ctx, cloudOperationTimeout)
	defer cancel()

	for {
		switch operation.GetState() {
		case cloudoperation.AsyncOperation_STATE_FULFILLED:
			return nil
		case cloudoperation.AsyncOperation_STATE_FAILED,
			cloudoperation.AsyncOperation_STATE_CANCELLED,
			cloudoperation.AsyncOperation_STATE_REJECTED:
			return fmt.Errorf(
				"Cloud operation %s %s: %s",
				operation.GetId(),
				strings.ToLower(strings.TrimPrefix(operation.GetState().String(), "STATE_")),
				operation.GetFailureReason(),
			)
		case cloudoperation.AsyncOperation_STATE_UNSPECIFIED,
			cloudoperation.AsyncOperation_STATE_PENDING,
			cloudoperation.AsyncOperation_STATE_IN_PROGRESS:
		default:
			return fmt.Errorf("Cloud operation %s returned unknown state %d", operation.GetId(), operation.GetState())
		}

		delay := operation.GetCheckDuration().AsDuration()
		if delay < minimumCloudOperationPollDelay {
			delay = minimumCloudOperationPollDelay
		}
		if err := sleep(ctx, delay); err != nil {
			return cloudOperationWaitError(operation.GetId(), err)
		}
		response, err := service.GetAsyncOperation(ctx, &cloudservice.GetAsyncOperationRequest{
			AsyncOperationId: operation.GetId(),
		})
		if err != nil {
			if ctx.Err() != nil {
				return cloudOperationWaitError(operation.GetId(), ctx.Err())
			}
			return fmt.Errorf("get Cloud operation %s: %w", operation.GetId(), err)
		}
		operation = response.GetAsyncOperation()
		if operation == nil || operation.GetId() == "" {
			return fmt.Errorf("get Cloud operation returned an empty operation")
		}
	}
}

func cloudOperationWaitError(operationID string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timed out waiting for Cloud operation %s", operationID)
	}
	return fmt.Errorf("wait for Cloud operation %s: %w", operationID, err)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
