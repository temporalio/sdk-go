package internal

// PinnedDevServerVersion is the Temporal CLI release the SDK's own test suites
// run against. It is pinned rather than tracking latest because the tests
// depend on server behaviour that moves between releases.
//
// This is a CLI version, not a server version: testsuite resolves it through
// temporal.download/cli, and the server is whichever one that CLI embeds.
// v1.9.3-nexus-serialization-context embeds Server 1.33.0 (main @ 0d4fd4e).
// The tag is named for an unrelated Nexus feature; the server is why it is
// pinned here, as it is the earliest build with CHASM schedule time skipping.
//
// It lives in internal rather than testsuite so the build tool and the test
// module can share one literal without it becoming public API. Both are inside
// go.temporal.io/sdk/... and so may import this package, despite being separate
// Go modules.
const PinnedDevServerVersion = "v1.9.3-nexus-serialization-context"
