// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"errors"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	interactionpb "go.temporal.io/api/interaction/v1"

	"go.temporal.io/sdk/converter"
	iconverter "go.temporal.io/sdk/internal/converter"
)

type mockWorkflowDefinition struct {
	WorkflowDefinition
	OnWorkflowTaskStartedFunc func(time.Duration)
}

func (m *mockWorkflowDefinition) OnWorkflowTaskStarted(d time.Duration) {
	m.OnWorkflowTaskStartedFunc(d)
}

func testDecodeValueHelper(t *testing.T, env *workflowEnvironmentImpl) {
	equals := func(a, b interface{}) bool {
		ao := a.(ActivityOptions)
		bo := b.(ActivityOptions)
		return ao.TaskQueue == bo.TaskQueue
	}
	value := ActivityOptions{TaskQueue: "test-taskqueue"}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskQueue = "value-changed"
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func TestDecodedValue(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: converter.GetDefaultDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func TestDecodedValueWithDataConverter(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: iconverter.NewTestDataConverter(),
	}
	testDecodeValueHelper(t, env)
}

func Test_DecodedValuePtr(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: converter.GetDefaultDataConverter(),
	}
	equals := func(a, b interface{}) bool {
		ao := a.(*ActivityOptions)
		bo := b.(*ActivityOptions)
		return ao.TaskQueue == bo.TaskQueue
	}
	value := &ActivityOptions{TaskQueue: "test-taskqueue"}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	value.TaskQueue = "value-changed"
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)
}

func Test_DecodedValueNil(t *testing.T) {
	t.Parallel()
	env := &workflowEnvironmentImpl{
		dataConverter: converter.GetDefaultDataConverter(),
	}
	equals := func(a, b interface{}) bool {
		return a == nil && b == nil
	}
	// newValue is nil, old value is nil
	var value interface{}
	blob := env.encodeValue(value)
	isEqual := env.isEqualValue(value, blob, equals)
	require.True(t, isEqual)

	// newValue is nil, oldValue is not nil
	blob = env.encodeValue("any-non-nil-value")
	isEqual = env.isEqualValue(value, blob, equals)
	require.False(t, isEqual)

	// newValue is not nil, oldValue is nil
	blob = env.encodeValue(nil)
	isEqual = env.isEqualValue("non-nil-value", blob, equals)
	require.False(t, isEqual)
}

func Test_ValidateAndSerializeSearchAttributes(t *testing.T) {
	t.Parallel()
	_, err := validateAndSerializeSearchAttributes(nil)
	require.EqualError(t, err, "search attributes is empty")

	attr := map[string]interface{}{
		"JustKey": make(chan int),
	}
	_, err = validateAndSerializeSearchAttributes(attr)
	require.EqualError(t, err, "encode search attribute [JustKey] error: unable to encode: json: unsupported type: chan int")

	attr = map[string]interface{}{
		"key": 1,
	}
	searchAttr, err := validateAndSerializeSearchAttributes(attr)
	require.NoError(t, err)
	require.Equal(t, 1, len(searchAttr.IndexedFields))
	var resp int
	_ = converter.GetDefaultDataConverter().FromPayload(searchAttr.IndexedFields["key"], &resp)
	require.Equal(t, 1, resp)
}

func Test_UpsertSearchAttributes(t *testing.T) {
	t.Parallel()
	helper := newCommandsHelper()
	_, ctx := createRootTestContext()
	env := &workflowEnvironmentImpl{
		commandsHelper: helper,
		workflowInfo:   GetWorkflowInfo(ctx),
	}
	helper.setCurrentWorkflowTaskStartedEventID(4)
	err := env.UpsertSearchAttributes(nil)
	require.Error(t, err)

	err = env.UpsertSearchAttributes(map[string]interface{}{
		TemporalChangeVersion: []string{"change2-1", "change1-1"}},
	)
	require.NoError(t, err)
	_, ok := env.commandsHelper.commands[makeCommandID(commandTypeUpsertSearchAttributes, "change2-1")]
	require.True(t, ok)
	require.Equal(t, int64(7), env.GenerateSequence())

	err = env.UpsertSearchAttributes(map[string]interface{}{"key": 1})
	require.NoError(t, err)
	require.Equal(t, int64(8), env.GenerateSequence())
}

