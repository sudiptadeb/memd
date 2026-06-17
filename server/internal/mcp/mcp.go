// Package mcp implements a minimal MCP (Model Context Protocol) server
// over HTTP. It supports initialize, tools/list, and tools/call — enough
// for clients like Claude Code and Codex CLI to discover and invoke the
// memory_* storage tools and memd_* workflow tools that this server exposes.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sudiptadeb/memd/server/internal/config"
	"github.com/sudiptadeb/memd/server/internal/doctrine"
	"github.com/sudiptadeb/memd/server/internal/feature"
	"github.com/sudiptadeb/memd/server/internal/graph"
	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/storage"
	"github.com/sudiptadeb/memd/server/internal/tasks"
)

const protocolVersion = "2025-03-26"

// Preload budget for MEMORY.md in activeMemorySection. memory_load clamps the
// preloaded index to whichever limit is hit first, cutting on a line boundary.
const (
	memoryIndexPreloadMaxLines = 200
	memoryIndexPreloadMaxBytes = 25 << 10
)

// fileSizeWarnBytes is the per-file size above which toolWrite warns the agent
// to split content into smaller focused files.
const fileSizeWarnBytes = 100 << 10

// Server is an MCP endpoint backed by a registry.
type Server struct {
	reg        *registry.Registry
	live       *doctrine.Live    // global doctrine + feature base doctrines (live-editable)
	features   *feature.Registry // built-in structured-memory features
	serverName string
	serverVer  string

	// clock supplies "now" for time-derived rendering (e.g. flagging overdue
	// tasks). nil means time.Now().UTC(); tests override it for determinism.
	clock func() time.Time

	mu    sync.Mutex
	loads map[string]loadState // connector ID → memory_load tracking for the current client session
}

// now returns the current date/time used for derived rendering.
func (s *Server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UTC()
}

// loadState tracks whether a connector's current client session has called
// memory_load, and whether the one-time before-load nudge already fired.
// State is in-memory only: each MCP initialize re-arms the guard for a fresh
// conversation, and a server restart merely lets one nudge fire again.
type loadState struct {
	loaded bool
	nudged bool
}

func New(reg *registry.Registry, live *doctrine.Live, features *feature.Registry, name, version string) *Server {
	return &Server{
		reg:        reg,
		live:       live,
		features:   features,
		serverName: name,
		serverVer:  version,
		loads:      map[string]loadState{},
	}
}

// instructionsText returns the current global doctrine (an admin may have
// overridden it at runtime via the Live store).
func (s *Server) instructionsText() string {
	if s.live == nil {
		return ""
	}
	return s.live.Get(doctrine.GlobalID)
}

// Mount registers the MCP handler under prefix. Tokens may be supplied either
// as prefix + "/<token>" or as Authorization: Bearer <token>.
func (s *Server) Mount(mux *http.ServeMux, prefix string) {
	prefix = mountPrefix(prefix)
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, prefix) })
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) { s.handle(w, r, prefix) })
}

