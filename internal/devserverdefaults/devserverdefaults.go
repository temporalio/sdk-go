// Package devserverdefaults holds settings shared between dev-server callsites.
package devserverdefaults

// CLIVersion is the Temporal CLI release used by integration test dev servers.
const CLIVersion = "v1.7.1-standalone-nexus-operations"

// SQLitePragmas returns the SQLite tuning flags to splice into a dev server's
// ExtraArgs.
func SQLitePragmas() []string {
	return []string{
		"--sqlite-pragma", "journal_mode=WAL",
		"--sqlite-pragma", "synchronous=OFF",
	}
}
