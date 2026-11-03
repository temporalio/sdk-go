package internal

// LocalVar is a value local to a single workflow run.
//
// Exposed as: [go.temporal.io/sdk/workflow.LocalVar]
type LocalVar[T any] struct {
	key   *workflowLocalKey
	store *workflowLocalStore
}

type workflowLocalKey struct {
	// Keep keys non-zero-sized so distinct allocations have distinct addresses.
	_ byte
}

// NewLocalVar creates a LocalVar bound to the workflow run for ctx.
//
// Exposed as: [go.temporal.io/sdk/workflow.NewLocalVar]
func NewLocalVar[T any](ctx Context) LocalVar[T] {
	return LocalVar[T]{
		key:   &workflowLocalKey{},
		store: getWorkflowLocalStore(ctx),
	}
}

// Get returns the value associated with v in the workflow run for ctx, or the
// zero value of T if v has not been set.
func (v LocalVar[T]) Get(ctx Context) T {
	store := v.storeFor(ctx)
	value, ok := store.values[v.key]
	if !ok || value == nil {
		var zero T
		return zero
	}
	return value.(T)
}

// Set associates value with v in the workflow run for ctx.
func (v LocalVar[T]) Set(ctx Context, value T) {
	store := v.storeFor(ctx)
	assertWorkflowLocalWritable(ctx)
	store.values[v.key] = value
}

func (v LocalVar[T]) storeFor(ctx Context) *workflowLocalStore {
	if v.key == nil || v.store == nil {
		panic("workflow.LocalVar is not initialized; use workflow.NewLocalVar(ctx)")
	}
	store := getWorkflowLocalStore(ctx)
	if store != v.store {
		panic("workflow.LocalVar: context belongs to a different workflow run")
	}
	return store
}

type workflowLocalStore struct {
	values map[*workflowLocalKey]any
}

type workflowLocalStoreContextKey struct{}

func workflowContextWithLocalStore(ctx Context) Context {
	return WithValue(ctx, workflowLocalStoreContextKey{}, &workflowLocalStore{
		values: make(map[*workflowLocalKey]any),
	})
}

func getWorkflowLocalStore(ctx Context) *workflowLocalStore {
	if ctx == nil {
		panic("workflow.LocalVar: nil workflow context")
	}
	store, ok := ctx.Value(workflowLocalStoreContextKey{}).(*workflowLocalStore)
	if !ok {
		panic("workflow.LocalVar: context is not associated with a workflow run")
	}
	return store
}

func assertWorkflowLocalWritable(ctx Context) {
	state, _ := ctx.Value(coroutinesContextKey).(*coroutineState)
	if state != nil && state.dispatcher.getIsReadOnly() {
		panic(panicIllegalAccessCoroutineState)
	}
}