// MountHTTP registers simple token-authenticated HTTP endpoints for agents
// that can fetch URLs but cannot speak MCP.
func (s *Server) MountHTTP(mux *http.ServeMux, prefix string) {
	prefix = mountPrefix(prefix)
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) { s.handleHTTP(w, r, prefix) })
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) { s.handleHTTP(w, r, prefix) })
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, prefix string) {
	conn, tail, ok := s.connectorFromRequest(r, prefix, config.ConnectorKindMCP)
	if !ok || tail != "" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	resp := s.dispatch(conn, &req)
	if resp == nil {
		// JSON-RPC notification — MCP Streamable HTTP spec requires
		// 202 Accepted with no body. Strict clients (rmcp / Codex CLI)
		// treat any other status as a protocol error and close the
		// transport.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// --- JSON-RPC envelope ---

type rpcReq struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type rpcResp struct {
	Jsonrpc string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResp{
		Jsonrpc: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

// --- Dispatch ---

func (s *Server) dispatch(conn *registry.Connector, req *rpcReq) *rpcResp {
	switch req.Method {
	case "initialize":
		logs.InfoUser(conn.OwnerUserID, "MCP initialize from connector %q", conn.Name)
		return s.handleInitialize(conn, req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(conn, req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		logs.InfoUser(conn.OwnerUserID, "MCP prompts/get from connector %q", conn.Name)
		return s.handlePromptsGet(conn, req)
	case "ping":
		return &rpcResp{Jsonrpc: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *Server) handleInitialize(conn *registry.Connector, req *rpcReq) *rpcResp {
	// A new initialize marks a new client session: re-arm the load-first
	// guard so the next conversation gets its own nudge if it skips
	// memory_load.
	s.resetLoad(conn.ID)
	// The instructions field carries the doctrine only (stable, small).
	// Active memory content arrives via the memory_load tool, which the
	// doctrine instructs the agent to call as its first action.
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":   map[string]any{},
				"prompts": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    s.serverName,
				"version": s.serverVer,
			},
			"instructions": s.instructionsText(),
		},
	}
}

// guardedByLoad reports whether a tool reads or mutates memory content and
// therefore expects memory_load to have run first this session. Introspection
// tools (memory_directories, memory_status) and the memd_* workflows — whose
// bodies embed their own memory_load step — stay unguarded.
func guardedByLoad(name string) bool {
	switch name {
	case "memory_search", "memory_read", "memory_list", "memory_graph",
		"memory_write", "memory_move", "memory_delete", "memory_delete_folder":
		return true
	}
	return false
}

// nudgeBeforeLoad reports, exactly once per session, that a guarded tool was
// called before memory_load — the caller returns a soft error telling the
// agent to load first and retry. Later calls pass through, so an agent that
// ignores the nudge can never livelock on it.
func (s *Server) nudgeBeforeLoad(connID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loads[connID]
	if st.loaded || st.nudged {
		return false
	}
	st.nudged = true
	s.loads[connID] = st
	return true
}

func (s *Server) markLoaded(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loads[connID]
	st.loaded = true
	s.loads[connID] = st
}

func (s *Server) resetLoad(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loads, connID)
}

// activeMemorySection composes a snapshot of the connector's accessible
// directories. For each directory it renders:
//
//   - the directory's metadata (id, backend, purpose),
//   - a shallow topology — root entries plus the direct children of memory/
//     if present — so the agent can see where things live without paying for
//     a recursive listing,
//   - the full contents of MEMORY.md (the canonical entry file).
//
// For deeper navigation the agent uses memory_list / memory_read.
func (s *Server) activeMemorySection(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	var sb strings.Builder
	sb.WriteString("# Active Memory\n\n")
	if len(dirs) == 0 {
		sb.WriteString("_No directories are accessible through this connector._\n")
		return sb.String()
	}
	sb.WriteString("Regenerated on every `memory_load` call — the current state of memory. Treat the contents below as memory you already know. For deeper navigation, call `memory_list` on a folder or `memory_read` on a specific file.\n\n")
	sb.WriteString(s.structuredMemoryDoctrine(dirs))
	for _, d := range dirs {
		fmt.Fprintf(&sb, "### %s\n\n", d.Directory.Name)
		fmt.Fprintf(&sb, "- id: `%s`\n", d.Directory.ID)
		fmt.Fprintf(&sb, "- backend: %s\n", d.Directory.Backend)
		if d.Directory.Description != "" {
			fmt.Fprintf(&sb, "- purpose: %s\n", d.Directory.Description)
		}
		sb.WriteString("\n")

		root, err := d.Backend.ListPath("")
		if err != nil {
			fmt.Fprintf(&sb, "_(could not list directory: %v)_\n\n", err)
			continue
		}
		sb.WriteString("**Topology (root + first layer of each top-level folder):**\n\n```\n")
		writeTopology(&sb, d.Backend, root)
		sb.WriteString("```\n\n")

		sb.WriteString(s.featureStateSection(d, root))

		body, err := d.Backend.Read("MEMORY.md")
		if err != nil {
			sb.WriteString("_(MEMORY.md missing — bootstrap with `memory_write`)_\n\n")
			continue
		}
		sb.WriteString("**`MEMORY.md`:**\n\n```markdown\n")
		body, lines, truncated := clampPreload(body)
		sb.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			sb.WriteString("\n")
		}
		if truncated {
			fmt.Fprintf(&sb, "[memd: MEMORY.md truncated at %d lines / %dKB for preload — memory_read(\"MEMORY.md\") returns the full index; consider a reorganise pass]\n", lines, memoryIndexPreloadMaxBytes>>10)
		}
		sb.WriteString("```\n\n")
	}
	return sb.String()
}

// enabledFeatures returns the enabled, available (non-coming-soon) features for
// a directory, in the order they are stored on the directory.
func (s *Server) enabledFeatures(d registry.DirectoryView) []feature.Feature {
	if s.features == nil {
		return nil
	}
	var feats []feature.Feature
	for _, df := range d.Directory.Features {
		if !df.Enabled {
			continue
		}
		f, ok := s.features.Lookup(df.Key)
		if !ok || f.ComingSoon {
			continue
		}
		feats = append(feats, f)
	}
	return feats
}

// structuredMemoryDoctrine renders the base doctrine for every structured-memory
// kind enabled anywhere in this load — exactly once, no matter how many
// directories enable it. The doctrine is identical across directories (only the
// per-directory preferences and state differ), so repeating it per directory
// would waste preload budget. Each directory's live state and preferences are
// rendered separately by featureStateSection.
func (s *Server) structuredMemoryDoctrine(dirs []registry.DirectoryView) string {
	var order []feature.Feature
	seen := map[string]bool{}
	where := map[string][]string{}
	for _, d := range dirs {
		for _, f := range s.enabledFeatures(d) {
			if !seen[f.Key] {
				seen[f.Key] = true
				order = append(order, f)
			}
			where[f.Key] = append(where[f.Key], d.Directory.Name)
		}
	}
	if len(order) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Structured memory\n\n")
	sb.WriteString("Beyond freeform notes, some directories keep these structured kinds of memory. The rules for each kind apply wherever it is enabled; each directory's current state and preferences appear in that directory's section below.\n\n")
	for _, f := range order {
		fmt.Fprintf(&sb, "### %s\n\n%s\n\n", f.Name, f.AgentSummary)
		base := ""
		if s.live != nil {
			base = s.live.Get(doctrine.FeatureID(f.Key))
		}
		if base == "" {
			base = f.BaseDoctrine()
		}
		sb.WriteString(base)
		fmt.Fprintf(&sb, "\n\n_Enabled in: %s._\n\n", strings.Join(where[f.Key], ", "))
	}
	return sb.String()
}

// featureStateSection renders the per-directory state of each enabled feature: a
// derived, always-fresh summary (for tasks, an open/overdue/due-soon digest
// scanned from the files) plus the directory's user-preference overlay from
// <folder>/_feature.md. The shared how-to doctrine lives once in
// structuredMemoryDoctrine, so this section stays compact. root is the
// directory's already-listed root entries, used to skip features whose folder
// does not exist yet without an extra listing.
func (s *Server) featureStateSection(d registry.DirectoryView, root []storage.DirEntry) string {
	feats := s.enabledFeatures(d)
	if len(feats) == 0 {
		return ""
	}
	hasFolder := map[string]bool{}
	for _, e := range root {
		if e.IsDir {
			hasFolder[e.Name] = true
		}
	}
	var sb strings.Builder
	for _, f := range feats {
		fmt.Fprintf(&sb, "**%s** (structured memory):\n", f.Name)
		if f.Key == "tasks" {
			sb.WriteString(s.taskState(d, f, hasFolder[f.Folder]))
		}
		if d.Backend != nil && hasFolder[f.Folder] {
			if prefs, err := d.Backend.ReadRaw(f.Folder + "/_feature.md"); err == nil {
				if t := strings.TrimSpace(string(prefs)); t != "" {
					fmt.Fprintf(&sb, "\nPreferences (%s/_feature.md):\n%s\n", f.Folder, t)
				}
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// maxListedTasks caps how many overdue / due-soon task lines are spelled out in
// the derived summary, keeping the preload bounded; the rest are counted.
const maxListedTasks = 5

// taskState renders a directory's tasks folder as a derived summary for the
// preload, reusing the built-in tasks grammar/board so the agent sees the same
// open/overdue/due-soon view the dashboard does. Files are read with ReadRaw so
// the snapshot never bumps managed access stats or triggers a backend write.
func (s *Server) taskState(d registry.DirectoryView, f feature.Feature, folderExists bool) string {
	if d.Backend == nil || !folderExists {
		return "- no tasks yet\n"
	}
	entries, err := d.Backend.ListPath(f.Folder)
	if err != nil {
		return ""
	}
	var lists []tasks.List
	for _, e := range entries {
		if e.IsDir || !tasks.IsListFile(e.Name) {
			continue
		}
		body, err := d.Backend.ReadRaw(e.Path)
		if err != nil {
			continue
		}
		lists = append(lists, tasks.BuildList(e.Path, tasks.DisplayName(e.Name), body))
	}
	return renderTaskBoard(tasks.BuildBoard(lists, s.now()))
}

// renderTaskBoard turns a derived board into the compact preload summary: a
// counts line, then the overdue and due-soon tasks (capped).
func renderTaskBoard(b tasks.Board) string {
	var open, total int
	for _, l := range b.Lists {
		open += l.Open
		total += l.Total
	}
	if total == 0 {
		return "- no tasks yet\n"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "- %d open · %d done", open, total-open)
	if len(b.Overdue) > 0 {
		fmt.Fprintf(&sb, " · %d overdue", len(b.Overdue))
	}
	if len(b.DueSoon) > 0 {
		fmt.Fprintf(&sb, " · %d due soon", len(b.DueSoon))
	}
	sb.WriteString("\n")
	writeTaskItems(&sb, "overdue", b.Overdue)
	writeTaskItems(&sb, "due soon", b.DueSoon)
	return sb.String()
}

func writeTaskItems(sb *strings.Builder, label string, items []tasks.Task) {
	for i, t := range items {
		if i >= maxListedTasks {
			fmt.Fprintf(sb, "  - …and %d more %s\n", len(items)-maxListedTasks, label)
			return
		}
		title := t.Title
		for _, tag := range t.Tags {
			title += " #" + tag
		}
		due := ""
		if t.Due != "" {
			due = " (due " + t.Due + ")"
		}
		fmt.Fprintf(sb, "  - %s: %s%s — %s\n", label, title, due, t.File)
	}
}

// clampPreload limits a MEMORY.md body to the first memoryIndexPreloadMaxLines
// lines or memoryIndexPreloadMaxBytes, whichever is hit first, always cutting
// on a line boundary. It returns the (possibly shortened) body, the number of
// lines kept, and whether truncation occurred. A body inside both limits is
// returned unchanged so untruncated indexes render byte-identically.
func clampPreload(body []byte) ([]byte, int, bool) {
	if !overPreloadBudget(body) {
		return body, 0, false
	}
	lines := 0
	kept := 0 // bytes kept, ending on a line boundary
	for i := 0; i < len(body); {
		nl := i
		for nl < len(body) && body[nl] != '\n' {
			nl++
		}
		end := nl
		if end < len(body) {
			end++ // include the newline
		}
		if lines >= memoryIndexPreloadMaxLines || end > memoryIndexPreloadMaxBytes {
			break
		}
		kept = end
		lines++
		i = end
	}
	return body[:kept], lines, true
}

// overPreloadBudget reports whether a MEMORY.md body exceeds the preload budget
// in lines or bytes.
func overPreloadBudget(body []byte) bool {
	if len(body) > memoryIndexPreloadMaxBytes {
		return true
	}
	return countLines(body) > memoryIndexPreloadMaxLines
}

// countLines returns the number of text lines in body. A trailing newline does
// not add an empty final line; an unterminated final line is still counted.
func countLines(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	n := bytes.Count(body, []byte{'\n'})
	if body[len(body)-1] != '\n' {
		n++
	}
	return n
}

// writeTopology renders the root entries plus the first layer of every
// top-level folder. Files at root print as-is. Folders print their name
// followed by their direct children indented one level; nested folders
// are summarised with a child count rather than walked further.
//
// This makes the active-memory snapshot useful regardless of the
// directory's chosen layout (single `memory/` vs. multiple thematic
// folders like `notes/` + `projects/` + `preferences/`).
func writeTopology(sb *strings.Builder, b storage.Backend, root []storage.DirEntry) {
	for _, e := range root {
		if !e.IsDir {
			fmt.Fprintf(sb, "%s\n", e.Name)
			continue
		}
		children, err := b.ListPath(e.Path)
		if err != nil {
			fmt.Fprintf(sb, "%s/  (could not list)\n", e.Name)
			continue
		}
		fmt.Fprintf(sb, "%s/\n", e.Name)
		for _, c := range children {
			if c.IsDir {
				deep, _ := b.ListPath(c.Path)
				fmt.Fprintf(sb, "  %s/  (%d items)\n", c.Name, len(deep))
			} else {
				fmt.Fprintf(sb, "  %s\n", c.Name)
			}
		}
	}
}

// --- Tool catalog ---

var toolsCatalog = []map[string]any{
	// Storage tools — agent-internal primitives. The agent calls these to
	// read and write memory while executing user requests or workflows.
	// Users should invoke the memd_* workflow tools (or the equivalent
	// slash-command prompts), not these.
	// memory_load deliberately skips the [Agent-internal storage primitive.]
	// tag the other primitives carry: clients that defer tool loading
	// truncate descriptions to roughly the first 80 characters, and the
	// call-first imperative must survive that cut.
	{
		"name":        "memory_load",
		"description": "CALL THIS FIRST, every conversation, before responding to anything else. Returns your active memory — every accessible directory's description, file listing, and the full contents of its top-level MEMORY.md. Treat its result as memory you already know.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_directories",
		"description": "[Agent-internal storage primitive.] List the memory directories this connector can access (no content). Rarely needed — memory_load returns more.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_search",
		"description": "[Agent-internal storage primitive.] Search text memory files for a query. Returns matching lines with file paths. Binary files are skipped.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string", "description": "Text to search for (case-insensitive)."},
				"directory_id": map[string]any{"type": "string", "description": "Restrict the search to one directory. If omitted, all visible directories are searched."},
				"limit":        map[string]any{"type": "integer", "description": "Maximum number of hits. Default 50."},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "memory_read",
		"description": "[Agent-internal storage primitive.] Read one memory file in full. Markdown and HTML files get last_read_at/access_count updates in memd: front matter (HTML uses a leading comment); other files are returned unchanged.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string", "description": "File path relative to the directory root (e.g. 'MEMORY.md', 'memory/feedback/foo.md', or 'memory/mock-ui.html')."},
			},
			"required": []string{"directory_id", "path"},
		},
	},
	{
		"name":        "memory_list",
		"description": "[Agent-internal storage primitive.] List the direct children of a path inside a memory directory. Use to dive into a folder the Active Memory topology shows by name. Pass an empty path (or omit it) to list the directory root.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string", "description": "Path relative to the directory root. Empty or '.' = root."},
			},
			"required": []string{"directory_id"},
		},
	},
	{
		"name":        "memory_write",
		"description": "[Agent-internal storage primitive.] Create or update a memory file. For Markdown and HTML files, any memd: front-matter block in the content is discarded; the server owns that subtree. HTML metadata is stored in a leading comment. Other file types are stored verbatim. For git-backed directories the server debounces commit + push.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"content":      map[string]any{"type": "string"},
				"message":      map[string]any{"type": "string", "description": "Optional commit message for git-backed directories."},
			},
			"required": []string{"directory_id", "path", "content"},
		},
	},
	{
		"name":        "memory_move",
		"description": "[Agent-internal storage primitive.] Rename or move a file or folder from src to dst inside the directory. Preferred over write-then-delete because git tracks it as a rename (history follows the file). Fails if dst already exists. Cannot move MEMORY.md at the root.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"src":          map[string]any{"type": "string", "description": "Current path of the file or folder."},
				"dst":          map[string]any{"type": "string", "description": "New path."},
				"message":      map[string]any{"type": "string", "description": "Optional commit message for git-backed directories."},
			},
			"required": []string{"directory_id", "src", "dst"},
		},
	},
	{
		"name":        "memory_delete",
		"description": "[Agent-internal storage primitive.] Delete a single file. Use with care — prefer memory_move into _archive/ when the content might matter historically. Cannot delete MEMORY.md at the root. For folders, use memory_delete_folder.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"message":      map[string]any{"type": "string", "description": "Optional commit message for git-backed directories."},
			},
			"required": []string{"directory_id", "path"},
		},
	},
	{
		"name":        "memory_delete_folder",
		"description": "[Agent-internal storage primitive.] Recursively delete a folder and everything inside it. Heavy operation — prefer memory_move into _archive/ for individual files. Cannot delete the directory root.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"message":      map[string]any{"type": "string", "description": "Optional commit message for git-backed directories."},
			},
			"required": []string{"directory_id", "path"},
		},
	},
	{
		"name":        "memory_status",
		"description": "[Agent-internal storage primitive.] Report backend status for each visible directory (last sync, last error).",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_graph",
		"description": "[Agent-internal storage primitive.] Return the link graph of a directory: how files connect via markdown links. With no path, returns a summary — totals, orphan files (no links in or out), and broken links (targets that don't exist). With a path, returns that file's neighbours (outbound + inbound links). Use it to navigate by relationship, find disconnected memory, or spot dead links before a housekeep.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string", "description": "Optional: a file to centre on. Omit for a whole-directory summary."},
			},
			"required": []string{"directory_id"},
		},
	},
	// Workflow tools — equivalent to the MCP prompts of the same root name.
	// MCP prompts only surface as slash commands in some clients (Claude
	// Code yes; Codex CLI no). Exposing the same workflows as tools means
	// every client can invoke them. Distinct namespace (`memd_`) keeps
	// them visually separate from the storage tools (`memory_`).
	{
		"name":        "memd_reorganise",
		"description": "Workflow: rearrange the shelves — restructure existing memory, group root files into folders, rewrite MEMORY.md as a curated sectioned index. Returns the workflow body; follow its steps. Same as the /<connector>:reorganise prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to reorganise."},
			},
		},
	},
	{
		"name":        "memd_harvest",
		"description": "Workflow: bring in the crop — gather knowledge from sources OUTSIDE memd (Claude auto-memory, Cursor rules, raw notes, another memd directory) and integrate via ADD/UPDATE/DELETE/NONE. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:harvest prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to harvest into."},
			},
		},
	},
	{
		"name":        "memd_dream",
		"description": "Workflow: sleep consolidation — forget unused / contradicted files, cement what was referenced this session. Uses managed memd: stats for Markdown/HTML where available. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:dream prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to dream over."},
			},
		},
	},
	{
		"name":        "memd_recall",
		"description": "Workflow: reminisce on a topic — search, walk linked pages, synthesise an answer. Returns the workflow body; follow its steps. Same as the /<connector>:recall prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic":        map[string]any{"type": "string", "description": "What to recall (free text)."},
				"directory_id": map[string]any{"type": "string", "description": "Optional: restrict to one directory."},
			},
			"required": []string{"topic"},
		},
	},
	{
		"name":        "memd_housekeep",
		"description": "Workflow: daily tidying — fix dangling links, orphan files, missing Markdown front matter, stale last_reorganised. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:housekeep prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to housekeep."},
			},
		},
	},
}

