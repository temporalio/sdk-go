package main

import (
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	_ "google.golang.org/protobuf/types/known/anypb"
)

// This is what fails CI when a go.temporal.io/api upgrade adds, removes, or reshapes a
// payload-bearing field: the table must be updated and the validator regenerated.
func TestGeneratedValidatorIsUpToDate(t *testing.T) {
	want, err := generate(defaultTable)
	require.NoError(t, err)
	got, err := os.ReadFile("../../payloadlimits/validator_gen.go")
	require.NoError(t, err)
	if !upToDate(got, want) {
		t.Fatal("internal/payloadlimits/validator_gen.go is out of date; run `go generate ./internal/payloadlimits`")
	}
}

func TestUpToDateIgnoresCRLF(t *testing.T) {
	require.True(t, upToDate([]byte("a\r\nb\r\n"), []byte("a\nb\n")))
	require.False(t, upToDate([]byte("a\r\nc\r\n"), []byte("a\nb\n")))
}

func TestUnclassifiedFieldFailsGeneration(t *testing.T) {
	table := defaultTable
	table.blobFields = slices.DeleteFunc(slices.Clone(table.blobFields), func(s string) bool {
		return s == "temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.input"
	})
	_, err := generate(table)
	require.ErrorContains(t, err, "not classified")
	require.ErrorContains(t, err, "temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.input")
}

// An Any field's contents are opaque and may hold payloads, so an unlisted one must fail generation
// rather than be skipped.
func TestUnclassifiedAnyFieldFailsGeneration(t *testing.T) {
	table := defaultTable
	table.blobFields = slices.DeleteFunc(slices.Clone(table.blobFields), func(s string) bool {
		return s == "temporal.api.protocol.v1.Message.body"
	})
	_, err := generate(table)
	require.ErrorContains(t, err, "not classified")
	require.ErrorContains(t, err, "temporal.api.protocol.v1.Message.body")
}

// A map of any leaf type other than Payload or Payloads must fail generation: the walk never
// descends into leaf types, so such a field would otherwise be skipped without being classified.
func TestMapOfLeafTypeFieldPanics(t *testing.T) {
	for _, tc := range []struct{ dependency, valueType string }{
		{"google/protobuf/any.proto", anyType},
		{"temporal/api/failure/v1/message.proto", failureType},
		{"temporal/api/common/v1/message.proto", memoType},
		{"temporal/api/common/v1/message.proto", headerType},
		{"temporal/api/common/v1/message.proto", searchAttributesType},
	} {
		t.Run(tc.valueType, func(t *testing.T) {
			fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
				Name:       proto.String("payloadlimits_gen_test.proto"),
				Package:    proto.String("temporal.test"),
				Syntax:     proto.String("proto3"),
				Dependency: []string{tc.dependency},
				MessageType: []*descriptorpb.DescriptorProto{{
					Name: proto.String("Holder"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name:     proto.String("values"),
						Number:   proto.Int32(1),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".temporal.test.Holder.ValuesEntry"),
					}},
					NestedType: []*descriptorpb.DescriptorProto{{
						Name:    proto.String("ValuesEntry"),
						Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
						Field: []*descriptorpb.FieldDescriptorProto{
							{
								Name:   proto.String("key"),
								Number: proto.Int32(1),
								Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
							},
							{
								Name:     proto.String("value"),
								Number:   proto.Int32(2),
								Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
								Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
								TypeName: proto.String("." + tc.valueType),
							},
						},
					}},
				}},
			}, protoregistry.GlobalFiles)
			require.NoError(t, err)
			field := fd.Messages().ByName("Holder").Fields().ByName("values")
			require.PanicsWithValue(t,
				"payload-limits: map field temporal.test.Holder.values holds "+tc.valueType+
					" values, which the generator cannot measure yet; add a leafKind for it",
				func() { classify(field) })
		})
	}
}

func TestStaleEntryFailsGeneration(t *testing.T) {
	table := defaultTable
	table.notValidatedFields = append(slices.Clone(table.notValidatedFields), "temporal.api.common.v1.Nonexistent.field")
	_, err := generate(table)
	require.ErrorContains(t, err, "no longer correspond")
	require.ErrorContains(t, err, "temporal.api.common.v1.Nonexistent.field")
}

func TestDuplicateEntryFailsGeneration(t *testing.T) {
	table := defaultTable
	table.memoFields = append(slices.Clone(table.memoFields), table.blobFields[0])
	_, err := generate(table)
	require.ErrorContains(t, err, "duplicate table entry")
}

func TestGoCamelCase(t *testing.T) {
	require.Equal(t, "ScheduleActivityTaskCommandAttributes", goCamelCase("schedule_activity_task_command_attributes"))
	require.Equal(t, "Input", goCamelCase("input"))
	require.Equal(t, "Field_1", goCamelCase("field_1"))
}
