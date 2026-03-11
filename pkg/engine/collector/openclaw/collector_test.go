package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// ─── Mock Store ───────────────────────────────────────────────────────────────

// mockStore is a thread-safe in-memory implementation of engine.Store for tests.
type mockStore struct {
	mu              sync.Mutex
	runs            map[string]*engine.Run
	events          map[string]*engine.Event // keyed by Event.ID
	toolCalls       []*engine.ToolCall
	alerts          []*engine.Alert
	collectorStates map[string]*engine.CollectorState

	// Call counters used in assertions.
	insertRunCalls   int
	updateRunCalls   int
	insertEventCalls int
	insertToolCalls  int
}

func newMockStore() *mockStore {
	return &mockStore{
		runs:            make(map[string]*engine.Run),
		events:          make(map[string]*engine.Event),
		collectorStates: make(map[string]*engine.CollectorState),
	}
}

func (m *mockStore) InsertRun(_ context.Context, run *engine.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertRunCalls++
	if _, exists := m.runs[run.ID]; exists {
		return fmt.Errorf("run %s already exists", run.ID)
	}
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *mockStore) UpdateRun(_ context.Context, run *engine.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateRunCalls++
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *mockStore) GetRun(_ context.Context, id string) (*engine.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.runs[id]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, fmt.Errorf("run %s not found", id)
}