func (s *Server) handleToolsList(req *rpcReq) *rpcResp {
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": toolsCatalog},
	}
}

// --- Tool execution ---

func (s *Server) handleToolsCall(conn *registry.Connector, req *rpcReq) *rpcResp {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}

	logs.InfoUser(conn.OwnerUserID, "MCP tools/call %s from %q", params.Name, conn.Name)
	if guardedByLoad(params.Name) && s.nudgeBeforeLoad(conn.ID) {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "memory_load has not been called in this session — call memory_load first to load active memory, then retry " + params.Name + "."},
				},
				"isError": true,
			},
		}
	}
	var (
		text  string
		isErr bool
	)
	switch params.Name {
	case "memory_load":
		s.markLoaded(conn.ID)
		text = s.activeMemorySection(conn)
	case "memory_directories":
		text = s.toolDirectories(conn)
	case "memory_list":
		text, isErr = s.toolListPath(conn, params.Arguments)
	case "memory_search":
		text, isErr = s.toolSearch(conn, params.Arguments)
	case "memory_read":
		text, isErr = s.toolRead(conn, params.Arguments)
	case "memory_write":
		text, isErr = s.toolWrite(conn, params.Arguments)
	case "memory_move":
		text, isErr = s.toolMove(conn, params.Arguments)
	case "memory_delete":
		text, isErr = s.toolDelete(conn, params.Arguments)
	case "memory_delete_folder":
		text, isErr = s.toolDeleteFolder(conn, params.Arguments)
	case "memory_status":
		text = s.toolStatus(conn)
	case "memory_graph":
		text, isErr = s.toolGraph(conn, params.Arguments)
	case "memd_reorganise", "memd_harvest", "memd_dream", "memd_recall", "memd_housekeep":
		text, isErr = s.toolWorkflow(params.Name, params.Arguments)
	default:
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "unknown tool: " + params.Name},
		}
	}

	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
			"isError": isErr,
		},
	}
}

