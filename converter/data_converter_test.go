package converter

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func testDataConverterFunction(t *testing.T, dc DataConverter, f interface{}, args ...interface{}) string {
	input, err := dc.ToPayloads(args...)
	require.NoError(t, err, err)

	var result []interface{}
	for _, v := range args {
		arg := reflect.New(reflect.TypeOf(v)).Interface()
		result = append(result, arg)
	}
	err = dc.FromPayloads(input, result...)
	require.NoError(t, err, err)

	var targetArgs []reflect.Value
	for _, arg := range result {
		targetArgs = append(targetArgs, reflect.ValueOf(arg).Elem())
	}
	fnValue := reflect.ValueOf(f)
	retValues := fnValue.Call(targetArgs)
	return retValues[0].Interface().(string)
}

func TestDefaultDataConverter(t *testing.T) {
	t.Parallel()
	t.Run("result", func(t *testing.T) {
		t.Parallel()
		f1 := func(ctx context.Context, r []byte) string {
			return "result"
		}
		r1 := testDataConverterFunction(t, defaultDataConverter, f1, context.Background(), []byte("test"))
		require.Equal(t, r1, "result")
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		// No parameters.
		f2 := func() string {
			return "empty-result"
		}
		r2 := testDataConverterFunction(t, defaultDataConverter, f2)
		require.Equal(t, r2, "empty-result")
	})
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		// Nil parameter.
		f3 := func(r []byte) string {
			return "nil-result"
		}
		r3 := testDataConverterFunction(t, defaultDataConverter, f3, []byte(""))
		require.Equal(t, r3, "nil-result")
	})
}

func testToStringsFunction(
	t *testing.T,
	dc DataConverter,
	args ...interface{},
) []string {
	input, err := dc.ToPayloads(args...)
	require.NoError(t, err, err)

	strings := dc.ToStrings(input)

	return strings
}

func TestToStrings(t *testing.T) {
	t.Parallel()

	testStruct := struct {
		A string
		B int
	}{
		A: "hi",
		B: 3,
	}

	got := testToStringsFunction(t, defaultDataConverter,
		[]byte("test"),
		[]string{"hello", "world"},
		"hello world",
		42,
		testStruct)

	want := []string{
		"dGVzdA",
		`["hello","world"]`,
		`"hello world"`,
		"42",
		`{"A":"hi","B":3}`,
	}

	require.Equal(t, want, got)
}
