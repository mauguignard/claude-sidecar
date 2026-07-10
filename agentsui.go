package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
)

// Root LaunchDaemon; its presence alone tells the menu keep-awake can work — only it can flip SleepDisabled.
const sleepdPlistPath = "/Library/LaunchDaemons/io.github.mauguignard.claudesidecar.sleepd.plist"

// File existence is enough to label the menu; no need to confirm it's bootstrapped/running.
func sleepdInstalled() bool {
	return fileExists(sleepdPlistPath)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Persisted preferences, distinct from keepawake.state (that's the daemon heartbeat, not prefs).
const configPath = keepAwakeBaseDir + "/config.json"

type appConfig struct {
	KeepAwakeEnabled bool `json:"keepAwakeEnabled"`
}

// Defaults true (matching keepawake.go init) when the file is missing, unreadable, or malformed.
func loadConfigKeepAwakeEnabled() bool {
	return loadConfigKeepAwakeEnabledFrom(configPath)
}

func loadConfigKeepAwakeEnabledFrom(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var cfg appConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return true
	}
	return cfg.KeepAwakeEnabled
}

func saveConfigKeepAwakeEnabled(enabled bool) error {
	if err := os.MkdirAll(keepAwakeBaseDir, 0755); err != nil {
		return err
	}
	return saveConfigKeepAwakeEnabledTo(keepAwakeBaseDir, configPath, enabled)
}

func saveConfigKeepAwakeEnabledTo(dir, path string, enabled bool) error {
	b, err := json.Marshal(appConfig{KeepAwakeEnabled: enabled})
	if err != nil {
		return err
	}
	return atomicWriteFile(dir, path, b)
}

// Doesn't write want=0 itself; keepAwakeTick's next tick notices the false transition and writes it once.
func toggleKeepAwakeEnabled() {
	on := !menuKeepAwake.Checked()
	if on {
		menuKeepAwake.Check()
	} else {
		menuKeepAwake.Uncheck()
	}
	keepAwakeEnabledSet(on)
	_ = saveConfigKeepAwakeEnabled(on)
	renderAgents()
}

// Running takes priority over blocked: if counts ever disagree (mid-poll race), running is the more actionable state.
func agentsRowTitle(running, blocked int) (title string, c color.NRGBA) {
	switch {
	case running > 0:
		return "Agents: " + pluralAgents(running) + " working", colorOK
	case blocked > 0:
		return "Agents: blocked on you", colorWarn
	default:
		return "Agents: —", colorIdle
	}
}

// installed==false wins over all; battKnown==false is treated as battOK (fail-open, like keepAwakeTick).
func keepAwakeDetailLine(installed, enabled bool, running int, battKnown bool, pct int, onAC bool, battMin int) string {
	if !installed {
		return "Sleep helper not installed"
	}
	if !enabled || running == 0 {
		return "Normal sleep"
	}
	if battKnown && !battOK(pct, onAC, battMin) {
		return fmt.Sprintf("Paused — battery %d%% (≤ %d%% cutoff)", pct, battMin)
	}
	return "Keeping Mac awake (lid-safe)"
}
