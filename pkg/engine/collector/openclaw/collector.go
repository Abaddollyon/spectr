package openclaw

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

// Collector implements engine.Collector for the OpenClaw agent framework.
// It discovers JSONL session files at ~/.openclaw/agents/*/sessions/*.jsonl,
// tails them for new content using fsnotify, and feeds parsed events into the
// provided Store. Byte offsets are persisted via Store.SetCollectorState so
// that ingest can resume after a process restart without re-processing history.
type Collector struct{}

// Name returns the framework identifier used in Run.Framework and
// CollectorState.CollectorID prefixes.
func (c *Collector) Name() string {
	return "openclaw"
}

// Discover returns all OpenClaw session JSONL file paths.
// It expands ~/.openclaw/agents/*/sessions/*.jsonl and filters out ancillary
// files such as *.jsonl.lock and *.jsonl.deleted.* that OpenClaw creates
// alongside the primary session files.
func (c *Collector) Discover() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("openclaw discover: home dir: %w", err)
	}

	pattern := filepath.Join(home, ".openclaw", "agents", "*", "sessions", "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("openclaw discover: glob: %w", err)
	}

	var files []string
	for _, m := range matches {
		base := filepath.Base(m)
		// Accept only plain *.jsonl files — skip .jsonl.lock, .jsonl.deleted.*, etc.
		if strings.HasSuffix(base, ".jsonl") && !strings.Contains(base, ".jsonl.") {
			files = append(files, m)
		}
	}
	return files, nil
}

// Start begins ingesting OpenClaw session files into store.
// It processes existing file content from saved byte offsets, then watches
// agent session directories for writes/creates and processes new content as
// it arrives. Blocks until ctx is cancelled.
func (c *Collector) Start(ctx context.Context, store engine.Store) error {
	files, err := c.Discover()
	if err != nil {
		return fmt.Errorf("openclaw start: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("openclaw start: create watcher: %w", err)
	}
	defer watcher.Close() //nolint:errcheck

	// In-memory session states (model, pending tool calls, etc.).
	// Keyed by file path; protected by mu.
	mu := &sync.Mutex{}
	sessions := make(map[string]*sessionState)

	// Process existing file content (resume from saved offsets).
	for _, f := range files {
		if err := c.processFile(ctx, store, f, sessions, mu); err != nil {
			// Log to stderr and continue — one bad file should not block others.
			fmt.Fprintf(os.Stderr, "openclaw: process %s: %v\n", f, err)
		}
	}

	// Watch all agent session directories (including those with no files yet).
	watchedDirs := make(map[string]struct{})
	home, _ := os.UserHomeDir()
	agentGlob := filepath.Join(home, ".openclaw", "agents", "*", "sessions")
	agentDirs, _ := filepath.Glob(agentGlob)
	for _, dir := range agentDirs {
		if _, ok := watchedDirs[dir]; !ok {
			if werr := watcher.Add(dir); werr == nil {
				watchedDirs[dir] = struct{}{}
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
						fmt.Fprintf(os.Stderr, "openclaw: process %s: %v\n", name, err)
					}
				}
			}

		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// Watcher errors are non-fatal; continue.
		}
	}
}

// ─── File processing ──────────────────────────────────────────────────────────