func Test_MergeSearchAttributes(t *testing.T) {
	t.Parallel()

	encodeString := func(str string) *commonpb.Payload {
		payload, _ := converter.GetDefaultDataConverter().ToPayload(str)
		return payload
	}

	tests := []struct {
		name     string
		current  *commonpb.SearchAttributes
		upsert   *commonpb.SearchAttributes
		expected *commonpb.SearchAttributes
	}{
		{
			name:     "currentIsNil",
			current:  nil,
			upsert:   &commonpb.SearchAttributes{},
			expected: nil,
		},
		{
			name:     "currentIsEmpty",
			current:  &commonpb.SearchAttributes{IndexedFields: make(map[string]*commonpb.Payload)},
			upsert:   &commonpb.SearchAttributes{},
			expected: nil,
		},
		{
			name: "normalMerge",
			current: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`1`),
					"CustomKeywordField": encodeString(`keyword`),
				},
			},
			upsert: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":  encodeString(`2`),
					"CustomBoolField": encodeString(`true`),
				},
			},
			expected: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`2`),
					"CustomKeywordField": encodeString(`keyword`),
					"CustomBoolField":    encodeString(`true`),
				},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := mergeSearchAttributes(test.current, test.upsert)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_ValidateAndSerializeMemo(t *testing.T) {
	t.Parallel()
	_, err := validateAndSerializeMemo(nil, nil)
	require.EqualError(t, err, "memo is empty")

	attr := map[string]interface{}{
		"JustKey": make(chan int),
	}
	_, err = validateAndSerializeMemo(attr, nil)
	require.EqualError(
		t,
		err,
		"encode workflow memo error: unable to encode: json: unsupported type: chan int",
	)

	attr = map[string]interface{}{
		"key": 1,
	}
	memo, err := validateAndSerializeMemo(attr, nil)
	require.NoError(t, err)
	require.Equal(t, 1, len(memo.Fields))
	var resp int
	_ = converter.GetDefaultDataConverter().FromPayload(memo.Fields["key"], &resp)
	require.Equal(t, 1, resp)
}

func Test_UpsertMemo(t *testing.T) {
	t.Parallel()
	helper := newCommandsHelper()
	_, ctx := createRootTestContext()
	env := &workflowEnvironmentImpl{
		commandsHelper: helper,
		workflowInfo:   GetWorkflowInfo(ctx),
	}
	helper.setCurrentWorkflowTaskStartedEventID(4)
	err := env.UpsertMemo(nil)
	require.Error(t, err)

	err = env.UpsertMemo(map[string]interface{}{"key": 1})
	require.NoError(t, err)
	_, ok := env.commandsHelper.commands[makeCommandID(commandTypeModifyProperties, "6")]
	require.True(t, ok)
	require.Equal(t, int64(7), env.GenerateSequence())
}

func Test_MergeMemo(t *testing.T) {
	t.Parallel()

	encodeString := func(str string) *commonpb.Payload {
		payload, _ := converter.GetDefaultDataConverter().ToPayload(str)
		return payload
	}

	tests := []struct {
		name     string
		current  *commonpb.Memo
		upsert   *commonpb.Memo
		expected *commonpb.Memo
	}{
		{
			name:     "currentIsNil",
			current:  nil,
			upsert:   &commonpb.Memo{},
			expected: nil,
		},
		{
			name:     "currentIsEmpty",
			current:  &commonpb.Memo{Fields: make(map[string]*commonpb.Payload)},
			upsert:   &commonpb.Memo{},
			expected: nil,
		},
		{
			name: "normalMerge",
			current: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`1`),
					"CustomKeywordField": encodeString(`keyword`),
				},
			},
			upsert: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"CustomIntField":  encodeString(`2`),
					"CustomBoolField": encodeString(`true`),
				},
			},
			expected: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{
					"CustomIntField":     encodeString(`2`),
					"CustomKeywordField": encodeString(`keyword`),
					"CustomBoolField":    encodeString(`true`),
				},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(
			test.name,
			func(t *testing.T) {
				t.Parallel()
				result := mergeMemo(test.current, test.upsert)
				require.Equal(t, test.expected, result)
			},
		)
	}
}

func Test_GetChangeVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		changeID string
		version  Version
		expected string
	}{
		{
			name:     "default",
			changeID: "cid",
			version:  DefaultVersion,
			expected: "cid--1",
		},
		{
			name:     "normal_case",
			changeID: "cid",
			version:  1,
			expected: "cid-1",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := getChangeVersion(test.changeID, test.version)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_GetChangeVersions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		changeID               string
		version                Version
		existingChangeVersions map[string]Version
		expected               []string
	}{
		{
			name:                   "single_change_id",
			changeID:               "cid",
			version:                1,
			existingChangeVersions: map[string]Version{},
			expected:               []string{"cid-1"},
		},
		{
			name:     "multi_change_ids",
			changeID: "cid2",
			version:  1,
			existingChangeVersions: map[string]Version{
				"cid": 1,
			},
			expected: []string{"cid2-1", "cid-1"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := getChangeVersions(test.changeID, test.version, test.existingChangeVersions)
			require.Equal(t, test.expected, result)
		})
	}
}

func Test_CreateSearchAttributesForChangeVersion(t *testing.T) {
	t.Parallel()
	result := createSearchAttributesForChangeVersion("cid", 1, map[string]Version{})
	val, ok := result["TemporalChangeVersion"]
	require.True(t, ok, "Remember to update related key on server side")
	require.Equal(t, []string{"cid-1"}, val)
}