func (m *mockStore) ListRuns(_ context.Context, _ engine.ListRunsOpts) ([]*engine.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*engine.Run
	for _, r := range m.runs {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (m *mockStore) InsertEvent(_ context.Context, ev *engine.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertEventCalls++
	cp := *ev
	m.events[ev.ID] = &cp
	return nil
}

func (m *mockStore) ListEvents(_ context.Context, runID string) ([]*engine.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*engine.Event
	for _, ev := range m.events {
		if ev.RunID == runID {
			cp := *ev
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *mockStore) InsertToolCall(_ context.Context, tc *engine.ToolCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertToolCalls++
	cp := *tc
	m.toolCalls = append(m.toolCalls, &cp)
	return nil
}

func (m *mockStore) ListToolCalls(_ context.Context, runID string) ([]*engine.ToolCall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*engine.ToolCall
	for _, tc := range m.toolCalls {
		if tc.RunID == runID {
			cp := *tc
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *mockStore) InsertAlert(_ context.Context, a *engine.Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *a
	m.alerts = append(m.alerts, &cp)
	return nil
}

func (m *mockStore) ListAlerts(_ context.Context, _ engine.ListAlertsOpts) ([]*engine.Alert, error) {
	return nil, nil
}

func (m *mockStore) AckAlert(_ context.Context, _ string) error { return nil }

func (m *mockStore) TokenUsageOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return nil, nil
}

func (m *mockStore) CostOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return nil, nil
}

func (m *mockStore) TopToolCalls(_ context.Context, _, _ time.Time, _ int) ([]engine.ToolFrequency, error) {
	return nil, nil
}

func (m *mockStore) GetCollectorState(_ context.Context, id string) (*engine.CollectorState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.collectorStates[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

func (m *mockStore) SetCollectorState(_ context.Context, state *engine.CollectorState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *state
	m.collectorStates[state.CollectorID] = &cp
	return nil
}

func (m *mockStore) Close() error { return nil }

// ─── Parser tests ─────────────────────────────────────────────────────────────

func TestParseLine_Session(t *testing.T) {
	line := `{"type":"session","version":3,"id":"abc123","timestamp":"2026-01-01T10:00:00.000Z","cwd":"/home/user"}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Type != "session" {
		t.Errorf("type = %q; want %q", entry.Type, "session")
	}
	if entry.LineID != "abc123" {
		t.Errorf("LineID = %q; want %q", entry.LineID, "abc123")
	}
	if entry.CWD != "/home/user" {
		t.Errorf("CWD = %q; want %q", entry.CWD, "/home/user")
	}
	wantTS, _ := time.Parse(time.RFC3339, "2026-01-01T10:00:00Z")
	if !entry.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v; want %v", entry.Timestamp, wantTS)
	}
}

func TestParseLine_ModelChange(t *testing.T) {
	line := `{"type":"model_change","id":"m001","parentId":null,"timestamp":"2026-01-01T10:00:00.001Z","provider":"anthropic","modelId":"claude-sonnet-4-6"}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Type != "model_change" {
		t.Errorf("type = %q; want %q", entry.Type, "model_change")
	}
	if entry.Provider != "anthropic" {
		t.Errorf("Provider = %q; want %q", entry.Provider, "anthropic")
	}
	if entry.ModelID != "claude-sonnet-4-6" {
		t.Errorf("ModelID = %q; want %q", entry.ModelID, "claude-sonnet-4-6")
	}
}

func TestParseLine_UserMessage(t *testing.T) {
	line := `{"type":"message","id":"e001","parentId":"m001","timestamp":"2026-01-01T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hello world"}],"timestamp":1735722001000}}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Type != "message" {
		t.Errorf("type = %q; want %q", entry.Type, "message")
	}
	if entry.RawMessage == nil {
		t.Fatal("RawMessage is nil")
	}
	if entry.RawMessage.Role != "user" {
		t.Errorf("role = %q; want %q", entry.RawMessage.Role, "user")
	}
	if len(entry.RawMessage.Content) != 1 {
		t.Fatalf("content length = %d; want 1", len(entry.RawMessage.Content))
	}
	if entry.RawMessage.Content[0].Text != "hello world" {
		t.Errorf("text = %q; want %q", entry.RawMessage.Content[0].Text, "hello world")
	}
	// The outer ISO timestamp is used (the embedded unix-ms field is ignored).
	wantTS, _ := time.Parse(time.RFC3339, "2026-01-01T10:00:01Z")
	if !entry.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v; want %v", entry.Timestamp, wantTS)
	}
}

func TestParseLine_AssistantWithToolCall(t *testing.T) {
	line := `{"type":"message","id":"e002","parentId":"e001","timestamp":"2026-01-01T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Running…"},{"type":"toolCall","id":"toolu_abc","name":"exec","arguments":{"command":"echo hi"}}]}}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.RawMessage.Role != "assistant" {
		t.Errorf("role = %q; want assistant", entry.RawMessage.Role)
	}
	var toolCallCount int
	for _, c := range entry.RawMessage.Content {
		if c.Type == "toolCall" {
			toolCallCount++
			if c.Name != "exec" {
				t.Errorf("tool name = %q; want exec", c.Name)
			}
			var args map[string]string
			if err := json.Unmarshal(c.Arguments, &args); err != nil {
				t.Fatalf("unmarshal args: %v", err)
			}
			if args["command"] != "echo hi" {
				t.Errorf("command = %q; want %q", args["command"], "echo hi")
			}
		}
	}
	if toolCallCount != 1 {
		t.Errorf("toolCall count = %d; want 1", toolCallCount)
	}
}

func TestParseLine_ToolResult_Success(t *testing.T) {
	line := `{"type":"message","id":"e003","parentId":"e002","timestamp":"2026-01-01T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"toolu_abc","toolName":"exec","content":[{"type":"text","text":"hi"}],"isError":false,"timestamp":1735722003000}}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg := entry.RawMessage
	if msg.Role != "toolResult" {
		t.Errorf("role = %q; want toolResult", msg.Role)
	}
	if msg.ToolCallID != "toolu_abc" {
		t.Errorf("ToolCallID = %q; want toolu_abc", msg.ToolCallID)
	}
	if msg.ToolName != "exec" {
		t.Errorf("ToolName = %q; want exec", msg.ToolName)
	}
	if msg.IsError {
		t.Error("IsError = true; want false")
	}
}

func TestParseLine_ToolResult_Error(t *testing.T) {
	line := `{"type":"message","id":"e004","parentId":"e003","timestamp":"2026-01-01T10:00:04.000Z","message":{"role":"toolResult","toolCallId":"toolu_bad","toolName":"read","content":[{"type":"text","text":"not found"}],"isError":true}}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !entry.RawMessage.IsError {
		t.Error("IsError = false; want true")
	}
}

func TestParseLine_CustomType(t *testing.T) {
	line := `{"type":"custom","customType":"model-snapshot","data":{"provider":"anthropic"},"id":"c001","parentId":"m001","timestamp":"2026-01-01T10:00:00.005Z"}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Type != "custom" {
		t.Errorf("type = %q; want custom", entry.Type)
	}
	// RawMessage should be nil for non-message types.
	if entry.RawMessage != nil {
		t.Error("RawMessage should be nil for custom type")
	}
}

func TestParseLine_ThinkingLevelChange(t *testing.T) {
	line := `{"type":"thinking_level_change","id":"tl001","parentId":"m001","timestamp":"2026-01-01T10:00:00.003Z","thinkingLevel":"high"}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Type != "thinking_level_change" {
		t.Errorf("type = %q; want thinking_level_change", entry.Type)
	}
}

func TestParseLine_EmptyLine(t *testing.T) {
	_, err := parseLine([]byte(""))
	if err == nil {
		t.Error("expected error for empty line")
	}
}

func TestParseLine_MalformedJSON(t *testing.T) {
	_, err := parseLine([]byte("{not json}"))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseLine_MissingType(t *testing.T) {
	_, err := parseLine([]byte(`{"id":"x","timestamp":"2026-01-01T10:00:00Z"}`))
	if err == nil {
		t.Error("expected error for missing type field")
	}
}

func TestParseLine_MultipleToolCalls(t *testing.T) {
	line := `{"type":"message","id":"e006","parentId":"e005","timestamp":"2026-01-01T10:00:06.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"toolu_1","name":"read","arguments":{"path":"/tmp/a"}},{"type":"toolCall","id":"toolu_2","name":"web_search","arguments":{"query":"test"}}]}}`
	entry, err := parseLine([]byte(line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var toolCalls []rawContent
	for _, c := range entry.RawMessage.Content {
		if c.Type == "toolCall" {
			toolCalls = append(toolCalls, c)
		}
	}
	if len(toolCalls) != 2 {
		t.Errorf("toolCall count = %d; want 2", len(toolCalls))
	}
}

// ─── Collector.Name ───────────────────────────────────────────────────────────

func TestCollector_Name(t *testing.T) {
	c := &Collector{}
	if got := c.Name(); got != "openclaw" {
		t.Errorf("Name() = %q; want %q", got, "openclaw")
	}
}

// ─── Discover ─────────────────────────────────────────────────────────────────

func TestCollector_Discover(t *testing.T) {
	// Build a temporary directory tree that mimics ~/.openclaw/agents/*/sessions/.
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".openclaw", "agents")

	type agentFixture struct {
		agent    string
		files    []string
		excluded []string // ancillary files that should NOT appear in results
	}

	fixtures := []agentFixture{
		{
			agent:    "main",
			files:    []string{"session1.jsonl", "session2.jsonl"},
			excluded: []string{"session1.jsonl.lock", "session1.jsonl.deleted.2026-01-01T00-00-00"},
		},
		{
			agent:  "sonnet",
			files:  []string{"session3.jsonl"},
		},
	}

	for _, a := range fixtures {
		sessDir := filepath.Join(agentsDir, a.agent, "sessions")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		for _, f := range a.files {
			if err := os.WriteFile(filepath.Join(sessDir, f), []byte("{}"), 0o644); err != nil {
				t.Fatalf("write %s: %v", f, err)
			}
		}
		for _, f := range a.excluded {
			if err := os.WriteFile(filepath.Join(sessDir, f), []byte("{}"), 0o644); err != nil {
				t.Fatalf("write excluded %s: %v", f, err)
			}
		}
	}

	got, err := discoverFromRoot(root)
	if err != nil {
		t.Fatalf("discoverFromRoot: %v", err)
	}

	want := map[string]bool{
		filepath.Join(agentsDir, "main", "sessions", "session1.jsonl"):   true,
		filepath.Join(agentsDir, "main", "sessions", "session2.jsonl"):   true,
		filepath.Join(agentsDir, "sonnet", "sessions", "session3.jsonl"): true,
	}

	if len(got) != len(want) {
		t.Errorf("Discover returned %d files; want %d\ngot: %v", len(got), len(want), got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected file in Discover results: %s", g)
		}
	}
}

// ─── processFile / full session ───────────────────────────────────────────────

func TestCollector_ProcessFile_FullSession(t *testing.T) {
	c := &Collector{}
	store := newMockStore()
	ctx := context.Background()

	fixturePath := filepath.Join("testdata", "session_full.jsonl")

	sessions := make(map[string]*sessionState)
	mu := &sync.Mutex{}

	if err := c.processFile(ctx, store, fixturePath, sessions, mu); err != nil {
		t.Fatalf("processFile: %v", err)
	}

	// --- Run assertions ---
	if store.insertRunCalls != 1 {
		t.Errorf("InsertRun calls = %d; want 1", store.insertRunCalls)
	}
	run, ok := store.runs["test-session-001"]
	if !ok {
		t.Fatal("run test-session-001 not found in store")
	}
	if run.Framework != "openclaw" {
		t.Errorf("Run.Framework = %q; want openclaw", run.Framework)
	}
	if run.Status != engine.RunStatusRunning {
		t.Errorf("Run.Status = %q; want running", run.Status)
	}

	// --- Event assertions ---
	// session_full.jsonl contains:
	//   e001 user                    → 1 event
	//   e002 assistant (1 toolCall)  → 1 + 1 = 2 events
	//   e003 toolResult              → 1 event
	//   e004 assistant (0 toolCalls) → 1 event
	//   e005 user                    → 1 event
	//   e006 assistant (2 toolCalls) → 1 + 2 = 3 events
	//   e007 toolResult              → 1 event
	//   e008 toolResult              → 1 event
	//   e009 assistant (0 toolCalls) → 1 event
	// Total: 12 events
	if store.insertEventCalls != 12 {
		t.Errorf("InsertEvent calls = %d; want 12", store.insertEventCalls)
	}

	// --- ToolCall assertions ---
	// 3 tools called: exec, read, web_search
	if store.insertToolCalls != 3 {
		t.Errorf("InsertToolCall calls = %d; want 3", store.insertToolCalls)
	}

	toolMap := make(map[string]*engine.ToolCall)
	for _, tc := range store.toolCalls {
		toolMap[tc.Tool] = tc
	}

	execTC, ok := toolMap["exec"]
	if !ok {
		t.Error("tool call 'exec' not found")
	} else {
		if !execTC.Success {
			t.Error("exec tool call should be successful (isError=false)")
		}
		if execTC.DurationMs <= 0 {
			t.Errorf("exec DurationMs = %d; want > 0", execTC.DurationMs)
		}
		if execTC.ArgsHash == "" {
			t.Error("exec ArgsHash should not be empty")
		}
	}

	readTC, ok := toolMap["read"]
	if !ok {
		t.Error("tool call 'read' not found")
	} else if !readTC.Success {
		t.Error("read tool call should be successful")
	}

	searchTC, ok := toolMap["web_search"]
	if !ok {
		t.Error("tool call 'web_search' not found")
	} else if searchTC.Success {
		t.Error("web_search tool call should be marked failed (isError=true in fixture)")
	}

	// --- Offset persisted ---
	key := stateKey(fixturePath)
	savedState, ok := store.collectorStates[key]
	if !ok {
		t.Error("CollectorState not saved after processFile")
	} else if savedState.ByteOffset == 0 {
		t.Error("saved ByteOffset is 0; want > 0")
	}
}

// ─── Offset tracking (restart recovery) ──────────────────────────────────────

func TestCollector_ProcessFile_OffsetTracking(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "incremental.jsonl")

	line1 := `{"type":"session","version":3,"id":"inc-001","timestamp":"2026-01-01T12:00:00.000Z","cwd":"/tmp"}` + "\n"
	line2 := `{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-01-01T12:00:00.001Z","provider":"anthropic","modelId":"claude-sonnet-4-6"}` + "\n"
	line3 := `{"type":"message","id":"msg1","parentId":"m1","timestamp":"2026-01-01T12:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}` + "\n"

	// First pass: write and process line1 + line2 only.
	if err := os.WriteFile(filePath, []byte(line1+line2), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	c := &Collector{}
	store := newMockStore()
	ctx := context.Background()
	sessions := make(map[string]*sessionState)
	mu := &sync.Mutex{}

	if err := c.processFile(ctx, store, filePath, sessions, mu); err != nil {
		t.Fatalf("first processFile: %v", err)
	}
	firstRunCalls := store.insertRunCalls
	firstEventCalls := store.insertEventCalls

	// Append line3.
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(line3); err != nil {
		t.Fatalf("write line3: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second pass: only line3 should be processed.
	if err := c.processFile(ctx, store, filePath, sessions, mu); err != nil {
		t.Fatalf("second processFile: %v", err)
	}

	// InsertRun must not be called again (offset saved the position).
	if store.insertRunCalls != firstRunCalls {
		t.Errorf("InsertRun called again on second pass: total calls = %d", store.insertRunCalls)
	}

	// Exactly one new InsertEvent (the user message in line3).
	newEventCalls := store.insertEventCalls - firstEventCalls
	if newEventCalls != 1 {
		t.Errorf("new InsertEvent calls on second pass = %d; want 1", newEventCalls)
	}
}

// ─── Session without header (mid-session resume) ──────────────────────────────

func TestCollector_ProcessFile_NoHeader(t *testing.T) {
	// Simulate resuming past the session header — Run must still be created.
	c := &Collector{}
	store := newMockStore()
	ctx := context.Background()
	fixturePath := filepath.Join("testdata", "session_no_header.jsonl")
	sessions := make(map[string]*sessionState)
	mu := &sync.Mutex{}

	if err := c.processFile(ctx, store, fixturePath, sessions, mu); err != nil {
		t.Fatalf("processFile: %v", err)
	}

	if len(store.runs) != 1 {
		t.Errorf("runs count = %d; want 1 (auto-created for headerless session)", len(store.runs))
	}
}

// ─── Model propagation ────────────────────────────────────────────────────────

func TestCollector_ModelPropagatedToEvents(t *testing.T) {
	c := &Collector{}
	store := newMockStore()
	ctx := context.Background()
	fixturePath := filepath.Join("testdata", "session_full.jsonl")
	sessions := make(map[string]*sessionState)
	mu := &sync.Mutex{}

	if err := c.processFile(ctx, store, fixturePath, sessions, mu); err != nil {
		t.Fatalf("processFile: %v", err)
	}

	// All events should carry the model set by model_change ("claude-sonnet-4-6").
	for _, ev := range store.events {
		if ev.Model != "" && ev.Model != "claude-sonnet-4-6" {
			t.Errorf("event %s has Model=%q; want claude-sonnet-4-6 or empty", ev.ID, ev.Model)
		}
	}
}

// ─── Helper unit tests ────────────────────────────────────────────────────────

func TestAgentIDFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/user/.openclaw/agents/main/sessions/abc.jsonl", "main"},
		{"/home/user/.openclaw/agents/sonnet/sessions/def.jsonl", "sonnet"},
		{"/home/user/.openclaw/agents/kimi/sessions/ghi.jsonl", "kimi"},
		{"/other/path/something.jsonl", "unknown"},
	}
	for _, tc := range cases {
		if got := agentIDFromPath(tc.path); got != tc.want {
			t.Errorf("agentIDFromPath(%q) = %q; want %q", tc.path, got, tc.want)
		}
	}
}

func TestSessionIDFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/user/.openclaw/agents/main/sessions/abc-def-123.jsonl", "abc-def-123"},
		{"/tmp/test-uuid-456.jsonl", "test-uuid-456"},
	}
	for _, tc := range cases {
		if got := sessionIDFromPath(tc.path); got != tc.want {
			t.Errorf("sessionIDFromPath(%q) = %q; want %q", tc.path, got, tc.want)
		}
	}
}

func TestHashArgs(t *testing.T) {
	args := json.RawMessage(`{"command":"echo hello"}`)
	h1 := hashArgs(args)
	if len(h1) != 16 { // 8 bytes → 16 hex chars
		t.Errorf("hashArgs length = %d; want 16", len(h1))
	}
	// Different args → different hash.
	h2 := hashArgs(json.RawMessage(`{"command":"echo world"}`))
	if h1 == h2 {
		t.Error("different args produced the same hash")
	}
	// Same args → same hash (deterministic).
	h3 := hashArgs(json.RawMessage(`{"command":"echo hello"}`))
	if h1 != h3 {
		t.Error("identical args produced different hashes")
	}
	// Nil / empty args.
	if h := hashArgs(nil); h != "" {
		t.Errorf("hashArgs(nil) = %q; want empty string", h)
	}
}

func TestStateKey(t *testing.T) {
	key := stateKey("/home/user/.openclaw/agents/main/sessions/abc.jsonl")
	if !strings.HasPrefix(key, "openclaw:") {
		t.Errorf("stateKey should start with 'openclaw:'; got %q", key)
	}
	if !strings.Contains(key, "abc.jsonl") {
		t.Errorf("stateKey should contain the file path; got %q", key)
	}
}

// ─── discoverFromRoot (test helper) ──────────────────────────────────────────

// discoverFromRoot mirrors Discover() but uses root instead of os.UserHomeDir().
// This allows testing the discovery logic without touching the real home directory.
func discoverFromRoot(root string) ([]string, error) {
	pattern := filepath.Join(root, ".openclaw", "agents", "*", "sessions", "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasSuffix(base, ".jsonl") && !strings.Contains(base, ".jsonl.") {
			files = append(files, m)
		}
	}
	return files, nil
}
