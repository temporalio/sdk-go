package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
)

type testVisitor struct {
	visitFunc func(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error)
}

func (v *testVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return v.visitFunc(ctx, payloads)
}

func TestCompositePayloadVisitor(t *testing.T) {
	t.Run("chains visitors in order", func(t *testing.T) {
		var order []int
		v1 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			order = append(order, 1)
			return p, nil
		}}
		v2 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			order = append(order, 2)
			return p, nil
		}}

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2}, order)
	})

	t.Run("passes transformed payloads to next visitor", func(t *testing.T) {
		extraPayload := makeTestPayload(5)
		v1 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			return append(p, extraPayload), nil
		}}
		var receivedCount int
		v2 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			receivedCount = len(p)
			return p, nil
		}}

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, 2, receivedCount)
	})

	t.Run("error short-circuits", func(t *testing.T) {
		expectedErr := errors.New("visitor error")
		v1 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			return nil, expectedErr
		}}
		v2Called := false
		v2 := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			v2Called = true
			return p, nil
		}}

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.ErrorIs(t, err, expectedErr)
		require.False(t, v2Called)
	})

	t.Run("single visitor", func(t *testing.T) {
		called := false
		v := &testVisitor{visitFunc: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			called = true
			return p, nil
		}}
		composite := newCompositePayloadVisitor(v)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.True(t, called)
	})

	t.Run("empty visitors", func(t *testing.T) {
		composite := newCompositePayloadVisitor()
		ctx := &proxy.VisitPayloadsContext{}
		result, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Len(t, result, 1)
	})
}