func TestUpdateEvents(t *testing.T) {
	mustPayload := func(i interface{}) *commonpb.Payload {
		t.Helper()
		p, err := converter.NewJSONPayloadConverter().ToPayload(i)
		if err != nil {
			t.FailNow()
		}
		return p
	}

	var (
		gotName   string
		gotArgs   *commonpb.Payloads
		gotHeader *commonpb.Header
	)

	weh := &workflowExecutionEventHandlerImpl{
		workflowEnvironmentImpl: &workflowEnvironmentImpl{
			updateHandler: func(name string, args *commonpb.Payloads, header *commonpb.Header, cb UpdateCallbacks) {
				gotName = name
				gotArgs = args
				gotHeader = header
			},
		},
		workflowDefinition: &mockWorkflowDefinition{
			OnWorkflowTaskStartedFunc: func(time.Duration) {},
		},
	}

	meta := &interactionpb.Meta{
		Id:              t.Name() + "-id",
		EventId:         1,
		InteractionType: enumspb.INTERACTION_TYPE_WORKFLOW_UPDATE,
		Identity:        t.Name() + "-identity",
		RequestId:       t.Name() + "-request_id",
	}
	input := &interactionpb.Input{
		Header: &commonpb.Header{Fields: map[string]*commonpb.Payload{"a": mustPayload("b")}},
		Name:   t.Name(),
		Args:   &commonpb.Payloads{Payloads: []*commonpb.Payload{mustPayload("arg0")}},
	}

	err := weh.ProcessInteraction(meta, input, false, false)
	require.NoError(t, err)

	require.Equal(t, input.Name, gotName)
	require.True(t, proto.Equal(input.Header, gotHeader))
	require.True(t, proto.Equal(input.Args, gotArgs))

	// UPDATE_ACCEPTED and UPDATE_COMPLETED are noops for the worker
	for _, evtype := range [...]enumspb.EventType{
		enumspb.EVENT_TYPE_WORKFLOW_UPDATE_ACCEPTED,
		enumspb.EVENT_TYPE_WORKFLOW_UPDATE_COMPLETED,
	} {
		require.NoError(t, weh.ProcessEvent(&historypb.HistoryEvent{EventType: evtype}, false, false))
	}
}

func TestUpdateCommandAdapter(t *testing.T) {
	t.Parallel()
	cmdHelper := newCommandsHelper()
	dc := converter.GetDefaultDataConverter()
	updateCallbacks := &updateCommandCallbacks{
		meta:     &interactionpb.Meta{Id: t.Name() + "-id"},
		dc:       dc,
		fc:       defaultFailureConverter,
		commands: cmdHelper,
	}
	t.Run("reject", func(t *testing.T) {
		wantErr := errors.New(t.Name())
		updateCallbacks.Reject(wantErr)
		cmds := cmdHelper.getCommands(true)
		require.Len(t, cmds, 1)
		cmd, attrs := cmds[0], cmds[0].GetRejectWorkflowUpdateCommandAttributes()
		require.Equal(t, enumspb.COMMAND_TYPE_REJECT_WORKFLOW_UPDATE, cmd.GetCommandType())
		require.NotNil(t, attrs.GetFailure())
		require.Equal(t, wantErr.Error(), attrs.GetFailure().GetMessage())
	})
	t.Run("accept", func(t *testing.T) {
		updateCallbacks.Accept()
		cmds := cmdHelper.getCommands(true)
		require.Len(t, cmds, 1)
		require.Equal(t, enumspb.COMMAND_TYPE_ACCEPT_WORKFLOW_UPDATE, cmds[0].GetCommandType())
	})
	t.Run("complete with error", func(t *testing.T) {
		wantErr := errors.New(t.Name())
		updateCallbacks.Complete(nil, wantErr)
		cmds := cmdHelper.getCommands(true)
		require.Len(t, cmds, 1)
		cmd, attrs := cmds[0], cmds[0].GetCompleteWorkflowUpdateCommandAttributes()
		require.Equal(t, enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_UPDATE, cmd.GetCommandType())
		require.NotNil(t, attrs.Output.GetFailure())
		require.Equal(t, wantErr.Error(), attrs.Output.GetFailure().GetMessage())
	})
	t.Run("complete successfully", func(t *testing.T) {
		wantSuccess := "success!"
		updateCallbacks.Complete(wantSuccess, nil)
		cmds := cmdHelper.getCommands(true)
		require.Len(t, cmds, 1)
		cmd, attrs := cmds[0], cmds[0].GetCompleteWorkflowUpdateCommandAttributes()
		require.Equal(t, enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_UPDATE, cmd.GetCommandType())
		require.Nil(t, attrs.Output.GetFailure())
		require.NotNil(t, attrs.Output.GetSuccess())

		deserializedSuccess := ""
		err := dc.FromPayloads(attrs.Output.GetSuccess(), &deserializedSuccess)
		require.NoError(t, err)
		require.Equal(t, wantSuccess, deserializedSuccess)
	})
	t.Run("complete successfully with invalid payload", func(*testing.T) {
		updateCallbacks.Complete(make(chan int), nil) // dataconverter cannot serialize a chan
		cmds := cmdHelper.getCommands(true)
		require.Len(t, cmds, 1)
		cmd, attrs := cmds[0], cmds[0].GetCompleteWorkflowUpdateCommandAttributes()
		require.Equal(t, enumspb.COMMAND_TYPE_COMPLETE_WORKFLOW_UPDATE, cmd.GetCommandType())
		require.NotNil(t, attrs.Output.GetFailure())
	})
}
