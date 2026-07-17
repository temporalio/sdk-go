package googleadk

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"runtime"
	"strings"

	"go.temporal.io/sdk/workflow"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

// runnable is the structural subset of an ADK tool the worker-side CallMcpTool
// Activity needs: the ability to execute the tool. The live MCP tools returned
// by mcptoolset.New(...) satisfy it.
type runnable interface {
	Run(ctx agent.Context, args any) (map[string]any, error)
}

// contextSnapshot is the serializable, read-only view of an ADK ToolContext that
// crosses the Activity boundary for MCP tool execution. Live references
// (services, the embedded context, mutable EventActions) are deliberately
// omitted: state mutations made inside an Activity do not propagate back into
// the workflow. Ordinary function tools run in-workflow and therefore see the
// real, mutable context instead of this snapshot.
type contextSnapshot struct {
	FunctionCallID string
	InvocationID   string
	AgentName      string
	UserID         string
	AppName        string
	SessionID      string
	Branch         string
	// State is a read-only copy of the session state visible to the tool.
	State map[string]any
	// Confirmation is the human's decision for this call, carried across the
	// Activity boundary so ADK's standard ctx.ToolConfirmation() check works
	// worker-side. Its Payload round-trips through the data converter, so typed
	// payloads arrive as map[string]any.
	Confirmation *toolconfirmation.ToolConfirmation
}

// snapshotContext copies the serializable fields out of a live ToolContext on
// the workflow side, before dispatch.
func snapshotContext(tctx agent.Context) contextSnapshot {
	st := map[string]any{}
	if rs := tctx.ReadonlyState(); rs != nil {
		for k, v := range rs.All() {
			st[k] = v
		}
	}
	return contextSnapshot{
		FunctionCallID: tctx.FunctionCallID(),
		InvocationID:   tctx.InvocationID(),
		AgentName:      tctx.AgentName(),
		UserID:         tctx.UserID(),
		AppName:        tctx.AppName(),
		SessionID:      tctx.SessionID(),
		Branch:         tctx.Branch(),
		State:          st,
		Confirmation:   tctx.ToolConfirmation(),
	}
}

// classifyToolError maps a tool failure to Temporal's retry contract. Tool
// failures default to retryable; a tool can opt out by returning an error that
// is already a non-retryable ApplicationError, which is passed through.
func classifyToolError(errType, name string, err error) error {
	var appErr interface{ NonRetryable() bool }
	if errors.As(err, &appErr) {
		return err
	}
	return newApplicationError(errType, true, err, "tool %q failed: %v", name, err)
}

// ----------------------------------------------------------------------------
// ActivityAsTool: expose an existing Temporal activity as an ADK tool.
// ----------------------------------------------------------------------------

// ActivityToolOptions configures ActivityAsTool.
type ActivityToolOptions struct {
	// Name is the tool name the model sees. Defaults to the activity's
	// registered name (or its Go function name when unregistered).
	Name string
	// Description is the tool description the model sees.
	Description string
	// ActivityOptions overrides the per-call Activity options for this tool. A
	// zero StartToCloseTimeout falls back to the default one-minute tool timeout.
	ActivityOptions workflow.ActivityOptions
}

// activityTool is an ADK tool backed by an existing Temporal activity. It runs
// in-workflow like any other tool, and its Run dispatches the user's activity
// (by name) to make the call durable. This is the opt-in way to run a tool as a
// Temporal Activity; plain function tools otherwise execute in-workflow.
type activityTool struct {
	activityName    string
	description     string
	decl            *genai.FunctionDeclaration
	activityOptions workflow.ActivityOptions
}