func (s *Server) toolDirectories(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	if len(dirs) == 0 {
		return "(no directories accessible)"
	}
	var sb strings.Builder
	for _, d := range dirs {
		fmt.Fprintf(&sb, "id=%s  name=%q  backend=%s\n  description: %s\n",
			d.Directory.ID, d.Directory.Name, d.Directory.Backend, d.Directory.Description)
	}
	return sb.String()
}

func (s *Server) toolSearch(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		Query       string `json:"query"`
		DirectoryID string `json:"directory_id"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	if a.Query == "" {
		return "query is required", true
	}
	dirs := s.reg.DirectoriesForConnector(conn)
	if a.DirectoryID != "" {
		dirs = filterDirsByID(dirs, a.DirectoryID)
		if len(dirs) == 0 {
			return "directory not accessible: " + a.DirectoryID, true
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	var sb strings.Builder
	total := 0
	for _, d := range dirs {
		hits, err := d.Backend.Search(a.Query, limit-total)
		if err != nil {
			fmt.Fprintf(&sb, "[%s] error: %v\n", d.Directory.Name, err)
			continue
		}
		for _, h := range hits {
			fmt.Fprintf(&sb, "[%s] %s:%d  %s\n", d.Directory.ID, h.Path, h.Line, h.Snippet)
			total++
			if total >= limit {
				break
			}
		}
		if total >= limit {
			break
		}
	}
	if total == 0 {
		return "(no matches)", false
	}
	return sb.String(), false
}

func (s *Server) toolRead(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	data, err := d.Backend.Read(a.Path)
	if err != nil {
		return err.Error(), true
	}
	return string(data), false
}

func (s *Server) toolWrite(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
		Content     string `json:"content"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if !d.CanWrite {
		return "directory is read-only for you: " + a.DirectoryID, true
	}
	if err := d.Backend.Write(a.Path, []byte(a.Content), a.Message); err != nil {
		return err.Error(), true
	}
	msg := fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path)
	if len(a.Content) > fileSizeWarnBytes {
		msg += " — warning: file exceeds 100KB; prefer many small focused files (split it)"
	}
	if a.Path == "MEMORY.md" && overPreloadBudget([]byte(a.Content)) {
		msg += " — warning: MEMORY.md exceeds the preload budget (200 lines / 25KB); only the first part will be preloaded by memory_load. Move detail into topic files."
	}
	return msg, false
}

