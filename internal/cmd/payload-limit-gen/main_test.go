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
	if string(got) != string(want) {
		t.Fatal("internal/payloadlimits/validator_gen.go is out of date; run `go generate ./internal/payloadlimits`")
	}
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

func TestMapOfAnyFieldPanics(t *testing.T) {
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("payloadlimits_gen_test.proto"),
		Package:    proto.String("temporal.test"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/any.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Holder"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("bodies"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".temporal.test.Holder.BodiesEntry"),
			}},
			NestedType: []*descriptorpb.DescriptorProto{{
				Name:    proto.String("BodiesEntry"),
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
						TypeName: proto.String(".google.protobuf.Any"),
					},
				},
			}},
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	field := fd.Messages().ByName("Holder").Fields().ByName("bodies")
	require.PanicsWithValue(t,
		"payload-limits: map field temporal.test.Holder.bodies holds google.protobuf.Any values, which the "+
			"generator cannot measure yet; add a leafKind for it",
		func() { classify(field) })
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