// ActivityAsTool wraps an existing Temporal activity function as an ADK tool, so
// agents can call code the user already runs as durable activities without
// re-declaring it. The activity must be func(context.Context, TArgs) (TResults,
// error) (or just error); the tool's parameter schema is inferred from TArgs.
// Register the same activity on the worker as usual — the tool dispatches it as
// a Temporal Activity when the agent calls it.
func ActivityAsTool(activityFn any, opts ActivityToolOptions) (tool.Tool, error) {
	v := reflect.ValueOf(activityFn)
	if v.Kind() != reflect.Func {
		return nil, fmt.Errorf("googleadk: ActivityAsTool requires a function, got %T", activityFn)
	}
	ft := v.Type()
	// Enforce the full documented signature at construction time: Run dispatches
	// exactly one TArgs value, so a mis-shaped function would otherwise fail
	// silently at dispatch (the data converter zero-fills missing arguments
	// rather than erroring). Outputs mirror Temporal's own activity contract —
	// one or two return values, the last implementing error.
	if ft.NumIn() != 2 {
		return nil, fmt.Errorf("googleadk: ActivityAsTool function must take exactly (context.Context, TArgs), got %d input arguments", ft.NumIn())
	}
	// Implements (not type equality) accepts custom context.Context
	// implementations while still rejecting workflow.Context, whose Done()
	// returns a workflow.Channel.
	if !ft.In(0).Implements(reflect.TypeOf((*context.Context)(nil)).Elem()) {
		return nil, fmt.Errorf("googleadk: ActivityAsTool function must take context.Context as its first argument, got %s", ft.In(0))
	}
	if ft.NumOut() < 1 || ft.NumOut() > 2 {
		return nil, fmt.Errorf("googleadk: ActivityAsTool function must return (TResults, error) or just error, got %d return values", ft.NumOut())
	}
	if !ft.Out(ft.NumOut() - 1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return nil, fmt.Errorf("googleadk: ActivityAsTool function must return error as its last return value, got %s", ft.Out(ft.NumOut()-1))
	}
	name := opts.Name
	if name == "" {
		name = activityFuncName(v)
	}
	argsType := ft.In(1)
	for argsType.Kind() == reflect.Ptr {
		argsType = argsType.Elem()
	}
	if argsType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("googleadk: ActivityAsTool args type must be a struct, got %s", argsType.Kind())
	}
	return &activityTool{
		activityName:    name,
		description:     opts.Description,
		activityOptions: opts.ActivityOptions,
		decl: &genai.FunctionDeclaration{
			Name:        name,
			Description: opts.Description,
			Parameters:  schemaFromStruct(argsType),
		},
	}, nil
}

func (t *activityTool) Name() string                            { return t.activityName }
func (t *activityTool) Description() string                     { return t.description }
func (t *activityTool) IsLongRunning() bool                     { return false }
func (t *activityTool) Declaration() *genai.FunctionDeclaration { return t.decl }

// ProcessRequest packs this tool's declaration into the request so the model
// sees it. It lets an activityTool be placed directly in an agent's Tools list.
func (t *activityTool) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	return packTool(req, t)
}

// Run executes in-workflow (ADK invokes it there via the deterministic task
// runner) and dispatches the underlying Temporal activity by name, so the user's
// code runs worker-side under Temporal's retry/timeout policy. Only the args
// cross the boundary; they are converted to the activity's TArgs by the data
// converter.
func (t *activityTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	wfCtx, ok := workflowContext(ctx)
	if !ok {
		return nil, errMissingContext
	}
	ao := resolveToolActivityOptions(t.activityOptions, "")
	ao.Summary = toolSummary(ctx, t.Name())
	actx := workflow.WithActivityOptions(wfCtx, ao)
	var raw any
	if err := workflow.ExecuteActivity(actx, t.activityName, args).Get(wfCtx, &raw); err != nil {
		return nil, err
	}
	return toResultMap(raw), nil
}

func activityFuncName(v reflect.Value) string {
	full := runtime.FuncForPC(v.Pointer()).Name()
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	return strings.TrimSuffix(full, "-fm")
}

// schemaFromStruct builds a genai object schema from a Go struct, honoring json
// tags for field names and exclusion (json:"-"). It is intentionally shallow —
// enough for the model to produce well-formed arguments — and recurses into
// nested structs and slices.
func schemaFromStruct(t reflect.Type) *genai.Schema {
	s := &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag := f.Tag.Get("json"); tag != "" {
			// A json tag of exactly "-" excludes the field from JSON entirely
			// (encoding/json semantics), so the data converter can never deliver
			// it to the activity — do not advertise it to the model. The check
			// runs on the raw tag, before comma-splitting, because json:"-,"
			// means a field literally named "-".
			if tag == "-" {
				continue
			}
			if comma := strings.Index(tag, ","); comma >= 0 {
				tag = tag[:comma]
			}
			if tag != "" {
				name = tag
			}
		}
		s.Properties[name] = schemaFromType(f.Type)
	}
	return s
}

func schemaFromType(t reflect.Type) *genai.Schema {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return &genai.Schema{Type: genai.TypeString}
	case reflect.Bool:
		return &genai.Schema{Type: genai.TypeBoolean}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &genai.Schema{Type: genai.TypeInteger}
	case reflect.Float32, reflect.Float64:
		return &genai.Schema{Type: genai.TypeNumber}
	case reflect.Slice, reflect.Array:
		return &genai.Schema{Type: genai.TypeArray, Items: schemaFromType(t.Elem())}
	case reflect.Struct:
		return schemaFromStruct(t)
	default:
		return &genai.Schema{Type: genai.TypeString}
	}
}

