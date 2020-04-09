package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	commonpb "go.temporal.io/temporal-proto/common"
)

func TestHeaderWriter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		initial  *commonpb.Header
		expected *commonpb.Header
		vals     map[string][]byte
	}{
		{
			"no values",
			&commonpb.Header{
				Fields: map[string][]byte{},
			},
			&commonpb.Header{
				Fields: map[string][]byte{},
			},
			map[string][]byte{},
		},
		{
			"add values",
			&commonpb.Header{
				Fields: map[string][]byte{},
			},
			&commonpb.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string][]byte{
				"key1": []byte("val1"),
				"key2": []byte("val2"),
			},
		},
		{
			"overwrite values",
			&commonpb.Header{
				Fields: map[string][]byte{
					"key1": []byte("unexpected"),
				},
			},
			&commonpb.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string][]byte{
				"key1": []byte("val1"),
				"key2": []byte("val2"),
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := NewHeaderWriter(test.initial)
			for key, val := range test.vals {
				writer.Set(key, val)
			}
			assert.Equal(t, test.expected, test.initial)
		})
	}
}

func TestHeaderReader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		header  *commonpb.Header
		keys    map[string]struct{}
		isError bool
	}{
		{
			"valid values",
			&commonpb.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string]struct{}{"key1": {}, "key2": {}},
			false,
		},
		{
			"invalid values",
			&commonpb.Header{
				Fields: map[string][]byte{
					"key1": []byte("val1"),
					"key2": []byte("val2"),
				},
			},
			map[string]struct{}{"key2": {}},
			true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := NewHeaderReader(test.header)
			err := reader.ForEachKey(func(key string, val []byte) error {
				if _, ok := test.keys[key]; !ok {
					return assert.AnError
				}
				return nil
			})
			if test.isError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
