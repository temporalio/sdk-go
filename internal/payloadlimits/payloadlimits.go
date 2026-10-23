// Package payloadlimits validates outbound request payload and memo sizes against the limits the
// Temporal server enforces.
//
// The per-field policy is generated from the API proto descriptors against the hand-authored
// decision table in internal/cmd/payload-limit-gen/table.go. Adding or removing a payload-bearing
// field in go.temporal.io/api fails that command's test until the table is updated and the
// validator regenerated.
package payloadlimits

//go:generate go run ../cmd/payload-limit-gen -out validator_gen.go

import (
	"strconv"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/log"
	"google.golang.org/protobuf/proto"
)

// LimitClass is the server-enforced size limit a payload field is subject to.
type LimitClass int

const (
	LimitClassBlob LimitClass = iota
	LimitClassMemo
)

// Severity is the threshold a violation exceeded.
type Severity int

const (
	SeverityWarning Severity = iota
	SeverityError
)

// Sink receives one Check per validated payload field, with the field's size as the server
// measures it. The generated traversal brackets each nested message with Enter*/Exit so a sink can
// track where a field is.
type Sink interface {
	// Check is called for each validated field. When enforceError is false the field may warn but
	// must never produce an error-level violation.
	Check(fieldName string, class LimitClass, size int64, enforceError bool)
	// Enter enters a singular nested-message field.
	Enter(name string)
	// EnterIndex enters element index of a repeated nested-message field.
	EnterIndex(name string, index int)
	// EnterKey enters the entry under key of a map-valued nested-message field.
	EnterKey(name string, key string)
	// Exit leaves the most recently entered field.
	Exit()
}

type pathSegment struct {
	name  string
	key   string
	index int
	kind  uint8
}

const (
	segmentField uint8 = iota
	segmentIndex
	segmentKey
)

// Path tracks the proto field names leading to the field being validated. Proto names keep paths
// identical across SDKs. Segments stay unrendered so the traversal, which runs on every request,
// does not allocate strings; Leaf renders only when there is a violation to report.
type Path struct {
	segments []pathSegment
}

func (p *Path) Push(name string) {
	p.segments = append(p.segments, pathSegment{name: name, kind: segmentField})
}

func (p *Path) PushIndex(name string, index int) {
	p.segments = append(p.segments, pathSegment{name: name, index: index, kind: segmentIndex})
}

func (p *Path) PushKey(name string, key string) {
	p.segments = append(p.segments, pathSegment{name: name, key: key, kind: segmentKey})
}

func (p *Path) Pop() {
	p.segments = p.segments[:len(p.segments)-1]
}

// Leaf renders the dotted path to fieldName, e.g.
// "commands[2].schedule_activity_task_command_attributes.input".
func (p *Path) Leaf(fieldName string) string {
	if len(p.segments) == 0 {
		return fieldName
	}
	var sb strings.Builder
	for _, s := range p.segments {
		sb.WriteString(s.name)
		switch s.kind {
		case segmentIndex:
			sb.WriteByte('[')
			sb.WriteString(strconv.Itoa(s.index))
			sb.WriteByte(']')
		case segmentKey:
			sb.WriteByte('[')
			sb.WriteString(s.key)
			sb.WriteByte(']')
		}
		sb.WriteByte('.')
	}
	sb.WriteString(fieldName)
	return sb.String()
}

// Limits are the warn and error thresholds in bytes for both limit classes. A zero threshold
// disables that check: zero warn means no warnings, zero error means warnings only.
type Limits struct {
	BlobWarn  int64
	BlobError int64
	MemoWarn  int64
	MemoError int64
}

func (l Limits) warn(class LimitClass) int64 {
	if class == LimitClassMemo {
		return l.MemoWarn
	}
	return l.BlobWarn
}

func (l Limits) error(class LimitClass) int64 {
	if class == LimitClassMemo {
		return l.MemoError
	}
	return l.BlobError
}

