package main

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

// Shared, world-writable dir the app and root daemon both read/write; app-side writers MkdirAll it first.
const keepAwakeBaseDir = "/Users/Shared/claude-sidecar"

// Heartbeat file the root daemon polls to decide whether to flip IOPMrootDomain's SleepDisabled flag.
const keepAwakeStatePath = keepAwakeBaseDir + "/keepawake.state"

// Shorter than keepAwakeHeartbeatInterval: bounds how fast a session transition is noticed (~5s).
const keepAwakeEvalInterval = 5 * time.Second

// How often the state file is rewritten while wanted, so the daemon's freshness check (ts within 90s) never goes stale.
const keepAwakeHeartbeatInterval = 30 * time.Second

var keepAwakeEnabled atomic.Bool

func init() {
	keepAwakeEnabled.Store(true)
}

func keepAwakeEnabledGet() bool {
	return keepAwakeEnabled.Load()
}

// Turning off doesn't write want=0 here; the poll loop notices the false transition and writes it once.
func keepAwakeEnabledSet(on bool) {
	keepAwakeEnabled.Store(on)
}

func keepAwakeBattMin() int {
	const def = 20
	v := os.Getenv("CLAUDE_USAGE_BATT_MIN")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	switch {
	case n < 5:
		return 5
	case n > 80:
		return 80
	default:
		return n
	}
}

// Flat key=value, not JSON: the shell daemon greps it and compares ts as an integer.
func keepAwakeStateBody(want bool, battMin int, ts time.Time) []byte {
	w := 0
	if want {
		w = 1
	}
	return []byte(fmt.Sprintf("want=%d\nbattMin=%d\nts=%d\n", w, battMin, ts.Unix()))
}

// Atomic write so the daemon never reads a partial state file.
func keepAwakeWriteState(want bool, battMin int, ts time.Time) error {
	if err := os.MkdirAll(keepAwakeBaseDir, 0755); err != nil {
		return err
	}
	body := keepAwakeStateBody(want, battMin, ts)
	return atomicWriteFile(keepAwakeBaseDir, keepAwakeStatePath, body)
}

// Previous tick's condition, so want=0 is written once on a true->false transition, not every tick.
var keepAwakeWasOn bool

// Guards against re-notifying every tick while battery stays below cutoff; resets when OK or on AC again.
var keepAwakeBattCutoffNotified bool

func keepAwakeLoop(evalInterval, heartbeatInterval time.Duration) {
	tick := time.NewTicker(evalInterval)
	defer tick.Stop()
	lastWrite := time.Time{}
	for range tick.C {
		keepAwakeTick(&lastWrite, heartbeatInterval)
	}
}

// Split from keepAwakeLoop so tests can drive one evaluation without real tickers.
func keepAwakeTick(lastWrite *time.Time, heartbeatInterval time.Duration) {
	now := time.Now()
	running := agentsRunning.Load() > 0

	batteryOK := true
	if running {
		if pct, onAC, err := readBatt(); err == nil {
			batteryOK = battOK(pct, onAC, keepAwakeBattMin())
			if batteryOK {
				keepAwakeBattCutoffNotified = false
			} else if !keepAwakeBattCutoffNotified {
				notify("Battery low", fmt.Sprintf("Battery at/below %d%% — letting the Mac sleep", keepAwakeBattMin()))
				keepAwakeBattCutoffNotified = true
			}
		}
		// pmset hiccup: keep prior batteryOK=true; the root daemon fails closed as the safety net.
	}

	on := keepAwakeEnabledGet() && running && batteryOK

	switch {
	case on && !keepAwakeWasOn:
		if keepAwakeWriteState(true, keepAwakeBattMin(), now) == nil {
			*lastWrite = now
		}
	case on && keepAwakeWasOn:
		if now.Sub(*lastWrite) >= heartbeatInterval {
			if keepAwakeWriteState(true, keepAwakeBattMin(), now) == nil {
				*lastWrite = now
			}
		}
	case !on && keepAwakeWasOn:
		if keepAwakeWriteState(false, keepAwakeBattMin(), now) == nil {
			*lastWrite = now
		}
	}
	keepAwakeWasOn = on
}

// Best-effort want=0 on clean exit; a SIGKILL skips it, so the root daemon's watchdog is the real cleanup.
func keepawakeShutdown() {
	_ = keepAwakeWriteState(false, keepAwakeBattMin(), time.Now())
}
