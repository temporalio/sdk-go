package util

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_byteSliceToString(t *testing.T) {
	data := []byte("blob-data")
	v := reflect.ValueOf(data)
	strVal := valueToString(v)

	require.Equal(t, "[blob-data]", strVal)

	intBlob := []int32{1, 2, 3}
	v2 := reflect.ValueOf(intBlob)
	strVal2 := valueToString(v2)

	require.Equal(t, "[len=3]", strVal2)
}

func TestValueToString(t *testing.T) {

	testValue := "test"
	testSlice := []string{"a", "b", "c"}
	var emptyInter interface{}
	var testFloat64 float64 = 5.55

	tempStruct := struct {
		Test string
	}{
		"test",
	}

	tempUnexportedStruct := struct {
		test string
	}{
		"test",
	}

	tests := []struct {
		name     string
		value    reflect.Value
		expected string
	}{
		{
			name:     "pointer",
			value:    reflect.ValueOf(&testValue),
			expected: testValue,
		},
		{
			name:     "invalid",
			value:    reflect.ValueOf(emptyInter),
			expected: "",
		},
		{
			name:     "struct",
			value:    reflect.ValueOf(tempStruct),
			expected: "(Test:test)",
		},
		{
			name:     "unexported struct",
			value:    reflect.ValueOf(tempUnexportedStruct),
			expected: "()",
		},
		{
			name:     "slice",
			value:    reflect.ValueOf(testSlice),
			expected: "[len=3]",
		},
		{
			name:     "default",
			value:    reflect.ValueOf(testFloat64),
			expected: "5.55",
		},
	}

	for _, d := range tests {
		t.Logf("testing test name %s", d.name)
		str := valueToString(d.value)

		if str != d.expected {
			t.Fatalf("%s: expected %s, received %s", d.name, d.expected, str)
		}

	}

}
