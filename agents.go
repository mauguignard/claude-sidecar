package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Shared location so the hook writer and this poller agree without any IPC.
const sessionsDir = "/Users/Shared/claude-sidecar/sessions"

// Backstop for session files that never got a terminal hook event (crash, SIGKILL); liveness alone can't tell "still working" from "hung".
const sessionMaxAge = 24 * time.Hour

// On-disk shape hooks.go writes: {"state":"running|idle|blocked","pid":<int>,"ts":"<RFC3339>"}.
type sessionEntry struct {
	State string    `json:"state"`
	PID   int       `json:"pid"`
	TS    time.Time `json:"ts"`
}

// pid>0 is checked before any liveness call: pid 0 signals our own process group and would read as alive forever.
func aggregate(entries []sessionEntry, alive func(int) bool, now time.Time) (running, blocked int) {
	for _, e := range entries {
		if now.Sub(e.TS) >= sessionMaxAge {
			continue
		}
		switch e.State {
		case "running":
			if e.PID > 0 && alive(e.PID) {
				running++
			}
		case "blocked":
			if e.PID > 0 && alive(e.PID) {
				blocked++
			}
		}
	}
	return running, blocked
}

// EPERM means the process exists but we lack permission to signal it — still alive.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// A missing dir means zero sessions, not an error; never creates the dir — only the hook writer does.
func scanSessions(dir string, now time.Time) []sessionEntry {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []sessionEntry
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, f.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var e sessionEntry
		if err := json.Unmarshal(b, &e); err != nil {
			continue
		}
		if now.Sub(e.TS) >= sessionMaxAge {
			_ = os.Remove(path)
			continue
		}
		out = append(out, e)
	}
	return out
}

var (
	agentsRunning atomic.Int64
	agentsBlocked atomic.Int64
)

// Previous poll's running count, so we notify only on 0<->N transitions, not every tick.
var lastRunningCount int

// Guards the transition state in case polling ever runs concurrently.
var notifyMu sync.Mutex

// Plain-ticker scan of sessionsDir every interval, no fsnotify.
func agentsPollLoop(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	agentsPoll()
	for range tick.C {
		agentsPoll()
	}
}

func agentsPoll() {
	now := time.Now()
	entries := scanSessions(sessionsDir, now)
	running, blocked := aggregate(entries, pidAlive, now)
	agentsRunning.Store(int64(running))
	agentsBlocked.Store(int64(blocked))

	notifyMu.Lock()
	prev := lastRunningCount
	lastRunningCount = running
	notifyMu.Unlock()

	switch {
	case prev == 0 && running > 0:
		notify("Keeping Mac awake", pluralAgents(running)+" working")
	case prev > 0 && running == 0:
		notify("All agents finished", "normal sleep restored")
	}
}

func pluralAgents(n int) string {
	if n == 1 {
		return "1 agent"
	}
	return strconv.Itoa(n) + " agents"
}

// Best-effort: osascript notifications can be silently suppressed until the user grants permission, so errors are swallowed.
func notify(title, body string) {
	script := `display notification "` + escapeAppleScript(body) + `" with title "` + escapeAppleScript(title) + `"`
	_ = exec.Command("osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func agentsOnce() {
	now := time.Now()
	entries := scanSessions(sessionsDir, now)
	running, blocked := aggregate(entries, pidAlive, now)
	fmt.Printf("running: %d, blocked: %d\n", running, blocked)
}
