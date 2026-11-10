package secretsmanager

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestFailureReason(t *testing.T) {
	apiErr := func(code string) error {
		return fmt.Errorf("operation error: %w", &smithy.GenericAPIError{Code: code, Message: "msg"})
	}
	for name, tc := range map[string]struct {
		err      error
		expected failureReason
	}{
		"not found":         {apiErr("ResourceNotFoundException"), failureResourceNotFound},
		"access denied":     {apiErr("AccessDeniedException"), failureAccessDenied},
		"decryption":        {apiErr("DecryptionFailure"), failureDecryptionFailure},
		"invalid request":   {apiErr("InvalidRequestException"), failureInvalidRequest},
		"invalid parameter": {apiErr("InvalidParameterException"), failureInvalidRequest},
		"throttling":        {apiErr("ThrottlingException"), failureThrottling},
		"internal":          {apiErr("InternalServiceError"), failureInternalServiceError},
		"unknown code":      {apiErr("SomethingElse"), failureOther},
		"extension 4xx":     {&lambdaExtensionStatusError{statusCode: 400, body: "bad request"}, failureClientError},
		"extension 5xx":     {&lambdaExtensionStatusError{statusCode: 500, body: "oops"}, failureServerError},
		"extension other":   {&lambdaExtensionStatusError{statusCode: 302, body: "redirect"}, failureOther},
		"deadline":          {fmt.Errorf("wrapped: %w", context.DeadlineExceeded), failureTimeout},
		"canceled":          {&smithy.CanceledError{Err: context.Canceled}, failureTimeout},
		"net timeout":       {&url.Error{Op: "Get", URL: "u", Err: timeoutError{}}, failureTimeout},
		"invalid response":  {&invalidResponseError{errors.New("bad")}, failureInvalidResponse},
		"other":             {errors.New("connection refused"), failureOther},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.expected, classifyFailure(tc.err))
		})
	}
}

// Mock telemetry utilities for other tests //

type recordingLogger struct {
	mu   sync.Mutex
	logs []string
}

func (*recordingLogger) Debug(string, ...any)               {}
func (l *recordingLogger) Info(msg string, keyvals ...any)  { l.log("INFO", msg, keyvals) }
func (l *recordingLogger) Warn(msg string, keyvals ...any)  { l.log("WARN", msg, keyvals) }
func (l *recordingLogger) Error(msg string, keyvals ...any) { l.log("ERROR", msg, keyvals) }

func (l *recordingLogger) log(level, msg string, keyvals []any) {
	msg = level + " " + msg
	for i := 0; i+1 < len(keyvals); i += 2 {
		msg += fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1])
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, msg)
}

// take returns and clears the recorded logs
func (l *recordingLogger) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	ret := l.logs
	l.logs = nil
	return ret
}

// recordingMetrics is a metrics handler that records values by name and tags.
type recordingMetrics struct {
	tags map[string]string
	data *recordedMetrics
}

type recordedMetrics struct {
	mu       sync.Mutex
	counters map[string]int64           // keyed by metricKey
	timers   map[string][]time.Duration // keyed by metricKey
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{data: &recordedMetrics{
		counters: map[string]int64{},
		timers:   map[string][]time.Duration{},
	}}
}

func metricKey(name string, tags map[string]string) string {
	key := name
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		key += " " + k + "=" + tags[k]
	}
	return key
}

func tagsFromPairs(kv []any) map[string]string {
	tags := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		tags[fmt.Sprint(kv[i])] = fmt.Sprint(kv[i+1])
	}
	return tags
}

func (m *recordingMetrics) WithTags(tags map[string]string) client.MetricsHandler {
	newTags := maps.Clone(m.tags)
	if newTags == nil {
		newTags = map[string]string{}
	}
	maps.Copy(newTags, tags)
	return &recordingMetrics{tags: newTags, data: m.data}
}

func (m *recordingMetrics) Counter(name string) client.MetricsCounter {
	key := metricKey(name, m.tags)
	return counterFunc(func(d int64) {
		m.data.mu.Lock()
		defer m.data.mu.Unlock()
		m.data.counters[key] += d
	})
}

func (m *recordingMetrics) Gauge(string) client.MetricsGauge {
	return client.MetricsNopHandler.Gauge("")
}

func (m *recordingMetrics) Timer(name string) client.MetricsTimer {
	key := metricKey(name, m.tags)
	return timerFunc(func(d time.Duration) {
		m.data.mu.Lock()
		defer m.data.mu.Unlock()
		m.data.timers[key] = append(m.data.timers[key], d)
	})
}

type counterFunc func(int64)

func (f counterFunc) Inc(d int64) { f(d) }

type timerFunc func(time.Duration)

func (f timerFunc) Record(d time.Duration) { f(d) }

func (m *recordingMetrics) counter(name string, tagPairs ...any) int64 {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	return m.data.counters[metricKey(name, tagsFromPairs(tagPairs))]
}

// counters returns all recorded counters with the given name, keyed by metricKey
func (m *recordingMetrics) counters(name string) map[string]int64 {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	ret := map[string]int64{}
	for k, v := range m.data.counters {
		if strings.HasPrefix(k, name+" ") {
			ret[k] = v
		}
	}
	return ret
}

func (m *recordingMetrics) timer(name string, tagPairs ...any) []time.Duration {
	m.data.mu.Lock()
	defer m.data.mu.Unlock()
	return m.data.timers[metricKey(name, tagsFromPairs(tagPairs))]
}
