package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sessionIDPattern bounds session_id before it becomes part of a filename.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type hookInput struct {
	SessionID string `json:"session_id"`
	EventName string `json:"hook_event_name"`
}

// sessionsDir and sessionEntry are defined in agents.go, which reads the files this mode writes.

// Hook contract: never write stdout (some events pipe it into the model's context), always exit 0 (a failing hook must not block the turn).
func runHook() {
	defer func() {
		// Never let a panic surface as a nonzero exit from a hook.
		_ = recover()
		os.Exit(0)
	}()

	in, err := readHookInput(os.Stdin)
	if err != nil {
		return
	}
	if !sessionIDPattern.MatchString(in.SessionID) {
		return
	}

	event := hookEventState(in.EventName)
	if event == "" {
		// Unknown event, or SubagentStop: a subagent finishing is not the main agent's turn ending.
		return
	}

	pid := findClaudePID()
	_ = applyHookEvent(sessionsDir, in.EventName, in.SessionID, pid)
}

// Cap at 1MB — hook payloads are small JSON objects; anything larger is not one.
func readHookInput(r io.Reader) (hookInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return hookInput{}, err
	}
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hookInput{}, err
	}
	return in, nil
}

// "" means don't change state: unknown events and SubagentStop (a subagent ending is not the main agent's turn ending).
func hookEventState(event string) string {
	switch event {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return "running"
	case "Stop":
		return "idle"
	case "Notification":
		return "blocked"
	case "SessionEnd":
		return "deleted"
	default:
		return ""
	}
}

// Assumes sessionID and event validity were already checked by the caller.
func applyHookEvent(dir string, event, sessionID string, pid int) error {
	state := hookEventState(event)
	if state == "" {
		return nil
	}

	path := filepath.Join(dir, sessionID+".json")

	if state == "deleted" {
		err := os.Remove(path)
		if err != nil && os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	s := sessionEntry{State: state, PID: pid, TS: time.Now()}
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}

	return atomicWriteFile(dir, path, body)
}

// dir must equal filepath.Dir(path): same-dir temp+rename is atomic, but a cross-filesystem temp would make the rename a copy, not a replace.
func atomicWriteFile(dir, path string, body []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Walk up from our parent to the first non-shell ancestor — that's the `claude` process (hooks run as its child, possibly via `/bin/sh -c`). Capped at 5 levels.
func findClaudePID() int {
	pid := os.Getppid()
	for i := 0; i < 5; i++ {
		if pid <= 1 {
			break
		}
		ppid, comm, err := psLookup(pid)
		if err != nil {
			break
		}
		name := filepath.Base(comm)
		if name != "sh" && name != "bash" && name != "zsh" {
			return pid
		}
		if ppid <= 0 {
			break
		}
		pid = ppid
	}
	// Ambiguous (all shells, or ps failed): fall back to the immediate parent; downstream liveness tolerates a wrong-but-alive PID.
	return os.Getppid()
}

// comm is a full path on macOS (e.g. /bin/zsh) and may contain spaces, so split off only the first field (ppid) and keep the rest as comm.
func psLookup(pid int) (ppid int, comm string, err error) {
	out, err := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", err
	}
	line := strings.TrimSpace(string(out))
	fields := strings.SplitN(strings.TrimLeft(line, " "), " ", 2)
	if len(fields) < 2 {
		return 0, "", errNoParse
	}
	ppid, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", err
	}
	return ppid, strings.TrimSpace(fields[1]), nil
}

var errNoParse = errParseError("ps output did not parse")

type errParseError string

func (e errParseError) Error() string { return string(e) }
