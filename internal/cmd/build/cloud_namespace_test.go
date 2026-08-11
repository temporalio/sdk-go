package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	cloudservice "go.temporal.io/cloud-sdk/api/cloudservice/v1"
	cloudnamespace "go.temporal.io/cloud-sdk/api/namespace/v1"
	cloudoperation "go.temporal.io/cloud-sdk/api/operation/v1"
	"go.temporal.io/cloud-sdk/cloudclient"
	"google.golang.org/grpc"
)

func TestCreateCloudNamespace(t *testing.T) {
	service := &fakeCloudNamespaceService{
		createResponse: &cloudservice.CreateNamespaceResponse{
			Namespace:      "sdk-go-ci-1-1-0.account",
			AsyncOperation: cloudOperation("create", cloudoperation.AsyncOperation_STATE_IN_PROGRESS),
		},
		getOperationResponses: []*cloudservice.GetAsyncOperationResponse{{
			AsyncOperation: cloudOperation("create", cloudoperation.AsyncOperation_STATE_FULFILLED),
		}},
	}
	var output bytes.Buffer
	var delays []time.Duration
	err := createCloudNamespace(
		context.Background(),
		service,
		"sdk-go-ci-1-1-0",
		[]byte("test-ca"),
		&output,
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "namespace=sdk-go-ci-1-1-0.account\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if len(delays) != 1 || delays[0] != minimumCloudOperationPollDelay {
		t.Fatalf("poll delays = %v, want [%v]", delays, minimumCloudOperationPollDelay)
	}
	request := service.createRequest
	if request == nil || request.Spec == nil {
		t.Fatal("CreateNamespace request or spec is nil")
	}
	if got, want := request.Spec.Name, "sdk-go-ci-1-1-0"; got != want {
		t.Fatalf("namespace name = %q, want %q", got, want)
	}
	if got, want := request.Spec.RetentionDays, int32(1); got != want {
		t.Fatalf("retention days = %d, want %d", got, want)
	}
	if request.Spec.MtlsAuth == nil || !request.Spec.MtlsAuth.Enabled {
		t.Fatal("mTLS auth is not enabled")
	}
	if got, want := string(request.Spec.MtlsAuth.AcceptedClientCa), "test-ca"; got != want {
		t.Fatalf("accepted client CA = %q, want %q", got, want)
	}
	if len(request.Spec.Replicas) != 1 || request.Spec.Replicas[0].Region != cloudNamespaceRegion {
		t.Fatalf("replicas = %v, want region %q", request.Spec.Replicas, cloudNamespaceRegion)
	}
	if len(service.getOperationRequests) != 1 || service.getOperationRequests[0].AsyncOperationId != "create" {
		t.Fatalf("GetAsyncOperation requests = %v, want operation create", service.getOperationRequests)
	}
}

func TestCreateCloudNamespaceRecordsOutputBeforeWaitFailure(t *testing.T) {
	service := &fakeCloudNamespaceService{createResponse: &cloudservice.CreateNamespaceResponse{
		Namespace: "sdk-go-ci.account",
		AsyncOperation: &cloudoperation.AsyncOperation{
			Id:            "create",
			State:         cloudoperation.AsyncOperation_STATE_FAILED,
			FailureReason: "provisioning failed",
		},
	}}
	var output bytes.Buffer
	err := createCloudNamespace(
		context.Background(), service, "sdk-go-ci", []byte("test-ca"), &output, unexpectedSleep(t),
	)
	if err == nil || !strings.Contains(err.Error(), "provisioning failed") {
		t.Fatalf("error = %v, want provisioning failure", err)
	}
	if got, want := output.String(), "namespace=sdk-go-ci.account\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestWaitForCloudOperationTerminalFailures(t *testing.T) {
	for _, state := range []cloudoperation.AsyncOperation_State{
		cloudoperation.AsyncOperation_STATE_FAILED,
		cloudoperation.AsyncOperation_STATE_CANCELLED,
		cloudoperation.AsyncOperation_STATE_REJECTED,
	} {
		t.Run(state.String(), func(t *testing.T) {
			err := waitForCloudOperation(
				context.Background(),
				&fakeCloudNamespaceService{},
				&cloudoperation.AsyncOperation{Id: "operation", State: state, FailureReason: "reason"},
				unexpectedSleep(t),
			)
			if err == nil || !strings.Contains(err.Error(), strings.ToLower(strings.TrimPrefix(state.String(), "STATE_"))) {
				t.Fatalf("error = %v, want state %s", err, state)
			}
		})
	}
}

