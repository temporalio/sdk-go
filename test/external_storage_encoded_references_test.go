package test_test

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/extstore"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const referenceRecordingEncoding = "binary/reference-recording"

// referenceRecordingCodec wraps every payload, and records the serialization
// context it was given whenever it decodes a storage reference.
type referenceRecordingCodec struct {
	sc       converter.SerializationContext
	recorder *referenceContextRecorder
}

type referenceContextRecorder struct {
	mu       sync.Mutex
	contexts []converter.SerializationContext
}

func (r *referenceContextRecorder) record(sc converter.SerializationContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contexts = append(r.contexts, sc)
}

func (r *referenceContextRecorder) recorded() []converter.SerializationContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]converter.SerializationContext(nil), r.contexts...)
}

func (c referenceRecordingCodec) WithSerializationContext(sc converter.SerializationContext) converter.PayloadCodec {
	return referenceRecordingCodec{sc: sc, recorder: c.recorder}
}

func (c referenceRecordingCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		data, err := proto.Marshal(p)
		if err != nil {
			return nil, err
		}
		result[i] = &commonpb.Payload{
			Metadata: map[string][]byte{converter.MetadataEncoding: []byte(referenceRecordingEncoding)},
			Data:     data,
		}
	}
	return result, nil
}

func (c referenceRecordingCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()[converter.MetadataEncoding]) != referenceRecordingEncoding {
			result[i] = p
			continue
		}
		result[i] = &commonpb.Payload{}
		if err := proto.Unmarshal(p.GetData(), result[i]); err != nil {
			return nil, err
		}
		if extstore.IsStorageReference(result[i]) {
			c.recorder.record(c.sc)
		}
	}
	return result, nil
}

// referenceEncodingInterceptor encodes and marks the storage references in every
// outgoing request, as an SDK that writes encoded references does. This SDK only
// reads them for now.
func referenceEncodingInterceptor(codec converter.PayloadCodec) grpc.UnaryClientInterceptor {
	visitor := extstore.NewReferenceEncodingVisitor(func(context.Context) extstore.ReferenceCodec { return codec })
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if msg, ok := req.(proto.Message); ok {
			if err := proxy.VisitPayloads(ctx, msg, proxy.VisitPayloadsOptions{Visitor: visitor.Visit, SkipSearchAttributes: true}); err != nil {
				return err
			}
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func extStoreEncodedReferencesWorkflow(ctx workflow.Context, input string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var activityResult string
	if err := workflow.ExecuteActivity(ctx, extStoreLargeResultActivity).Get(ctx, &activityResult); err != nil {
		return "", err
	}
	ctx = workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	var localActivityResult string
	if err := workflow.ExecuteLocalActivity(ctx, extStoreLargeResultActivity).Get(ctx, &localActivityResult); err != nil {
		return "", err
	}
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:         workflow.GetInfo(ctx).WorkflowExecution.ID + "-child",
		WorkflowRunTimeout: time.Minute,
	})
	var childResult string
	if err := workflow.ExecuteChildWorkflow(ctx, intTestChildWorkflow, input).Get(ctx, &childResult); err != nil {
		return "", err
	}
	return input + activityResult + localActivityResult + childResult, nil
}

// TestEncodedReferencesDecodedWithPayloadContext verifies that every encoded
// storage reference is decoded with the serialization context of the payload it
// replaces: on the worker, in the client, and on replay.
func (s *ExternalStorageTestSuite) TestEncodedReferencesDecodedWithPayloadContext() {
	recorder := &referenceContextRecorder{}
	codec := referenceRecordingCodec{recorder: recorder}
	externalStorage := converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{s.driver},
		PayloadSizeThreshold: extStoreThreshold,
	}
	dataConverter := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)

	c, err := s.newDefaultClient(func(options *client.Options) {
		options.WorkerHeartbeatInterval = -1
		options.DataConverter = dataConverter
		options.ExternalStorage = externalStorage
		options.ConnectionOptions.DialOptions = append(options.ConnectionOptions.DialOptions,
			grpc.WithChainUnaryInterceptor(referenceEncodingInterceptor(codec)))
	})
	s.NoError(err)
	defer c.Close()
	w := worker.New(c, s.taskQueueName, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterWorkflow(extStoreEncodedReferencesWorkflow)
	w.RegisterWorkflow(intTestChildWorkflow)
	w.RegisterActivity(extStoreLargeResultActivity)
	s.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	wfID := "ext-encoded-refs-" + uuid.NewString()
	input := oversized(10)
	run, err := c.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreEncodedReferencesWorkflow, input)
	s.NoError(err)
	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(input+oversized(72)+oversized(72)+"child:"+input, result)

	workflowContext := converter.WorkflowSerializationContext{Namespace: s.config.Namespace, WorkflowID: wfID}
	activityContext := converter.ActivitySerializationContext{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreEncodedReferencesWorkflow",
		ActivityType: "extStoreLargeResultActivity",
		TaskQueue:    s.taskQueueName,
	}
	childContext := converter.WorkflowSerializationContext{Namespace: s.config.Namespace, WorkflowID: wfID + "-child"}
	// Workflow input on the worker, workflow result in the client, the activity
	// result on the worker, which arrives in a later workflow task, and the child
	// workflow's input on the worker and its result in the parent.
	s.Contains(recorder.recorded(), converter.SerializationContext(workflowContext))
	s.Contains(recorder.recorded(), converter.SerializationContext(activityContext))
	s.Contains(recorder.recorded(), converter.SerializationContext(childContext))

	// Replay decodes the local activity marker, whose result reference uses the
	// local activity's context.
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		DataConverter:   dataConverter,
		ExternalStorage: externalStorage,
	})
	s.NoError(err)
	replayer.RegisterWorkflow(extStoreEncodedReferencesWorkflow)
	replayer.RegisterWorkflow(intTestChildWorkflow)
	s.NoError(replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, s.config.Namespace,
		workflow.Execution{ID: wfID, RunID: run.GetRunID()}))

	localActivityContext := activityContext
	localActivityContext.IsLocal = true
	s.Contains(recorder.recorded(), converter.SerializationContext(localActivityContext))
}