// Violation is a payload field whose size exceeded its warning or error threshold.
type Violation struct {
	// Path is the proto field path from the request root.
	Path     string
	Class    LimitClass
	Severity Severity
	Size     int64
	// Limit is the threshold that was exceeded.
	Limit int64
}

// Error returns the user-facing [TMPRL1103] message.
func (v *Violation) Error() string {
	noun := "payloads"
	if v.Class == LimitClassMemo {
		noun = "memo"
	}
	kind := "warning"
	if v.Severity == SeverityError {
		kind = "error"
	}
	return "[TMPRL1103] Attempted to upload " + noun + " with size that exceeded the " + kind + " limit."
}

// CollectingSink sorts each checked field into Warnings or Errors, without logging or deciding
// what to do about them. A field over its error threshold is an error when enforceError is set and
// an error threshold is configured; otherwise a field over its warning threshold is a warning.
type CollectingSink struct {
	Limits   Limits
	Warnings []*Violation
	Errors   []*Violation
	path     Path
}

func (s *CollectingSink) Check(fieldName string, class LimitClass, size int64, enforceError bool) {
	if errLimit := s.Limits.error(class); enforceError && errLimit > 0 && size > errLimit {
		s.Errors = append(s.Errors, &Violation{
			Path: s.path.Leaf(fieldName), Class: class, Severity: SeverityError, Size: size, Limit: errLimit,
		})
	} else if warnLimit := s.Limits.warn(class); warnLimit > 0 && size > warnLimit {
		s.Warnings = append(s.Warnings, &Violation{
			Path: s.path.Leaf(fieldName), Class: class, Severity: SeverityWarning, Size: size, Limit: warnLimit,
		})
	}
}

func (s *CollectingSink) Enter(name string)                 { s.path.Push(name) }
func (s *CollectingSink) EnterIndex(name string, index int) { s.path.PushIndex(name, index) }
func (s *CollectingSink) EnterKey(name string, key string)  { s.path.PushKey(name, key) }
func (s *CollectingSink) Exit()                             { s.path.Pop() }

// Validate checks req against limits. If any field exceeded its error threshold, it logs the
// errors and returns the first as a *Violation without logging warnings; otherwise it logs each
// warning and returns nil. A nil logger disables logging.
func Validate(req proto.Message, limits Limits, logger log.Logger) error {
	sink := &CollectingSink{Limits: limits}
	dispatch(sink, req)
	if len(sink.Errors) > 0 {
		if logger != nil {
			for _, v := range sink.Errors {
				logger.Error(v.Error(), logTags(v)...)
			}
		}
		return sink.Errors[0]
	}
	if logger != nil {
		for _, v := range sink.Warnings {
			logger.Warn(v.Error(), logTags(v)...)
		}
	}
	return nil
}

func logTags(v *Violation) []any {
	if v.Class == LimitClassMemo {
		return []any{"MemoSize", v.Size, "MemoSizeLimit", v.Limit, "PayloadPath", v.Path}
	}
	return []any{"PayloadSize", v.Size, "PayloadSizeLimit", v.Limit, "PayloadPath", v.Path}
}

func messageSize(m proto.Message) int64 {
	return int64(proto.Size(m))
}

func messageSizeSum[M proto.Message](ms []M) int64 {
	var total int64
	for _, m := range ms {
		total += int64(proto.Size(m))
	}
	return total
}

// mapPayloadsSum mirrors the server's sum(len(key) + payloads.Size()) for map<string, Payloads>
// fields such as RecordMarkerCommandAttributes.details.
func mapPayloadsSum(m map[string]*commonpb.Payloads) int64 {
	var total int64
	for k, v := range m {
		total += int64(len(k)) + int64(proto.Size(v))
	}
	return total
}

// mapPayloadDataSum mirrors the server's sum(len(key) + len(payload.data)) for map<string, Payload>
// fields such as search attributes. The server counts raw data here, not serialized payload size.
func mapPayloadDataSum(m map[string]*commonpb.Payload) int64 {
	var total int64
	for k, v := range m {
		total += int64(len(k)) + int64(len(v.GetData()))
	}
	return total
}
