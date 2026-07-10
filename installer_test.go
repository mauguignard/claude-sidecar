package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Shaped like a real settings.json so the merge is exercised against a realistic
// tree: a foreign PreToolUse hook (must survive), and a top-level "Notification"
// key that sits OUTSIDE "hooks" — the merge must not confuse it with hooks.Notification.
const realSettingsFixture = `{
  "cleanupPeriodDays": 365,
  "permissions": {
    "deny": [
      "Bash(rm -rf /)", "Bash(rm -rf ~)", "Bash(sudo *)", "Bash(curl * | sh)"
    ]
  },
  "model": "sonnet",
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [ { "type": "command", "command": "other-tool audit --bash" } ] }
    ]
  },
  "statusLine": { "type": "command", "command": "ccstatusline", "padding": 0, "refreshInterval": 10 },
  "theme": "dark",
  "Notification": [
    { "hooks": [ { "command": "terminal-notifier -message 'Needs your permission' -title 'Claude Code'", "type": "command" } ], "matcher": "permission_prompt" }
  ]
}`

// foreignHookCommand is the unrelated PreToolUse hook the merge/uninstall must never touch.
const foreignHookCommand = "other-tool audit --bash"

const testCommand = "/Users/tester/Applications/ClaudeSidecar.app/Contents/MacOS/ClaudeSidecar --hook"

func decodeFixture(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}
	return m
}

func hookEntries(t *testing.T, settings map[string]any, event string) []any {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	arr, _ := hooks[event].([]any)
	return arr
}

func findEntryWithCommand(entries []any, command string) map[string]any {
	for _, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := entry["hooks"].([]any)
		for _, h := range inner {
			hookObj, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, _ := hookObj["command"].(string); c == command {
				return entry
			}
		}
	}
	return nil
}

func TestMergeHooksShapesAndIdempotency(t *testing.T) {
	settings := decodeFixture(t, realSettingsFixture)
	orig := decodeFixture(t, realSettingsFixture)

	merged := mergeHooks(settings, testCommand)

	toolEvents := []string{"PreToolUse", "PostToolUse"}
	lifecycleEvents := []string{"UserPromptSubmit", "Stop", "Notification", "SessionEnd"}

	for _, event := range toolEvents {
		entries := hookEntries(t, merged, event)
		entry := findEntryWithCommand(entries, testCommand)
		if entry == nil {
			t.Fatalf("hooks.%s: no entry for our command; entries=%+v", event, entries)
		}
		if entry["matcher"] != "*" {
			t.Errorf("hooks.%s: matcher = %v, want \"*\"", event, entry["matcher"])
		}
	}

	for _, event := range lifecycleEvents {
		entries := hookEntries(t, merged, event)
		entry := findEntryWithCommand(entries, testCommand)
		if entry == nil {
			t.Fatalf("hooks.%s: no entry for our command; entries=%+v", event, entries)
		}
		if _, present := entry["matcher"]; present {
			t.Errorf("hooks.%s: matcher key present (%v), want no matcher key at all for a lifecycle event", event, entry["matcher"])
		}
	}

	preTool := hookEntries(t, merged, "PreToolUse")
	foreign := findEntryWithCommand(preTool, foreignHookCommand)
	if foreign == nil {
		t.Fatal("foreign PreToolUse hook was lost during merge")
	}
	if foreign["matcher"] != "Bash" {
		t.Errorf("foreign hook matcher = %v, want \"Bash\"", foreign["matcher"])
	}

	for _, key := range []string{"cleanupPeriodDays", "permissions", "model",
		"statusLine", "theme", "Notification"} {
		if !reflect.DeepEqual(merged[key], orig[key]) {
			t.Errorf("top-level key %q changed by merge:\n got: %#v\nwant: %#v", key, merged[key], orig[key])
		}
	}

	// The stray top-level "Notification" and hooks.Notification are independent values, not folded into each other.
	strayNotification, _ := merged["Notification"].([]any)
	if len(strayNotification) != 1 {
		t.Fatalf("stray top-level Notification key corrupted: %+v", strayNotification)
	}
	strayEntry, _ := strayNotification[0].(map[string]any)
	if strayEntry["matcher"] != "permission_prompt" {
		t.Errorf("stray top-level Notification entry changed: %+v", strayEntry)
	}

	// Merging twice must be a no-op so repeated install-hooks runs stay idempotent.
	again := mergeHooks(merged, testCommand)
	if !reflect.DeepEqual(merged, again) {
		t.Errorf("merge is not idempotent:\nfirst:  %#v\nsecond: %#v", merged, again)
	}
}

