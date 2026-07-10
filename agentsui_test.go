package main

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentsRowTitle(t *testing.T) {
	cases := []struct {
		running, blocked int
		wantTitle        string
		wantColor        color.NRGBA
	}{
		{0, 0, "Agents: —", colorIdle},
		{1, 0, "Agents: 1 agent working", colorOK},
		{3, 0, "Agents: 3 agents working", colorOK},
		{0, 1, "Agents: blocked on you", colorWarn},
		{2, 1, "Agents: 2 agents working", colorOK}, // running wins over blocked
	}
	for _, c := range cases {
		title, got := agentsRowTitle(c.running, c.blocked)
		if title != c.wantTitle {
			t.Errorf("agentsRowTitle(%d,%d) title = %q, want %q", c.running, c.blocked, title, c.wantTitle)
		}
		if got != c.wantColor {
			t.Errorf("agentsRowTitle(%d,%d) color = %v, want %v", c.running, c.blocked, got, c.wantColor)
		}
	}
}

func TestKeepAwakeDetailLine(t *testing.T) {
	cases := []struct {
		name      string
		installed bool
		enabled   bool
		running   int
		battKnown bool
		pct       int
		onAC      bool
		battMin   int
		want      string
	}{
		{"not installed wins over everything", false, true, 5, true, 90, false, 20, "Sleep helper not installed"},
		{"disabled", true, false, 5, true, 90, false, 20, "Normal sleep"},
		{"no agents running", true, true, 0, true, 90, false, 20, "Normal sleep"},
		{"running, battery unknown, fail open", true, true, 2, false, 0, false, 20, "Keeping Mac awake (lid-safe)"},
		{"running, on AC, low pct doesn't matter", true, true, 2, true, 5, true, 20, "Keeping Mac awake (lid-safe)"},
		{"running, on battery, above cutoff", true, true, 2, true, 50, false, 20, "Keeping Mac awake (lid-safe)"},
		{"running, on battery, at cutoff", true, true, 2, true, 20, false, 20, "Paused — battery 20% (≤ 20% cutoff)"},
		{"running, on battery, below cutoff", true, true, 2, true, 15, false, 20, "Paused — battery 15% (≤ 20% cutoff)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keepAwakeDetailLine(c.installed, c.enabled, c.running, c.battKnown, c.pct, c.onAC, c.battMin)
			if got != c.want {
				t.Errorf("keepAwakeDetailLine(...) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.plist")
	if fileExists(missing) {
		t.Error("fileExists(missing) = true, want false")
	}
	present := filepath.Join(dir, "present.plist")
	if err := os.WriteFile(present, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(present) {
		t.Error("fileExists(present) = false, want true")
	}
}

func TestLoadConfigKeepAwakeEnabledFrom(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "no-such-config.json")
	if !loadConfigKeepAwakeEnabledFrom(missing) {
		t.Error("missing file should default to true")
	}

	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if !loadConfigKeepAwakeEnabledFrom(malformed) {
		t.Error("malformed file should default to true")
	}

	off := filepath.Join(dir, "off.json")
	if err := os.WriteFile(off, []byte(`{"keepAwakeEnabled":false}`), 0644); err != nil {
		t.Fatal(err)
	}
	if loadConfigKeepAwakeEnabledFrom(off) {
		t.Error("keepAwakeEnabled:false should load as false")
	}

	on := filepath.Join(dir, "on.json")
	if err := os.WriteFile(on, []byte(`{"keepAwakeEnabled":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !loadConfigKeepAwakeEnabledFrom(on) {
		t.Error("keepAwakeEnabled:true should load as true")
	}
}

func TestSaveLoadConfigKeepAwakeEnabledRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := saveConfigKeepAwakeEnabledTo(dir, path, false); err != nil {
		t.Fatalf("save: %v", err)
	}
	if loadConfigKeepAwakeEnabledFrom(path) {
		t.Error("round trip: expected false")
	}

	if err := saveConfigKeepAwakeEnabledTo(dir, path, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !loadConfigKeepAwakeEnabledFrom(path) {
		t.Error("round trip: expected true")
	}
}
