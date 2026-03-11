package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// Collector implements engine.Collector for the Claude Code agent framework.
// It discovers JSONL session files at ~/.claude/projects/**/*.jsonl,
// tails them for new content using fsnotify, and feeds parsed events into the
// provided Store. Byte offsets are persisted via Store.SetCollectorState so
// that ingest can resume after a process restart without re-processing history.
type Collector struct{}

// Name returns the framework identifier.
func (c *Collector) Name() string {
	return "claudecode"
}

// Discover returns all Claude Code session JSONL file paths.
// It expands ~/.claude/projects/**/*.jsonl, skipping the history file
// and any lock/temp files.
func (c *Collector) Discover() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("claudecode discover: home dir: %w", err)
	}

	projectsDir := filepath.Join(home, ".claude", "projects")

	var files []string
	err = filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Directory may not exist — not an error for us.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		base := d.Name()
		// Only plain *.jsonl files — skip .jsonl.lock, history.jsonl, etc.
		if strings.HasSuffix(base, ".jsonl") && !strings.Contains(base, ".jsonl.") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("claudecode discover: walk: %w", err)
	}
	return files, nil
}

// Start begins ingesting Claude Code session files into store.
// It processes existing file content from saved byte offsets, then watches
// project directories for writes/creates and processes new content as it arrives.
// Blocks until ctx is cancelled.
func (c *Collector) Start(ctx context.Context, store engine.Store) error {
	files, err := c.Discover()
	if err != nil {
		return fmt.Errorf("claudecode start: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("claudecode start: create watcher: %w", err)
	}
	defer watcher.Close() //nolint:errcheck

	mu := &sync.Mutex{}
	sessions := make(map[string]*sessionState)

	// Process existing file content (resume from saved offsets).
	for _, f := range files {
		if err := c.processFile(ctx, store, f, sessions, mu); err != nil {
			fmt.Fprintf(os.Stderr, "claudecode: process %s: %v\n", f, err)
		}
	}

	// Watch all project directories (including ones with no files yet).
	watchedDirs := make(map[string]struct{})
	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude", "projects")
	entries, _ := os.ReadDir(projectsDir)
	for _, e := range entries {
		if e.IsDir() {
			dir := filepath.Join(projectsDir, e.Name())
			if _, ok := watchedDirs[dir]; !ok {
				if werr := watcher.Add(dir); werr == nil {
					watchedDirs[dir] = struct{}{}
				}
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				name := event.Name
				base := filepath.Base(name)
				if strings.HasSuffix(base, ".jsonl") && !strings.Contains(base, ".jsonl.") {
					if err := c.processFile(ctx, store, name, sessions, mu); err != nil {
						fmt.Fprintf(os.Stderr, "claudecode: process %s: %v\n", name, err)
					}
				}
			}

		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// ─── File processing ──────────────────────────────────────────────────────────

func (c *Collector) processFile(
	ctx context.Context,
	store engine.Store,
	filePath string,
	sessions map[string]*sessionState,
	mu *sync.Mutex,
) error {
	mu.Lock()
	defer mu.Unlock()

	collectorID := stateKey(filePath)

	var offset int64
	if state, err := store.GetCollectorState(ctx, collectorID); err == nil && state != nil {
		offset = state.ByteOffset
	}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close() //nolint:errcheck

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek to %d: %w", offset, err)
		}
	}

	ss, ok := sessions[filePath]
	if !ok {
		ss = &sessionState{
			runID:        sessionIDFromPath(filePath),
			framework:    "claudecode",
			pendingTools: make(map[string]*pendingTool),
		}
		sessions[filePath] = ss
	}

	reader := bufio.NewReaderSize(f, 2*1024*1024)
	pos := offset
	lastSuccessPos := offset

	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		lineLen := int64(len(lineBytes))

		if lineLen > 0 {
			if lineBytes[len(lineBytes)-1] == '\n' {
				trimmed := bytes.TrimRight(lineBytes, "\r\n")
				if len(trimmed) > 0 {
					if perr := c.processLine(ctx, store, filePath, trimmed, ss); perr == nil {
						pos += lineLen
						lastSuccessPos = pos
					} else {
						pos += lineLen
						lastSuccessPos = pos
					}
				} else {
					pos += lineLen
					lastSuccessPos = pos
				}
			} else {
				// Partial (unterminated) line — wait for more data.
				pos += lineLen
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
	}

	if lastSuccessPos > offset {
		if serr := store.SetCollectorState(ctx, &engine.CollectorState{
			CollectorID: collectorID,
			FilePath:    filePath,
			ByteOffset:  lastSuccessPos,
			LastEventID: ss.lastEventID,
		}); serr != nil {
			return fmt.Errorf("save state: %w", serr)
		}
	}

	return nil
}

// ─── Line dispatching ─────────────────────────────────────────────────────────

func (c *Collector) processLine(
	ctx context.Context,
	store engine.Store,
	filePath string,
	data []byte,
	ss *sessionState,
) error {
	entry, err := parseLine(data)
	if err != nil {
		return err
	}

	switch entry.Type {
	case "user":
		return c.handleUserOrAssistant(ctx, store, entry, ss)
	case "assistant":
		return c.handleUserOrAssistant(ctx, store, entry, ss)
	default:
		// file-history-snapshot and other housekeeping lines — skip.
		return nil
	}
}

// ensureRun creates (or upserts) the Run for this session.
// Claude Code has no explicit session-open line; we infer the Run from the first
// user or assistant line we encounter.
func (c *Collector) ensureRun(ctx context.Context, store engine.Store, entry *ParsedEntry, ss *sessionState) error {
	if ss.runCreated {
		return nil
	}

	// Prefer the sessionId from the line; fall back to the filename-derived ID.
	if entry.SessionID != "" {
		ss.runID = entry.SessionID
	}

	run := &engine.Run{
		ID:         ss.runID,
		Framework:  ss.framework,
		AgentID:    "claudecode",
		SessionKey: ss.runID,
		Status:     engine.RunStatusRunning,
		StartedAt:  entry.Timestamp,
		Metadata:   map[string]string{"cwd": entry.CWD, "version": entry.Version},
	}
	if err := store.InsertRun(ctx, run); err != nil {
		_ = store.UpdateRun(ctx, run)
	}
	ss.runCreated = true
	ss.startedAt = entry.Timestamp
	return nil
}

func (c *Collector) handleUserOrAssistant(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	ss *sessionState,
) error {
	if err := c.ensureRun(ctx, store, entry, ss); err != nil {
		return err
	}

	// Update model if this is an assistant message.
	if entry.Role == "assistant" && entry.Model != "" {
		ss.model = entry.Model
	}

	eventID := ss.runID + ":" + entry.UUID
	ss.lastEventID = eventID

	switch entry.Role {
	case "user":
		return c.handleUser(ctx, store, entry, eventID, ss)
	case "assistant":
		return c.handleAssistant(ctx, store, entry, eventID, ss)
	default:
		return nil
	}
}

func (c *Collector) handleUser(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	eventID string,
	ss *sessionState,
) error {
	// If this user message contains tool results, resolve pending tool calls.
	for _, tr := range entry.ToolResults {
		resultEventID := ss.runID + ":tr:" + entry.UUID + ":" + tr.ToolUseID
		data, _ := json.Marshal(map[string]interface{}{
			"tool_use_id": tr.ToolUseID,
			"is_error":    tr.IsError,
		})
		trEv := &engine.Event{
			ID:        resultEventID,
			RunID:     ss.runID,
			Type:      engine.EventTypeToolResult,
			Timestamp: entry.Timestamp,
			Model:     ss.model,
			Data:      data,
		}
		if err := store.InsertEvent(ctx, trEv); err != nil {
			return fmt.Errorf("insert tool-result event %s: %w", resultEventID, err)
		}

		if pending, ok := ss.pendingTools[tr.ToolUseID]; ok {
			pending.toolCall.Success = !tr.IsError
			pending.toolCall.DurationMs = entry.Timestamp.Sub(pending.timestamp).Milliseconds()
			if err := store.InsertToolCall(ctx, pending.toolCall); err != nil {
				return fmt.Errorf("insert tool call %s: %w", pending.toolCall.ID, err)
			}
			delete(ss.pendingTools, tr.ToolUseID)
		}
	}

	// Don't emit a message event for pure tool-result messages.
	if len(entry.ToolResults) > 0 && entry.TextContent == "" {
		return nil
	}

	data, _ := json.Marshal(map[string]string{"role": "user", "text": entry.TextContent})
	ev := &engine.Event{
		ID:        eventID,
		RunID:     ss.runID,
		Type:      engine.EventTypeMessage,
		Timestamp: entry.Timestamp,
		Model:     ss.model,
		Data:      data,
	}
	return store.InsertEvent(ctx, ev)
}

func (c *Collector) handleAssistant(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	eventID string,
	ss *sessionState,
) error {
	// Claude Code streams assistant messages as multiple partial lines sharing
	// the same UUID. Only the last one has stop_reason set with full usage.
	// We upsert (try insert, then update) to handle the streaming pattern.
	var tokensIn, tokensOut int64
	if entry.Usage != nil {
		tokensIn = entry.Usage.TotalIn()
		tokensOut = entry.Usage.OutputTokens
		// Update cumulative run counters.
		ss.totalTokensIn += tokensIn
		ss.totalTokensOut += tokensOut
	}

	data, _ := json.Marshal(map[string]string{"role": "assistant", "text": entry.TextContent})
	parentEv := &engine.Event{
		ID:        eventID,
		RunID:     ss.runID,
		Type:      engine.EventTypeMessage,
		Timestamp: entry.Timestamp,
		Model:     ss.model,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		Data:      data,
	}
	if err := store.InsertEvent(ctx, parentEv); err != nil {
		// Streaming: duplicate — update instead.
		_ = store.InsertEvent(ctx, parentEv)
	}

	// Emit tool call events and buffer pending tool calls.
	for _, tu := range entry.ToolUses {
		tcEventID := ss.runID + ":tc:" + tu.ID
		tcData, _ := json.Marshal(map[string]interface{}{
			"tool":      tu.Name,
			"arguments": json.RawMessage(tu.Input),
		})
		tcEv := &engine.Event{
			ID:            tcEventID,
			RunID:         ss.runID,
			ParentEventID: &eventID,
			Type:          engine.EventTypeToolCall,
			Timestamp:     entry.Timestamp,
			Model:         ss.model,
			Data:          tcData,
		}
		if err := store.InsertEvent(ctx, tcEv); err != nil {
			return fmt.Errorf("insert tool-call event %s: %w", tcEventID, err)
		}

		ss.pendingTools[tu.ID] = &pendingTool{
			toolCall: &engine.ToolCall{
				ID:        tcEventID,
				EventID:   tcEventID,
				RunID:     ss.runID,
				Tool:      tu.Name,
				ArgsHash:  hashArgs(tu.Input),
				Timestamp: entry.Timestamp,
			},
			timestamp: entry.Timestamp,
		}
	}

	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func stateKey(filePath string) string {
	return "claudecode:" + filePath
}

// sessionIDFromPath extracts the session UUID from the file's base name.
// Expected form: ~/.claude/projects/<project>/<uuid>.jsonl
func sessionIDFromPath(filePath string) string {
	base := filepath.Base(filePath)
	return strings.TrimSuffix(base, ".jsonl")
}

func hashArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	sum := sha256.Sum256(args)
	return fmt.Sprintf("%x", sum[:8])
}

// ─── Session state ────────────────────────────────────────────────────────────

type sessionState struct {
	runID          string
	framework      string
	model          string
	startedAt      time.Time
	runCreated     bool
	lastEventID    string
	totalTokensIn  int64
	totalTokensOut int64
	pendingTools   map[string]*pendingTool
}

type pendingTool struct {
	toolCall  *engine.ToolCall
	timestamp time.Time
}
