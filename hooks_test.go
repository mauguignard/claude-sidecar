package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSession(t *testing.T, dir, id string) sessionEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var s sessionEntry
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal session file: %v", err)
	}
	return s
}

func TestApplyHookEventRunningEvents(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		dir := t.TempDir()
		if err := applyHookEvent(dir, event, "sess1", 4242); err != nil {
			t.Fatalf("%s: applyHookEvent: %v", event, err)
		}
		s := readSession(t, dir, "sess1")
		if s.State != "running" {
			t.Errorf("%s: state = %q, want running", event, s.State)
		}
		if s.PID != 4242 {
			t.Errorf("%s: pid = %d, want 4242", event, s.PID)
		}
		if s.TS.IsZero() {
			t.Errorf("%s: ts not set", event)
		}
	}
}

func TestApplyHookEventStopMeansIdle(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "UserPromptSubmit", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	if err := applyHookEvent(dir, "Stop", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	s := readSession(t, dir, "sess1")
	if s.State != "idle" {
		t.Errorf("state = %q, want idle", s.State)
	}
}

func TestApplyHookEventNotificationMeansBlocked(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "Notification", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	s := readSession(t, dir, "sess1")
	if s.State != "blocked" {
		t.Errorf("state = %q, want blocked", s.State)
	}
}

func TestApplyHookEventSessionEndDeletesFile(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "UserPromptSubmit", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sess1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file should exist before SessionEnd: %v", err)
	}
	if err := applyHookEvent(dir, "SessionEnd", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be gone after SessionEnd, stat err = %v", err)
	}
}

func TestApplyHookEventSessionEndOnMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "SessionEnd", "never-existed", 1); err != nil {
		t.Errorf("SessionEnd on a missing file should be a no-op, got %v", err)
	}
}

// A subagent finishing does not mean the main agent's turn ended.
func TestApplyHookEventSubagentStopIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "UserPromptSubmit", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	if err := applyHookEvent(dir, "SubagentStop", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	s := readSession(t, dir, "sess1")
	if s.State != "running" {
		t.Errorf("SubagentStop must not change state, got %q", s.State)
	}
}

func TestApplyHookEventUnknownEventIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := applyHookEvent(dir, "SomeFutureEvent", "sess1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sess1.json")); !os.IsNotExist(err) {
		t.Error("unknown event should not create a session file")
	}
}

func TestApplyHookEventCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	if err := applyHookEvent(dir, "UserPromptSubmit", "sess1", 1); err != nil {
		t.Fatalf("applyHookEvent should MkdirAll: %v", err)
	}
	readSession(t, dir, "sess1")
}

func TestSessionIDPattern(t *testing.T) {
	valid := []string{"abc", "abc-123_XYZ", strings.Repeat("a", 128)}
	for _, id := range valid {
		if !sessionIDPattern.MatchString(id) {
			t.Errorf("expected %q to be valid", id)
		}
	}
	invalid := []string{
		"", "../../etc/passwd", "has space", "has/slash",
		strings.Repeat("a", 129), "semi;colon", "dot.dot",
	}
	for _, id := range invalid {
		if sessionIDPattern.MatchString(id) {
			t.Errorf("expected %q to be invalid", id)
		}
	}
}

func TestReadHookInputParsesFields(t *testing.T) {
	in, err := readHookInput(strings.NewReader(`{"session_id":"abc123","hook_event_name":"Stop","extra":"ignored"}`))
	if err != nil {
		t.Fatalf("readHookInput: %v", err)
	}
	if in.SessionID != "abc123" || in.EventName != "Stop" {
		t.Errorf("got %+v", in)
	}
}

func TestReadHookInputMalformedJSON(t *testing.T) {
	if _, err := readHookInput(strings.NewReader(`not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

func TestReadHookInputEmptyInput(t *testing.T) {
	if _, err := readHookInput(strings.NewReader(``)); err == nil {
		t.Error("expected an error for empty input")
	}
}

func TestReadHookInputMissingFields(t *testing.T) {
	in, err := readHookInput(strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("readHookInput: %v", err)
	}
	if in.SessionID != "" || in.EventName != "" {
		t.Errorf("expected zero-value fields, got %+v", in)
	}
	// Downstream, a zero-value session_id must fail sanitization.
	if sessionIDPattern.MatchString(in.SessionID) {
		t.Error("empty session_id must not pass sanitization")
	}
}

func TestReadHookInputRespectsSizeLimit(t *testing.T) {
	huge := `{"session_id":"` + strings.Repeat("a", 2<<20) + `"}`
	in, err := readHookInput(strings.NewReader(huge))
	// The reader is capped at 1MB, so oversized input truncates mid-string and fails to parse.
	if err == nil {
		t.Errorf("expected truncated oversized input to fail to parse, got %+v", in)
	}
}

func TestHookEventStateMapping(t *testing.T) {
	cases := map[string]string{
		"UserPromptSubmit": "running",
		"PreToolUse":       "running",
		"PostToolUse":      "running",
		"Stop":             "idle",
		"Notification":     "blocked",
		"SessionEnd":       "deleted",
		"SubagentStop":     "",
		"Bogus":            "",
	}
	for event, want := range cases {
		if got := hookEventState(event); got != want {
			t.Errorf("hookEventState(%q) = %q, want %q", event, got, want)
		}
	}
}
