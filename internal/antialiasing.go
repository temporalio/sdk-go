package internal

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

// antialiasingKind is an enum. It's either an activity or a workflow.
type antialiasingKind int

const (
	activityAntialiasing antialiasingKind = iota
	workflowAntialiasing
)

// antialiasingRegistration holds information about a workflow or activity
// registered by the user.
type antialiasingRegistration struct {
	functionName   string
	registeredName string
	// registeredAt is the place where this workflow or activity was registered.
	// The format of this string is "file:line" or "unknown location".
	registeredAt string
}

func (a antialiasingRegistration) String() string {
	return fmt.Sprintf("%s, registered as %q at %s", a.functionName, a.registeredName, a.registeredAt)
}

// antialiasingRecordRegistration checks if the registered workflow or activity
// has a name collision with another workflow or activity registered earlier.
func (r *registry) antialiasingRecordRegistration(kind antialiasingKind, function any, registeredName string) {
	var registrations map[string]antialiasingRegistration
	var kindName string
	switch kind {
	case activityAntialiasing:
		registrations, kindName = r.antialiasingActivityMap, "activities"
	default:
		registrations, kindName = r.antialiasingWorkflowMap, "workflows"
	}
	if registrations == nil || mightBeFunctionLiteral(function) {
		return
	}

	fnName, _ := getFunctionName(function)
	registration := antialiasingRegistration{
		registeredName: registeredName,
		functionName:   strings.TrimSuffix(runtime.FuncForPC(reflect.ValueOf(function).Pointer()).Name(), "-fm"),
		registeredAt:   registrationLocationFromCallStack(),
	}

	for _, name := range []string{fnName, registeredName} {
		if previous, ok := registrations[name]; ok &&
			previous.registeredName != registeredName && previous.functionName != registration.functionName {
			r.logger.Warn(
				fmt.Sprintf("Found two %s called %q:\n  %s\n  %s\n"+
					"Because of registration aliasing, calling one function might trigger the other!\n"+
					"Use worker.Options.DisableRegistrationAliasing to avoid ambiguity.",
					strings.ToLower(kindName), name, previous, registration),
			)
		}
		registrations[name] = registration
	}
}

// registrationLocationFromCallStack returns the file and line number of the
// first function in the call stack that is not a wrapper for workflow/activity
// registration. If no such function is found, it returns "unknown location".
func registrationLocationFromCallStack() string {
	var callers [16]uintptr
	count := runtime.Callers(2, callers[:])
	frames := runtime.CallersFrames(callers[:count])
	for {
		frame, more := frames.Next()
		isRegistrationWrapper := strings.HasPrefix(frame.Function, "go.temporal.io/sdk/internal.") &&
			(strings.HasSuffix(frame.Function, ".RegisterActivity") ||
				strings.HasSuffix(frame.Function, ".RegisterActivityWithOptions") ||
				strings.HasSuffix(frame.Function, ".RegisterWorkflow") ||
				strings.HasSuffix(frame.Function, ".RegisterWorkflowWithOptions") ||
				strings.HasSuffix(frame.Function, ".registerActivityStructWithOptions") ||
				strings.HasSuffix(frame.Function, ".antialiasingRecordRegistration"))
		if !isRegistrationWrapper && frame.File != "" && frame.Line > 0 {
			return fmt.Sprintf("%s:%d", frame.File, frame.Line)
		}
		if !more {
			return "unknown location"
		}
	}
}