func (s *Server) toolMove(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string `json:"directory_id"`
		Src         string `json:"src"`
		Dst         string `json:"dst"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if !d.CanWrite {
		return "directory is read-only for you: " + a.DirectoryID, true
	}
	if err := d.Backend.Move(a.Src, a.Dst, a.Message); err != nil {
		return err.Error(), true
	}
	return fmt.Sprintf("moved %s → %s", a.Src, a.Dst), false
}

func (s *Server) toolDelete(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if !d.CanWrite {
		return "directory is read-only for you: " + a.DirectoryID, true
	}
	if err := d.Backend.Delete(a.Path, a.Message); err != nil {
		return err.Error(), true
	}
	return "deleted " + a.Path, false
}

func (s *Server) toolDeleteFolder(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if !d.CanWrite {
		return "directory is read-only for you: " + a.DirectoryID, true
	}
	if err := d.Backend.DeleteFolder(a.Path, a.Message); err != nil {
		return err.Error(), true
	}
	return "deleted folder " + a.Path, false
}

func (s *Server) toolListPath(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	entries, err := d.Backend.ListPath(a.Path)
	if err != nil {
		return err.Error(), true
	}
	if len(entries) == 0 {
		return "(empty)", false
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir {
			children, _ := d.Backend.ListPath(e.Path)
			fmt.Fprintf(&sb, "%s/  (%d items)\n", e.Name, len(children))
		} else {
			fmt.Fprintf(&sb, "%s\n", e.Name)
		}
	}
	return sb.String(), false
}

// toolGraph renders the directory link graph as a compact text report. With a
// path it centres on one file's neighbours; otherwise it summarises orphans
// and broken links across the whole directory.
func (s *Server) toolGraph(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	g, err := graph.Build(d.Backend)
	if err != nil {
		return err.Error(), true
	}

	var sb strings.Builder
	if a.Path != "" {
		out, in := g.Neighbors(a.Path)
		fmt.Fprintf(&sb, "Neighbours of %s\n\n", a.Path)
		fmt.Fprintf(&sb, "Links out (%d):\n", len(out))
		for _, t := range out {
			fmt.Fprintf(&sb, "  → %s\n", t)
		}
		if len(out) == 0 {
			sb.WriteString("  (none)\n")
		}
		fmt.Fprintf(&sb, "Linked from (%d):\n", len(in))
		for _, t := range in {
			fmt.Fprintf(&sb, "  ← %s\n", t)
		}
		if len(in) == 0 {
			sb.WriteString("  (none)\n")
		}
		return sb.String(), false
	}

	fmt.Fprintf(&sb, "Graph: %d files, %d links, %d orphans, %d broken links.\n",
		len(g.Nodes), len(g.Edges), len(g.Orphans), len(g.Broken))
	if len(g.Orphans) > 0 {
		sb.WriteString("\nOrphans (no links in or out):\n")
		for _, p := range g.Orphans {
			fmt.Fprintf(&sb, "  • %s\n", p)
		}
	}
	if len(g.Broken) > 0 {
		sb.WriteString("\nBroken links (target missing):\n")
		for _, e := range g.Broken {
			fmt.Fprintf(&sb, "  ✗ %s → %s\n", e.From, e.To)
		}
	}
	if len(g.Orphans) == 0 && len(g.Broken) == 0 {
		sb.WriteString("\nNo orphans or broken links — the graph is well connected.\n")
	}
	return sb.String(), false
}

// --- Prompts ---

// promptsCatalog lists workflows as MCP prompts (slash commands) with NO
// arguments declared. Some clients (Claude Code) treat any declared
// argument as a UI gate, blocking the slash command on user input even
// when the argument is marked optional. Skipping the declaration fires
// the prompt immediately; the body itself asks for whatever it needs.
// The memd_* tool catalog still accepts JSON args.
var promptsCatalog = []map[string]any{
	{
		"name":        "reorganise",
		"description": "Rearrange the shelves: restructure existing memory, group root files into folders, rewrite MEMORY.md as a clean curated index, bump last_reorganised.",
	},
	{
		"name":        "harvest",
		"description": "Bring in the crop: gather knowledge from external sources (Claude auto-memory, Cursor rules, raw notes, another memd directory) and integrate via ADD/UPDATE/DELETE/NONE.",
	},
	{
		"name":        "dream",
		"description": "Sleep consolidation: forget unused / contradicted files, cement what was referenced this session. Uses managed memd: stats (last_read_at, access_count) for Markdown/HTML where available.",
	},
	{
		"name":        "recall",
		"description": "Reminisce on a topic: search memory, walk linked pages, and synthesise an answer rather than dumping raw search hits.",
	},
	{
		"name":        "housekeep",
		"description": "Daily tidying: find structural drift — dangling links, orphan files, missing Markdown front matter, stale last_reorganised. Fix in place autonomously.",
	},
}

func (s *Server) handlePromptsList(req *rpcReq) *rpcResp {
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"prompts": promptsCatalog},
	}
}

func (s *Server) handlePromptsGet(_ *registry.Connector, req *rpcReq) *rpcResp {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}
	body, desc, ok := workflowBody(params.Name, params.Arguments)
	if !ok {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "unknown prompt: " + params.Name},
		}
	}
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"description": desc,
			"messages":    promptMessage(body),
		},
	}
}

// workflowBody returns the prompt text and short description for a named
// workflow. Used both by prompts/get and by the equivalent memory_*
// workflow tools (so clients that don't surface MCP prompts as slash
// commands — e.g. Codex CLI — can still trigger the same workflow via
// tools/call).
func workflowBody(name string, args map[string]string) (text, description string, ok bool) {
	switch name {
	case "reorganise":
		return reorganiseText(args), "Rearrange the shelves.", true
	case "harvest":
		return harvestText(args), "Bring in the crop.", true
	case "dream":
		return dreamText(args), "Sleep consolidation.", true
	case "recall":
		return recallText(args), "Reminisce on a topic.", true
	case "housekeep":
		return housekeepText(args), "Daily tidying.", true
	}
	return "", "", false
}

func reorganiseText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+`Run a focused reorganisation pass on memd memory.

1. If you have not already in this session, call %smemory_load()%s.
2. %s
3. Walk every memory file with %smemory_list%s and %smemory_read%s. For each, decide:
   - **duplicated or redundant** → merge into the better file (%smemory_write%s the merged body to the canonical path; %smemory_delete%s the loser).
   - **stale or superseded** → %smemory_move%s it under %smemory/_archive/<same-name>%s; keep a one-line historical note in MEMORY.md if it still matters.
   - **related to others** → %smemory_move%s into a descriptive multi-word subfolder (e.g. %smemory/feedback/<name>.md%s, %smemory/mockups/<name>.html%s, or %smemory/data/<name>.csv%s).
4. Use %smemory_move%s for renames and folder changes — not write-then-delete. Move preserves git rename detection so the file's history follows it.
5. After the moves, walk the result and clean up: %smemory_delete_folder%s any leftover empty folders or stale subdirectories from prior incomplete passes.
6. Rewrite MEMORY.md as a curated sectioned index per the doctrine's "Curate, don't enumerate" rule. Each entry is one line: a Markdown link plus a concrete description of what's in the linked file. Bring MEMORY.md back under the preload budget (200 lines / 25KB) — move detail out into topic files so only the index is preloaded.
7. Update MEMORY.md's agent front matter: %slast_reorganised%s = today, %sentries%s = the final one-liner count.
8. Report the diff: counts of files moved / archived / merged / deleted; the new MEMORY.md section headers; anything you flagged for user attention.

Only stop for user input if the *Run in background* preamble's drastic-action triggers apply (e.g. you're about to delete content the user wrote, restructure more than a third of the directory, or delete a folder you're not 100%% sure is obsolete).`,
		"`", "`",
		dirHint(args),
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
	)
}

