package metrics

import (
	"time"

	"github.com/uber-go/tally"
	"go.temporal.io/temporal-proto/serviceerror"
)

type (
	operationScope struct {
		scope     tally.Scope
		startTime time.Time
	}
)

func (s *operationScope) handleError(err error) {
	s.scope.Timer(TemporalLatency).Record(time.Since(s.startTime))
	if err != nil {
		switch err.(type) {
		case *serviceerror.NotFound,
			*serviceerror.InvalidArgument,
			*serviceerror.NamespaceAlreadyExists,
			*serviceerror.WorkflowExecutionAlreadyStarted,
			*serviceerror.QueryFailed:
			s.scope.Counter(TemporalInvalidRequest).Inc(1)
		default:
			s.scope.Counter(TemporalError).Inc(1)
		}
	}
}
