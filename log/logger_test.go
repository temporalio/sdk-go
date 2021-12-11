package log

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

type (
	ctxKVPair string
	logger    struct {
		ctx context.Context
		t   *testing.T
	}
)

var (
	ctxkvp ctxKVPair
)

func (l *logger) Debug(msg string, keyvals ...interface{}) {
	panic("implement me")
}

func (l *logger) Info(msg string, keyvals ...interface{}) {
	val := l.ctx.Value(ctxkvp).(string)
	l.t.Log(msg, val)
	assert.Equal(l.t, "value-added-from-context", val)
	assert.Equal(l.t, "logging information...", msg)
}

func (l *logger) Warn(msg string, keyvals ...interface{}) {
	panic("implement me")
}

func (l *logger) Error(msg string, keyvals ...interface{}) {
	panic("implement me")
}

func (l *logger) Context(ctx context.Context) Logger {
	l.ctx = ctx
	return l
}

func TestLogger(t *testing.T) {
	ctx := context.Background()
	l := &logger{ctx: ctx, t: t}
	l.Context(context.WithValue(ctx, ctxkvp, "value-added-from-context")).Info("logging information...")
}
