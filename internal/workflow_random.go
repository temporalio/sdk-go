package internal

import (
	"crypto/sha256"
	"io"
	"math/rand/v2"
)

const seedVersion = "temporal.sdk.random.v1"

func deriveSeed(runID, name string) [32]byte {
	// The separators keep ("ab", "c") from colliding with ("a", "bc")
	return sha256.Sum256([]byte(seedVersion + "\x00" + runID + "\x00" + name))
}

func getRandomStream(randoms map[string]*workflowRandomStream, runID, name string) *workflowRandomStream {
	if r, ok := randoms[name]; ok {
		return r
	}

	randoms[name] = &workflowRandomStream{source: rand.NewChaCha8(deriveSeed(runID, name))}
	return randoms[name]
}

func reseedRandoms(randoms map[string]*workflowRandomStream, newRunID string) {
	for name, r := range randoms {
		r.reseed(deriveSeed(newRunID, name)) // Seed in place so workflow held references are updated
	}
}

// Exposed as: [go.temporal.io/sdk/workflow.WorkflowRandomStream]
type WorkflowRandomStream interface {
	rand.Source
	io.Reader
}

// workflowRandomStream wraps ChaCha8 instead of embedding it so callers cannot
// mutate a shared named stream through Seed or UnmarshalBinary.
//
// TODO: Define stable interleaving semantics. See [ChaCha8.Read].
//
// [ChaCha8.Read]: https://go.dev/src/math/rand/v2/chacha8.go#L48
type workflowRandomStream struct {
	source *rand.ChaCha8
}

func (r *workflowRandomStream) Uint64() uint64 {
	return r.source.Uint64()
}

func (r *workflowRandomStream) Read(p []byte) (int, error) {
	return r.source.Read(p)
}

func (r *workflowRandomStream) reseed(seed [32]byte) {
	r.source.Seed(seed)
}

// Exposed as: [go.temporal.io/sdk/workflow.GetRandomStream]
func GetRandomStream(ctx Context, name string) WorkflowRandomStream {
	return getWorkflowEnvironment(ctx).GetRandomStream(name)
}