func dirHint(args map[string]string) string {
	if id := strings.TrimSpace(args["directory_id"]); id != "" {
		return fmt.Sprintf("Target directory id: `%s`.", id)
	}
	return "If multiple directories are accessible, ask the user which one to operate on."
}

func promptMessage(text string) []map[string]any {
	return []map[string]any{
		{
			"role": "user",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	}
}

// backgroundPreamble is prepended to long-running passes (harvest, dream,
// housekeep) so the agent dispatches them to a background worker when one
// is available — keeping the main conversation responsive — and so the
// agent acts autonomously instead of stopping for approval ceremonies.
const backgroundPreamble = `**Run in background if you can. Act autonomously; report when done.**

If your environment has a Task / subagent / background-agent capability (Claude Code's Task tool, Codex's worker, Cursor's background agent), dispatch this entire pass to one and immediately return control to the user.

**Default to acting.** Do the work, then report what was done. Don't stop to ask permission for ordinary changes — every change is tracked in git and the user can review or revert.

**Only ask the user BEFORE writing when the change is *drastic*:**

- **Deleting a file** (prefer archiving — move to ` + "`memory/_archive/`" + ` — over deleting, since archives preserve content).
- **Removing prose or authored artifact content the user themselves wrote**, or large prose blocks (more than a paragraph) from any file.
- **Overwriting a managed file tagged ` + "`priority: load-bearing`" + `** when your new content contradicts it.
- **Restructuring more than a third of the directory in one pass** (mass moves and large folder reshuffles deserve a heads-up).

For everything else — adding new files, archiving stale ones, fixing links, rewriting MEMORY.md, promoting/demoting between sections, adding cross-links — proceed without asking.

When you finish, report:
- What you did (counts plus a one-line summary of each significant change).
- What you skipped and why.
- Anything you flagged for user attention.

If no background-agent capability is available, run inline using the same autonomous policy.

---

`

func harvestText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `harvest` pass on memd memory — bring in the crop.\n\n"+
		"Goal: gather durable knowledge from sources OUTSIDE memd (your other memory systems — Claude's auto-memory, Cursor rules, paste-in notes, another memd directory, prior session context) and integrate it INTO memd.\n\n"+
		"### Phase 1 — Survey\n\n"+
		"1. Call `memory_load()` to see the current state of memd memory.\n"+
		"2. %s\n"+
		"3. **Gather candidates first — do not write yet.** List every external source you can see and pull the durable facts out of each. Examples of sources:\n"+
		"   - Claude Code's `CLAUDE.md` / `AGENTS.md` auto-memory.\n"+
		"   - Cursor's `.cursorrules` or rules pages.\n"+
		"   - Notes the user has pasted into this conversation.\n"+
		"   - Facts inferred from prior session context.\n"+
		"   - Another memd directory the user wants to merge in.\n\n"+
		"### Phase 2 — Structure (only on fresh / sparse directories)\n\n"+
		"If the target directory is empty or has only a stub `MEMORY.md`, this harvest is also setting the directory's shape. memd does not prescribe a single canonical layout — Markdown pages under `memory/` are one option, but standalone HTML mockups, CSV tables, JSON examples, and other text artifacts are also valid memory files. HTML files can carry front matter in a leading comment. For directories whose content naturally splits across categories, multiple top-level folders work better (e.g. `notes/`, `projects/`, `preferences/`, `mockups/`, `data/`, `runbooks/`).\n\n"+
		"4. Cluster your candidates into 3–7 themes. Inspect them — does the content want one general `memory/` bucket, or distinct top-level folders?\n"+
		"5. **Propose the layout to the user before writing:** the top-level folders you'd create, what goes in each, and the section headings the new MEMORY.md will use to group them. Wait for approval. (This is the one phase where harvest pauses — picking the wrong shape early is expensive to fix.)\n\n"+
		"If the directory already has an established shape (more than a couple of files, an existing folder layout), skip Phase 2 and reuse the existing folders. Don't restructure during harvest — that's `reorganise`'s job.\n\n"+
		"### Phase 3 — Integrate\n\n"+
		"6. For each candidate fact, decide **ADD / UPDATE / DELETE / NONE** (search existing files first):\n"+
		"   - ADD → write a new file under the appropriate folder and add a one-line MEMORY.md entry under the right section.\n"+
		"   - UPDATE → edit the existing file in place.\n"+
		"   - DELETE → only when the new info clearly invalidates the old. Archive (move to `_archive/`) instead of deleting wherever possible; add a one-line supersession note.\n"+
		"   - NONE → skip.\n"+
		"7. Do the writes as you go — don't stop for approval ceremonies. Drastic actions (per the preamble) still wait.\n\n"+
		"### Phase 4 — Report\n\n"+
		"8. Report a summary: ADD count, UPDATE count, ARCHIVE/DELETE count, with one-line descriptions of each significant addition. List anything you skipped or flagged. If you created the layout in Phase 2, recap the folder structure.", dirHint(args))
}

