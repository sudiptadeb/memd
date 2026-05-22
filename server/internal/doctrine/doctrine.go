// Package doctrine ships the MCP `instructions` payload that the server sends
// to every connecting agent. Keep doctrine.md here in sync with the canonical
// copy at docs/doctrine.md (build/build.sh refreshes it before each build).
package doctrine

import _ "embed"

//go:embed doctrine.md
var Text string