// toResultMap normalizes an activity result into the map[string]any shape ADK
// expects. Object results are used as-is; scalars are wrapped under "result".
func toResultMap(raw any) map[string]any {
	if raw == nil {
		return map[string]any{}
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{"result": raw}
}

// ----------------------------------------------------------------------------
// Worker-side synthetic agent.Context over a read-only snapshot.
// ----------------------------------------------------------------------------

// activityToolContext implements ADK's unified agent.Context worker-side over a
// contextSnapshot. State is immutable (see immutableState); live facilities
// (artifacts, memory) are unavailable across the Activity boundary and report a
// typed error rather than silently no-op. HITL confirmation, by contrast, is
// tunneled: ToolConfirmation returns the human decision carried in the
// snapshot, and RequestConfirmation records into the local EventActions exactly
// like ADK's own tool context, so CallMcpTool can hand the pending request back
// to the workflow to be replayed there. It embeds
// agent.StrictContextMock so it keeps satisfying agent.Context as that interface
// grows; only the methods a reconstructed, read-only tool context can honor are
// overridden below. Any un-overridden method is one a tool cannot meaningfully
// use across the Activity boundary and panics loudly rather than silently
// returning a zero value.
type activityToolContext struct {
	agent.StrictContextMock
	snap  contextSnapshot
	state *immutableState
	acts  *session.EventActions
}

func newActivityToolContext(ctx context.Context, snap contextSnapshot) *activityToolContext {
	return &activityToolContext{
		StrictContextMock: agent.NewStrictContextMock(ctx),
		snap:              snap,
		state:             &immutableState{data: snap.State},
		acts:              &session.EventActions{StateDelta: map[string]any{}},
	}
}

func (c *activityToolContext) UserContent() *genai.Content          { return nil }
func (c *activityToolContext) InvocationID() string                 { return c.snap.InvocationID }
func (c *activityToolContext) AgentName() string                    { return c.snap.AgentName }
func (c *activityToolContext) UserID() string                       { return c.snap.UserID }
func (c *activityToolContext) AppName() string                      { return c.snap.AppName }
func (c *activityToolContext) SessionID() string                    { return c.snap.SessionID }
func (c *activityToolContext) Branch() string                       { return c.snap.Branch }
func (c *activityToolContext) FunctionCallID() string               { return c.snap.FunctionCallID }
func (c *activityToolContext) ReadonlyState() session.ReadonlyState { return c.state }
func (c *activityToolContext) State() session.State                 { return c.state }
func (c *activityToolContext) Actions() *session.EventActions       { return c.acts }
func (c *activityToolContext) Artifacts() agent.Artifacts           { return nil }

// IsolationScope and ResumedInput carry no meaning for a reconstructed tool
// context, so return their empty forms instead of panicking like the embedded
// mock: a tool that reads them across the Activity boundary should see "unset".
func (c *activityToolContext) IsolationScope() string          { return "" }
func (c *activityToolContext) ResumedInput(string) (any, bool) { return nil, false }

func (c *activityToolContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, newApplicationError(ErrorTypeTool, false, nil,
		"SearchMemory is not available inside a Temporal Activity")
}

// ToolConfirmation returns the human decision carried across the Activity
// boundary in the snapshot (nil when none), so ADK's standard worker-side
// confirmation check (e.g. mcptoolset's RequireConfirmation) sees the decision
// on the resume pass.
func (c *activityToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.snap.Confirmation
}

// RequestConfirmation mirrors ADK's own tool context: it records the pending
// confirmation into the local EventActions keyed by the function call ID. The
// CallMcpTool Activity returns it to the workflow, whose proxy re-records it
// workflow-side so the runner pauses for the human.
func (c *activityToolContext) RequestConfirmation(hint string, payload any) error {
	if c.snap.FunctionCallID == "" {
		return newApplicationError(ErrorTypeTool, false, nil,
			"function call id not set when requesting confirmation")
	}
	if c.acts.RequestedToolConfirmations == nil {
		c.acts.RequestedToolConfirmations = make(map[string]toolconfirmation.ToolConfirmation)
	}
	c.acts.RequestedToolConfirmations[c.snap.FunctionCallID] = toolconfirmation.ToolConfirmation{Hint: hint, Payload: payload}
	c.acts.SkipSummarization = true
	return nil
}

// errImmutableState is returned when tool code attempts to mutate the read-only
// state view shipped across the Activity boundary. The mutation would evaporate
// (Activities do not propagate state back into the workflow), so it fails loudly.
var errImmutableState = errors.New(
	"googleadk: tool state is read-only inside a Temporal Activity; " +
		"mutations do not propagate back to the workflow")

// immutableState implements session.State and session.ReadonlyState over a
// copied map. Get/All read; Set fails with errImmutableState.
type immutableState struct {
	data map[string]any
}

func (s *immutableState) Get(key string) (any, error) {
	v, ok := s.data[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

// Set always fails: the state view is a read-only copy. The failure is
// non-retryable because retrying a programming error that can never succeed
// would only burn the Activity's retry budget.
func (s *immutableState) Set(string, any) error {
	return newApplicationError(ErrorTypeTool, false, errImmutableState, "%s", errImmutableState.Error())
}

func (s *immutableState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}