func TestUninstallHooksRoundTrip(t *testing.T) {
	orig := decodeFixture(t, realSettingsFixture)
	settings := decodeFixture(t, realSettingsFixture)

	merged := mergeHooks(settings, testCommand)
	restored := removeHooks(merged, testCommand)

	if !reflect.DeepEqual(orig, restored) {
		t.Errorf("uninstall did not restore the original settings:\n got: %#v\nwant: %#v", restored, orig)
	}
}

func TestRemoveHooksLeavesOtherCommandsAlone(t *testing.T) {
	settings := decodeFixture(t, realSettingsFixture)
	merged := mergeHooks(settings, testCommand)

	after := removeHooks(merged, testCommand)

	preTool := hookEntries(t, after, "PreToolUse")
	if findEntryWithCommand(preTool, foreignHookCommand) == nil {
		t.Fatal("removeHooks deleted an unrelated command's entry")
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop", "Notification", "SessionEnd"} {
		if findEntryWithCommand(hookEntries(t, after, event), testCommand) != nil {
			t.Errorf("hooks.%s still contains our command after uninstall", event)
		}
	}
}

func TestRemoveHooksPrunesEmptyArraysAndKeys(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": testCommand},
					},
				},
			},
		},
	}
	after := removeHooks(settings, testCommand)

	hooks, ok := after["hooks"].(map[string]any)
	if ok {
		if _, present := hooks["Stop"]; present {
			t.Errorf("hooks.Stop should have been pruned entirely, got %+v", hooks["Stop"])
		}
		if len(hooks) != 0 {
			t.Errorf("hooks should be empty after pruning, got %+v", hooks)
		}
	}
	if _, present := after["hooks"]; present {
		t.Errorf(`"hooks" key should be removed entirely once empty, got %+v`, after["hooks"])
	}
}

// Installing hooks that point at a not-yet-installed binary would make every future turn report a hook failure.
func TestInstallHooksAtRefusesMissingBinary(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(realSettingsFixture), 0644); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(dir, "Applications", "ClaudeSidecar.app", "Contents", "MacOS", "ClaudeSidecar")

	err := installHooksAt(settingsPath, binaryPath)
	if err == nil {
		t.Fatal("installHooksAt should refuse to run when the app binary is not installed")
	}

	data, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	got := decodeFixture(t, string(data))
	want := decodeFixture(t, realSettingsFixture)
	if !reflect.DeepEqual(got, want) {
		t.Error("settings file was modified despite the missing-binary guard")
	}
}

func TestInstallHooksAtEndToEnd(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(realSettingsFixture), 0644); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(dir, "Applications", "ClaudeSidecar.app", "Contents", "MacOS", "ClaudeSidecar")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := installHooksAt(settingsPath, binaryPath); err != nil {
		t.Fatalf("installHooksAt: %v", err)
	}

	bakPath := settingsPath + ".bak"
	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("expected a one-time backup at %s: %v", bakPath, err)
	}
	if string(bakData) != realSettingsFixture {
		t.Error("backup does not match the pre-install settings content")
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("installed settings.json is not valid JSON: %v", err)
	}
	command := binaryPath + " --hook"
	if findEntryWithCommand(hookEntries(t, settings, "PreToolUse"), command) == nil {
		t.Error("installed settings missing our PreToolUse entry")
	}

	// A second install must not overwrite the existing backup.
	if err := os.WriteFile(bakPath, []byte("sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := installHooksAt(settingsPath, binaryPath); err != nil {
		t.Fatalf("second installHooksAt: %v", err)
	}
	bakData2, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bakData2) != "sentinel" {
		t.Error("install-hooks overwrote an existing .bak file")
	}

	if err := uninstallHooksAt(settingsPath, binaryPath); err != nil {
		t.Fatalf("uninstallHooksAt: %v", err)
	}
	finalData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeFixture(t, string(finalData))
	want := decodeFixture(t, realSettingsFixture)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uninstall did not restore original settings:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestReadSettingsFileMissingIsEmptyMap(t *testing.T) {
	dir := t.TempDir()
	settings, err := readSettingsFile(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("readSettingsFile on missing file: %v", err)
	}
	if len(settings) != 0 {
		t.Errorf("expected empty map for missing file, got %+v", settings)
	}
}