func dreamText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `dream` pass on memd memory — sleep consolidation.\n\n"+
		"Goal: for this session, **cement** (load-bearing, recently-used) and **fade** (unused, contradicted, superseded). Use managed files' `memd:` front matter as signal (Markdown front matter or HTML comment front matter); use MEMORY.md links, filenames, and content for unmanaged files.\n\n"+
		"1. Call `memory_load()` to see the current state.\n"+
		"2. %s\n"+
		"3. For every file, `memory_read` it. For Markdown/HTML managed files, inspect the `memd:` block:\n"+
		"   - `last_read_at` — when was this last accessed?\n"+
		"   - `access_count` — how often is it used?\n"+
		"   - `updated_at` — when did its body last change?\n"+
		"4. Act:\n"+
		"   - **Cement** — high `access_count`, recent `last_read_at`, referenced this session, or an important non-Markdown artifact. Pull into MEMORY.md's top sections; add cross-links from related files where the format supports links. Do it.\n"+
		"   - **Fade** — for managed files, `last_read_at` > 90 days, `access_count` 0–1, and not linked from MEMORY.md; for unmanaged files, clearly stale content or no index link. Archive (move under `memory/_archive/`) with a one-line supersession note in MEMORY.md if it still matters historically. Do it.\n"+
		"   - **Resolve contradictions** — if two files disagree and the recent session confirmed one, supersede the other in place.\n"+
		"   - Drastic actions (deleting content the user wrote, removing >1 paragraph, overwriting a `priority: load-bearing` managed file) — ask first per the preamble.\n"+
		"   - **Consolidation is non-destructive** — archive or mark superseded rather than deleting; never destroy content the user might want to review.\n"+
		"5. Report the diff: counts of files cemented / faded / merged; files skipped because the signal was ambiguous.\n\n"+
		"Stats are signal, not gospel. A rarely-read file can still be load-bearing (e.g. a once-a-year procedure), and unmanaged artifacts do not carry `memd:` stats. Use judgement; if a managed file's `priority` field says `load-bearing` or `reference`, treat low access_count as expected and leave it alone.", dirHint(args))
}

