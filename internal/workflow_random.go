package internal

import (
	"crypto/sha256"
	"math/rand/v2"
)

const seedVersion = "temporal.sdk.random.v1"

func deriveSeed(runID, name string) [32]byte {
	// The separators keep ("ab", "c") from colliding with ("a", "bc")
	return sha256.Sum256([]byte(seedVersion + "\x00" + runID + "\x00" + name))
}

func getRandom(randoms map[string]*rand.ChaCha8, runID, name string) *rand.ChaCha8 {
	if r, ok := randoms[name]; ok {
		return r
	}

	// The ChaCha8 source is stored rather than a *rand.Rand wrapper so both layers stay reachable.
	// In particular, Read is only available on the concrete source (golang/go#67059).
	randoms[name] = rand.NewChaCha8(deriveSeed(runID, name))
	return randoms[name]
}

func reseedRandoms(randoms map[string]*rand.ChaCha8, newRunID string) {
	for name, r := range randoms {
		r.Seed(deriveSeed(newRunID, name)) // Seed in place so workflow held references are updated
	}
}

func GetRandom(ctx Context, name string) *rand.ChaCha8 {
	return getWorkflowEnvironment(ctx).GetRandom(name)
}
