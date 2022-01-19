package determinism

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
)

// NonDeterminisms is a set of reasons why a function is non-deterministic.
type NonDeterminisms []Reason

// AFact is for implementing golang.org/x/tools/go/analysis.Fact.
func (*NonDeterminisms) AFact() {}

// String returns all reasons as a comma-delimited string.
func (n *NonDeterminisms) String() string {
	if n == nil {
		return "<none>"
	}
	var str string
	for _, reason := range *n {
		if str != "" {
			str += ", "
		}
		str += reason.String()
	}
	return str
}

// AppendChildReasonLines appends to lines the set of reasons in this slice.
// This will include newlines and indention based on depth.
func (n NonDeterminisms) AppendChildReasonLines(
	subject string,
	s []string,
	depth int,
	includePos bool,
) []string {
	for _, reason := range n {
		reasonStr := reason.String()
		if includePos {
			// Relativize path if it at least starts with working dir
			pos := reason.Pos()
			filename := pos.Filename
			if wd, err := os.Getwd(); err == nil && strings.HasPrefix(filename, wd) {
				if relFilename, err := filepath.Rel(wd, filename); err == nil {
					filename = relFilename
				}
			}
			reasonStr += fmt.Sprintf(" at %v:%v:%v", filename, pos.Line, pos.Column)
		}
		s = append(s, fmt.Sprintf("%v is non-deterministic, reason: %v", strings.Repeat("  ", depth)+subject, reasonStr))
		// Recurse if func call
		if funcCall, _ := reason.(*ReasonFuncCall); funcCall != nil {
			s = funcCall.Child.AppendChildReasonLines(funcCall.Func.FullName(), s, depth+1, includePos)
		}
	}
	return s
}

// Reason represents a reason for non-determinism.
type Reason interface {
	Pos() *token.Position
	// String is expected to just include the brief reason, not any child reasons.
	String() string
}

type reasonBase struct {
	pos *token.Position
}

func (r *reasonBase) Pos() *token.Position { return r.pos }

// ReasonDecl represents a function or var that was explicitly marked
// non-deterministic via config.
type ReasonDecl struct {
	reasonBase
}

// String returns the reason.
func (r *ReasonDecl) String() string {
	return "declared non-deterministic"
}

// ReasonFuncCall represents a call to a non-deterministic function.
type ReasonFuncCall struct {
	reasonBase
	Func  *types.Func
	Child NonDeterminisms
}

// String returns the reason.
func (r *ReasonFuncCall) String() string {
	return "calls non-deterministic function " + r.Func.FullName()
}

// ReasonVarAccess represents accessing a non-deterministic global variable.
type ReasonVarAccess struct {
	reasonBase
	Var *types.Var
}

// String returns the reason.
func (r *ReasonVarAccess) String() string {
	return "accesses non-deterministic var " + r.Var.Pkg().Path() + "." + r.Var.Name()
}

// ReasonConcurrency represents a non-deterministic concurrency construct.
type ReasonConcurrency struct {
	reasonBase
	Kind ConcurrencyKind
}

// String returns the reason.
func (r *ReasonConcurrency) String() string {
	switch r.Kind {
	case ConcurrencyKindGo:
		return "starts goroutine"
	case ConcurrencyKindRecv:
		return "receives from channel"
	case ConcurrencyKindSend:
		return "sends to channel"
	case ConcurrencyKindRange:
		return "iterates over channel"
	default:
		return "<unknown-kind>"
	}
}

// ConcurrencyKind is a construct that is non-deterministic for
// ReasonConcurrency.
type ConcurrencyKind int

const (
	ConcurrencyKindGo ConcurrencyKind = iota
	ConcurrencyKindRecv
	ConcurrencyKindSend
	ConcurrencyKindRange
)

// ReasonMapRange represents iterating over a map via range.
type ReasonMapRange struct {
	reasonBase
}

// String returns the reason.
func (r *ReasonMapRange) String() string {
	return "iterates over map"
}