func TestDeleteCloudNamespaceUsesResourceVersion(t *testing.T) {
	service := &fakeCloudNamespaceService{
		getNamespaceResponse: &cloudservice.GetNamespaceResponse{
			Namespace: &cloudnamespace.Namespace{ResourceVersion: "resource-version"},
		},
		deleteResponse: &cloudservice.DeleteNamespaceResponse{
			AsyncOperation: cloudOperation("delete", cloudoperation.AsyncOperation_STATE_FULFILLED),
		},
	}
	if err := deleteCloudNamespace(
		context.Background(), service, "sdk-go-ci.account", unexpectedSleep(t),
	); err != nil {
		t.Fatal(err)
	}
	if service.getNamespaceRequest == nil || service.getNamespaceRequest.Namespace != "sdk-go-ci.account" {
		t.Fatalf("GetNamespace request = %v", service.getNamespaceRequest)
	}
	if service.deleteRequest == nil {
		t.Fatal("DeleteNamespace request is nil")
	}
	if got, want := service.deleteRequest.Namespace, "sdk-go-ci.account"; got != want {
		t.Fatalf("delete namespace = %q, want %q", got, want)
	}
	if got, want := service.deleteRequest.ResourceVersion, "resource-version"; got != want {
		t.Fatalf("resource version = %q, want %q", got, want)
	}
}

func TestRunCloudNamespaceCommandValidatesInputsBeforeCreatingClient(t *testing.T) {
	factory := func(cloudclient.Options) (cloudNamespaceService, io.Closer, error) {
		t.Fatal("client factory called")
		return nil, nil, errors.New("unreachable")
	}
	tests := []struct {
		name   string
		args   []string
		env    map[string]string
		needle string
	}{
		{name: "usage", args: []string{"create"}, needle: "usage:"},
		{name: "empty namespace", args: []string{"delete", " "}, needle: "usage:"},
		{name: "api key", args: []string{"delete", "namespace"}, needle: "TEMPORAL_CLIENT_CLOUD_API_KEY"},
		{
			name:   "CA path",
			args:   []string{"create", "namespace"},
			env:    map[string]string{"TEMPORAL_CLIENT_CLOUD_API_KEY": "key"},
			needle: "TEMPORAL_CLOUD_CLIENT_CA_PATH",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runCloudNamespaceCommand(context.Background(), test.args, mapGetenv(test.env), factory)
			if err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("error = %v, want substring %q", err, test.needle)
			}
		})
	}
}

func cloudOperation(id string, state cloudoperation.AsyncOperation_State) *cloudoperation.AsyncOperation {
	return &cloudoperation.AsyncOperation{Id: id, State: state}
}

func unexpectedSleep(t *testing.T) cloudOperationSleeper {
	t.Helper()
	return func(context.Context, time.Duration) error {
		t.Fatal("unexpected sleep")
		return nil
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

type fakeCloudNamespaceService struct {
	createRequest         *cloudservice.CreateNamespaceRequest
	createResponse        *cloudservice.CreateNamespaceResponse
	createErr             error
	getOperationRequests  []*cloudservice.GetAsyncOperationRequest
	getOperationResponses []*cloudservice.GetAsyncOperationResponse
	getOperationErr       error
	getNamespaceRequest   *cloudservice.GetNamespaceRequest
	getNamespaceResponse  *cloudservice.GetNamespaceResponse
	getNamespaceErr       error
	deleteRequest         *cloudservice.DeleteNamespaceRequest
	deleteResponse        *cloudservice.DeleteNamespaceResponse
	deleteErr             error
}

func (f *fakeCloudNamespaceService) CreateNamespace(
	_ context.Context,
	request *cloudservice.CreateNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.CreateNamespaceResponse, error) {
	f.createRequest = request
	return f.createResponse, f.createErr
}

func (f *fakeCloudNamespaceService) GetAsyncOperation(
	_ context.Context,
	request *cloudservice.GetAsyncOperationRequest,
	_ ...grpc.CallOption,
) (*cloudservice.GetAsyncOperationResponse, error) {
	f.getOperationRequests = append(f.getOperationRequests, request)
	if f.getOperationErr != nil {
		return nil, f.getOperationErr
	}
	if len(f.getOperationResponses) == 0 {
		return nil, errors.New("unexpected GetAsyncOperation call")
	}
	response := f.getOperationResponses[0]
	f.getOperationResponses = f.getOperationResponses[1:]
	return response, nil
}

func (f *fakeCloudNamespaceService) GetNamespace(
	_ context.Context,
	request *cloudservice.GetNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.GetNamespaceResponse, error) {
	f.getNamespaceRequest = request
	return f.getNamespaceResponse, f.getNamespaceErr
}

func (f *fakeCloudNamespaceService) DeleteNamespace(
	_ context.Context,
	request *cloudservice.DeleteNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.DeleteNamespaceResponse, error) {
	f.deleteRequest = request
	return f.deleteResponse, f.deleteErr
}