func recallText(args map[string]string) string {
	topic := strings.TrimSpace(args["topic"])
	if topic == "" {
		topic = "(no topic supplied — ask the user what they want to recall)"
	}
	return fmt.Sprintf("Run a `recall` pass on memd memory — reminisce on a topic.\n\n"+
		"Topic: **%s**\n\n"+
		"1. Call `memory_load()` if you haven't this session.\n"+
		"2. %s\n"+
		"3. Run `memory_search` for the topic and adjacent terms.\n"+
		"4. `memory_read` each promising hit.\n"+
		"5. Walk links inside readable files — read related files too.\n"+
		"6. Besides direct matches, surface adjacent constraints and preferences linked to the topic — related files and user preferences that touch it — even when not explicitly asked.\n"+
		"7. Synthesise an answer for the user: what memd actually says about the topic, with file links. Cite the files you used.\n\n"+
		"Don't dump raw search hits. Walk the wiki and present what you found.", topic, dirHint(args))
}

func housekeepText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `housekeep` pass on memd memory — daily tidying.\n\n"+
		"Goal: find and fix **structural drift** without restructuring content. Housekeep is the most autonomous of the workflows — almost everything it does is reversible.\n\n"+
		"1. Call `memory_load()` to see the current state.\n"+
		"2. %s\n"+
		"3. Walk every file with `memory_read`. For each issue you find, fix it directly:\n"+
		"   - **Dangling links** — `MEMORY.md` references a missing file. Remove the entry (or add a redirect note if you can guess the new path).\n"+
		"   - **Orphan files** — memory files not linked from `MEMORY.md`. Add a one-line entry in the right section.\n"+
		"   - **Missing agent front matter** — for Markdown pages only, add `topic` / `tags` / `related` where the page's subject makes them obvious.\n"+
		"   - **Stale `last_reorganised`** — flag in the report. Don't bump it yourself; that's `reorganise`'s job.\n"+
		"   - **Empty template sections** — delete the empty heading.\n"+
		"   - **Preload-budget drift** — flag `MEMORY.md` over the preload budget (200 lines / 25KB) and any file over ~100KB; recommend a `reorganise` pass to move detail into topic files. Don't restructure here.\n"+
		"4. Report what you fixed and what you flagged (with reasoning).\n\n"+
		"Housekeep tidies; it doesn't restructure. If the directory needs structural change, recommend `reorganise` at the end of the report.", dirHint(args))
}

// --- Tool implementations ---

// toolWorkflow returns the body of a workflow (reorganise / harvest /
// dream / recall / housekeep) as a tool result. Equivalent to invoking
// the MCP prompt of the matching name. Tool name is "memd_<workflow>".
func (s *Server) toolWorkflow(toolName string, rawArgs json.RawMessage) (string, bool) {
	var a map[string]string
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &a)
	}
	if a == nil {
		a = map[string]string{}
	}
	workflow := strings.TrimPrefix(toolName, "memd_")
	body, _, ok := workflowBody(workflow, a)
	if !ok {
		return "unknown workflow: " + workflow, true
	}
	return body, false
}

func (s *Server) toolStatus(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	if len(dirs) == 0 {
		return "(no directories accessible)"
	}
	var sb strings.Builder
	for _, d := range dirs {
		st := d.Backend.Status()
		fmt.Fprintf(&sb, "%s: backend=%s path=%s last_sync=%s\n",
			d.Directory.Name, st.Backend, st.Path, st.LastSync.Format("2006-01-02 15:04:05"))
		if st.LastError != "" {
			fmt.Fprintf(&sb, "  last_error: %s\n", st.LastError)
		}
	}
	return sb.String()
}

func filterDirsByID(dirs []registry.DirectoryView, id string) []registry.DirectoryView {
	for _, d := range dirs {
		if d.Directory.ID == id {
			return []registry.DirectoryView{d}
		}
	}
	return nil
}