// processFile reads all unprocessed lines from filePath (starting at the
// stored byte offset) and emits events to store.
// mu guards the shared sessions map.
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

	// Retrieve the saved byte offset for this file.
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

	// Get or initialise in-memory session state.
	ss, ok := sessions[filePath]
	if !ok {
		ss = &sessionState{
			runID:        sessionIDFromPath(filePath),
			agentID:      agentIDFromPath(filePath),
			framework:    "openclaw",
			pendingTools: make(map[string]*pendingTool),
		}
		sessions[filePath] = ss
	}

	// Read new lines, tracking byte positions accurately.
	reader := bufio.NewReaderSize(f, 2*1024*1024) // 2 MB read buffer
	pos := offset
	lastSuccessPos := offset

	for {
		lineBytes, readErr := reader.ReadBytes('\n')
		lineLen := int64(len(lineBytes))

		if lineLen > 0 {
			// Only process lines that are newline-terminated.
			// A non-terminated last chunk means the writer hasn't flushed yet.
			if lineBytes[len(lineBytes)-1] == '\n' {
				trimmed := bytes.TrimRight(lineBytes, "\r\n")
				if len(trimmed) > 0 {
					if perr := c.processLine(ctx, store, filePath, trimmed, ss); perr == nil {
						pos += lineLen
						lastSuccessPos = pos
					} else {
						// Skip malformed lines but still advance past them
						// so we don't loop forever on persistent garbage.
						pos += lineLen
						lastSuccessPos = pos
					}
				} else {
					// Blank line.
					pos += lineLen
					lastSuccessPos = pos
				}
			} else {
				// Partial (unterminated) line at EOF — do NOT advance offset;
				// we will re-read it once more data is written.
				pos += lineLen
				// lastSuccessPos stays behind the partial line.
			}
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
	}

	// Persist the updated offset only if we made progress.
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
	case "session":
		return c.handleSession(ctx, store, entry, ss)
	case "model_change":
		ss.model = entry.ModelID
		return nil
	case "message":
		return c.handleMessage(ctx, store, entry, ss)
	default:
		// thinking_level_change, custom, etc. — not currently tracked.
		return nil
	}
}

// handleSession creates (or idempotently upserts) the Run for this session.
func (c *Collector) handleSession(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	ss *sessionState,
) error {
	ss.startedAt = entry.Timestamp

	// The session line's "id" field is the canonical UUID (OpenClaw guarantees
	// it matches the filename in production; test fixtures may differ).
	if entry.LineID != "" {
		ss.runID = entry.LineID
	}

	run := &engine.Run{
		ID:         ss.runID,
		Framework:  ss.framework,
		AgentID:    ss.agentID,
		SessionKey: ss.runID,
		Status:     engine.RunStatusRunning,
		StartedAt:  entry.Timestamp,
		Metadata:   map[string]string{"cwd": entry.CWD},
	}

	if err := store.InsertRun(ctx, run); err != nil {
		// Run already exists from a previous ingest pass — update metadata.
		_ = store.UpdateRun(ctx, run) //nolint:errcheck
	}
	ss.runCreated = true
	return nil
}

// handleMessage routes message lines by role.
func (c *Collector) handleMessage(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	ss *sessionState,
) error {
	// Guard: ensure the Run exists even if we joined mid-session.
	if !ss.runCreated {
		run := &engine.Run{
			ID:         ss.runID,
			Framework:  ss.framework,
			AgentID:    ss.agentID,
			SessionKey: ss.runID,
			Status:     engine.RunStatusRunning,
			StartedAt:  entry.Timestamp,
		}
		if err := store.InsertRun(ctx, run); err != nil {
			_ = store.UpdateRun(ctx, run) //nolint:errcheck
		}
		ss.runCreated = true
	}

	msg := entry.RawMessage
	if msg == nil {
		return nil
	}

	eventID := ss.runID + ":" + entry.LineID
	ss.lastEventID = eventID

	switch msg.Role {
	case "user":
		return c.handleUserMessage(ctx, store, entry, eventID, ss)
	case "assistant":
		return c.handleAssistantMessage(ctx, store, entry, eventID, ss)
	case "toolResult":
		return c.handleToolResult(ctx, store, entry, eventID, ss)
	default:
		return nil
	}
}

