package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func alwaysAlive(pid int) bool { return true }
func neverAlive(pid int) bool  { return false }
func aliveOnly(pids ...int) func(int) bool {
	set := make(map[int]bool, len(pids))
	for _, p := range pids {
		set[p] = true
	}
	return func(pid int) bool { return set[pid] }
}

func TestAggregate(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-1 * time.Minute)
	stale := now.Add(-25 * time.Hour)

	tests := []struct {
		name        string
		entries     []sessionEntry
		alive       func(int) bool
		wantRunning int
		wantBlocked int
	}{
		{
			name:        "running and alive counts",
			entries:     []sessionEntry{{State: "running", PID: 123, TS: fresh}},
			alive:       alwaysAlive,
			wantRunning: 1,
		},
		{
			name:        "running but dead pid does not count",
			entries:     []sessionEntry{{State: "running", PID: 123, TS: fresh}},
			alive:       neverAlive,
			wantRunning: 0,
		},
		{
			name:        "running with pid 0 never calls alive, does not count",
			entries:     []sessionEntry{{State: "running", PID: 0, TS: fresh}},
			alive:       alwaysAlive,
			wantRunning: 0,
		},
		{
			name:        "running with missing/negative pid does not count",
			entries:     []sessionEntry{{State: "running", PID: -1, TS: fresh}},
			alive:       alwaysAlive,
			wantRunning: 0,
		},
		{
			name:        "idle does not count as running or blocked",
			entries:     []sessionEntry{{State: "idle", PID: 123, TS: fresh}},
			alive:       alwaysAlive,
			wantRunning: 0,
			wantBlocked: 0,
		},
		{
			name:        "blocked counts separately from running",
			entries:     []sessionEntry{{State: "blocked", PID: 123, TS: fresh}},
			alive:       alwaysAlive,
			wantBlocked: 1,
		},
		{
			name:        "stale entry older than 24h does not count regardless of state",
			entries:     []sessionEntry{{State: "running", PID: 123, TS: stale}},
			alive:       alwaysAlive,
			wantRunning: 0,
		},
		{
			name: "mixed entries: only running+alive+fresh and blocked+alive+fresh count",
			entries: []sessionEntry{
				{State: "running", PID: 1, TS: fresh},
				{State: "running", PID: 2, TS: fresh}, // dead
				{State: "blocked", PID: 3, TS: fresh},
				{State: "idle", PID: 4, TS: fresh},
				{State: "running", PID: 5, TS: stale}, // stale
			},
			alive:       aliveOnly(1, 3, 4, 5),
			wantRunning: 1,
			wantBlocked: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			running, blocked := aggregate(tt.entries, tt.alive, now)
			if running != tt.wantRunning || blocked != tt.wantBlocked {
				t.Errorf("aggregate() = (%d, %d), want (%d, %d)", running, blocked, tt.wantRunning, tt.wantBlocked)
			}
		})
	}
}

func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(1) {
		t.Error("pidAlive(1) (launchd, always present) should be alive")
	}
}

func TestScanSessionsMissingDir(t *testing.T) {
	entries := scanSessions("/nonexistent/path/that/should/not/exist", time.Now())
	if entries != nil {
		t.Errorf("scanSessions on missing dir = %v, want nil", entries)
	}
	if _, err := os.Stat("/nonexistent/path/that/should/not/exist"); err == nil {
		t.Error("scanSessions must not create the sessions dir")
	}
}

func writeSessionFile(t *testing.T, dir, name string, e sessionEntry) {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSessionsGCsStaleFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	writeSessionFile(t, dir, "fresh.json", sessionEntry{State: "running", PID: 1, TS: now.Add(-1 * time.Minute)})
	writeSessionFile(t, dir, "stale.json", sessionEntry{State: "running", PID: 2, TS: now.Add(-25 * time.Hour)})

	entries := scanSessions(dir, now)
	if len(entries) != 1 {
		t.Fatalf("scanSessions returned %d entries, want 1 (stale filtered)", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.json")); err == nil {
		t.Error("stale.json should have been GC'd by scanSessions")
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh.json")); err != nil {
		t.Error("fresh.json should still exist")
	}
}
