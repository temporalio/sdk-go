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
	"bytes"
	"context"
	"fmt"
	"testing"

	"go.temporal.io/sdk/converter"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

type (
	ContextAwareDataConverter struct {
		dataConverter converter.DataConverter
		mask          string
	}

	contextKeyType struct{}
)

var (
	ContextAwareDataConverterContextKey = contextKeyType{}
)

func (dc *ContextAwareDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	payload, err := dc.dataConverter.ToPayload(value)
	if err != nil {
		return payload, err
	}
	if dc.mask != "" {
		payload.Data = bytes.ReplaceAll(payload.Data, []byte(dc.mask), []byte("?"))
	}

	return payload, nil
}

func (dc *ContextAwareDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	result := &commonpb.Payloads{}

	for i, value := range values {
		payload, err := dc.ToPayload(value)
		if err != nil {
			return nil, fmt.Errorf("values[%d]: %w", i, err)
		}

		result.Payloads = append(result.Payloads, payload)
	}

	return result, nil
}

func (dc *ContextAwareDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	return dc.dataConverter.FromPayload(payload, valuePtr)
}

func (dc *ContextAwareDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	return dc.dataConverter.FromPayloads(payloads, valuePtrs...)
}

func (dc *ContextAwareDataConverter) ToString(payload *commonpb.Payload) string {
	return dc.dataConverter.ToString(payload)
}

func (dc *ContextAwareDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	return dc.dataConverter.ToStrings(payloads)
}

func (dc *ContextAwareDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	v := ctx.Value(ContextAwareDataConverterContextKey)
	mask, ok := v.(string)
	if !ok {
		return dc
	}

	return &ContextAwareDataConverter{
		dataConverter: dc.dataConverter,
		mask:          mask,
	}
}

func (dc *ContextAwareDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	v := ctx.Value(ContextAwareDataConverterContextKey)
	mask, ok := v.(string)
	if !ok {
		return dc
	}

	return &ContextAwareDataConverter{
		dataConverter: dc.dataConverter,
		mask:          mask,
	}
}

func NewContextAwareDataConverter(dataConverter converter.DataConverter) converter.DataConverter {
	return &ContextAwareDataConverter{
		dataConverter: dataConverter,
	}
}

func TestContextAwareDataConverter(t *testing.T) {
	var contextAwareDataConverter = NewContextAwareDataConverter(converter.GetDefaultDataConverter())

	t.Parallel()
	t.Run("default", func(t *testing.T) {
		t.Parallel()
		payload, _ := contextAwareDataConverter.ToPayload("test")
		result := contextAwareDataConverter.ToString(payload)

		require.Equal(t, `"test"`, result)
	})
	t.Run("implements ContextAware", func(t *testing.T) {
		t.Parallel()
		_, ok := contextAwareDataConverter.(ContextAware)
		require.True(t, ok)
	})
	t.Run("with activity context", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "e")

		dc := WithContext(ctx, contextAwareDataConverter)

		payload, _ := dc.ToPayload("test")
		result := dc.ToString(payload)

		require.Equal(t, `"t?st"`, result)
	})
	t.Run("with workflow context", func(t *testing.T) {
		t.Parallel()
		ctx := Background()
		ctx = WithValue(ctx, ContextAwareDataConverterContextKey, "e")

		dc := WithWorkflowContext(ctx, contextAwareDataConverter)

		payload, _ := dc.ToPayload("test")
		result := dc.ToString(payload)

		require.Equal(t, `"t?st"`, result)
	})
}
