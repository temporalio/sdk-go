package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cloudservice "go.temporal.io/cloud-sdk/api/cloudservice/v1"
	cloudnamespace "go.temporal.io/cloud-sdk/api/namespace/v1"
	cloudoperation "go.temporal.io/cloud-sdk/api/operation/v1"
	"google.golang.org/grpc"
)

func TestCreateCloudNamespace(t *testing.T) {
	service := &fakeCloudNamespaceService{
		createResponse: &cloudservice.CreateNamespaceResponse{
			Namespace: "sdk-go-ci-1-1-0.account",
			AsyncOperation: &cloudoperation.AsyncOperation{
				Id:            "create",
				State:         cloudoperation.AsyncOperation_STATE_FAILED,
				FailureReason: "provisioning failed",
			},
		},
	}
	var output bytes.Buffer
	err := createCloudNamespace(
		context.Background(),
		service,
		"sdk-go-ci-1-1-0",
		[]byte("test-ca"),
		&output,
		unexpectedSleep(t),
	)
	if err == nil || !strings.Contains(err.Error(), "provisioning failed") {
		t.Fatalf("error = %v, want provisioning failure", err)
	}
	if got, want := output.String(), "namespace=sdk-go-ci-1-1-0.account\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
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
}

func TestWaitForCloudOperation(t *testing.T) {
	t.Run("pending then fulfilled", func(t *testing.T) {
		service := &fakeCloudNamespaceService{getOperationResponses: []*cloudservice.GetAsyncOperationResponse{{
			AsyncOperation: cloudOperation("operation", cloudoperation.AsyncOperation_STATE_FULFILLED),
		}}}
		var delays []time.Duration
		err := waitForCloudOperation(
			context.Background(),
			service,
			cloudOperation("operation", cloudoperation.AsyncOperation_STATE_PENDING),
			func(_ context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(delays) != 1 || delays[0] != minimumCloudOperationPollDelay {
			t.Fatalf("poll delays = %v, want [%v]", delays, minimumCloudOperationPollDelay)
		}
		if len(service.getOperationRequests) != 1 || service.getOperationRequests[0].AsyncOperationId != "operation" {
			t.Fatalf("GetAsyncOperation requests = %v, want operation", service.getOperationRequests)
		}
	})

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

type fakeCloudNamespaceService struct {
	createRequest         *cloudservice.CreateNamespaceRequest
	createResponse        *cloudservice.CreateNamespaceResponse
	getOperationRequests  []*cloudservice.GetAsyncOperationRequest
	getOperationResponses []*cloudservice.GetAsyncOperationResponse
	getNamespaceRequest   *cloudservice.GetNamespaceRequest
	getNamespaceResponse  *cloudservice.GetNamespaceResponse
	deleteRequest         *cloudservice.DeleteNamespaceRequest
	deleteResponse        *cloudservice.DeleteNamespaceResponse
}

func (f *fakeCloudNamespaceService) CreateNamespace(
	_ context.Context,
	request *cloudservice.CreateNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.CreateNamespaceResponse, error) {
	f.createRequest = request
	return f.createResponse, nil
}

func (f *fakeCloudNamespaceService) GetAsyncOperation(
	_ context.Context,
	request *cloudservice.GetAsyncOperationRequest,
	_ ...grpc.CallOption,
) (*cloudservice.GetAsyncOperationResponse, error) {
	f.getOperationRequests = append(f.getOperationRequests, request)
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
	return f.getNamespaceResponse, nil
}

func (f *fakeCloudNamespaceService) DeleteNamespace(
	_ context.Context,
	request *cloudservice.DeleteNamespaceRequest,
	_ ...grpc.CallOption,
) (*cloudservice.DeleteNamespaceResponse, error) {
	f.deleteRequest = request
	return f.deleteResponse, nil
}