func (c *Collector) handleUserMessage(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	eventID string,
	ss *sessionState,
) error {
	data, _ := json.Marshal(map[string]string{"role": "user"})
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

func (c *Collector) handleAssistantMessage(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	eventID string,
	ss *sessionState,
) error {
	msg := entry.RawMessage

	// Extract text content for the parent event.
	var textContent string
	for _, c := range msg.Content {
		if c.Type == "text" {
			textContent = c.Text
			break
		}
	}
	data, _ := json.Marshal(map[string]string{"role": "assistant", "text": textContent})

	parentEv := &engine.Event{
		ID:        eventID,
		RunID:     ss.runID,
		Type:      engine.EventTypeMessage,
		Timestamp: entry.Timestamp,
		Model:     ss.model,
		Data:      data,
	}
	if err := store.InsertEvent(ctx, parentEv); err != nil {
		return fmt.Errorf("insert assistant event %s: %w", eventID, err)
	}

	// Create a child event + buffer a ToolCall for each toolCall content item.
	for _, content := range msg.Content {
		if content.Type != "toolCall" {
			continue
		}

		tcEventID := ss.runID + ":tc:" + content.ID
		tcData, _ := json.Marshal(map[string]interface{}{
			"tool":      content.Name,
			"arguments": json.RawMessage(content.Arguments),
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

		// Buffer the ToolCall record; we can only finalise it once the result arrives.
		ss.pendingTools[content.ID] = &pendingTool{
			toolCall: &engine.ToolCall{
				ID:        tcEventID,
				EventID:   tcEventID,
				RunID:     ss.runID,
				Tool:      content.Name,
				ArgsHash:  hashArgs(content.Arguments),
				Timestamp: entry.Timestamp,
			},
			timestamp: entry.Timestamp,
		}
	}

	return nil
}

func (c *Collector) handleToolResult(
	ctx context.Context,
	store engine.Store,
	entry *ParsedEntry,
	eventID string,
	ss *sessionState,
) error {
	msg := entry.RawMessage

	// result event ID uses "tr" prefix to avoid collision with assistant event IDs.
	resultEventID := ss.runID + ":tr:" + entry.LineID
	data, _ := json.Marshal(map[string]interface{}{
		"tool":     msg.ToolName,
		"is_error": msg.IsError,
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

	// Complete and insert the buffered ToolCall.
	if pending, ok := ss.pendingTools[msg.ToolCallID]; ok {
		pending.toolCall.Success = !msg.IsError
		pending.toolCall.DurationMs = entry.Timestamp.Sub(pending.timestamp).Milliseconds()
		if err := store.InsertToolCall(ctx, pending.toolCall); err != nil {
			return fmt.Errorf("insert tool call %s: %w", pending.toolCall.ID, err)
		}
		delete(ss.pendingTools, msg.ToolCallID)
	}

	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// stateKey returns the CollectorID used in CollectorState for a given file path.
// Using a per-file key allows storing independent offsets for each session file.
func stateKey(filePath string) string {
	return "openclaw:" + filePath
}

// agentIDFromPath extracts the agent directory name from a session file path.
// Expected form: .../.openclaw/agents/<agentID>/sessions/<uuid>.jsonl
func agentIDFromPath(filePath string) string {
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	for i, part := range parts {
		if part == "agents" && i+2 < len(parts) {
			return parts[i+1]
		}
	}
	return "unknown"
}

// sessionIDFromPath returns the session UUID embedded in the file's base name.
func sessionIDFromPath(filePath string) string {
	base := filepath.Base(filePath)
	return strings.TrimSuffix(base, ".jsonl")
}

// hashArgs returns the first 8 bytes of SHA-256(args) as a hex string,
// used for fingerprinting identical tool invocations.
func hashArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	sum := sha256.Sum256(args)
	return fmt.Sprintf("%x", sum[:8])
}

// ─── Session state ────────────────────────────────────────────────────────────

// sessionState holds per-session in-memory ingestion context.
// It is NOT persisted; only the byte offset survives restarts.
type sessionState struct {
	runID        string
	agentID      string
	framework    string
	model        string
	startedAt    time.Time
	runCreated   bool
	lastEventID  string
	pendingTools map[string]*pendingTool // toolCallID → buffered record
}

// pendingTool holds an unconfirmed tool call awaiting its result message.
type pendingTool struct {
	toolCall  *engine.ToolCall
	timestamp time.Time
}
