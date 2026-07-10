package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Tool events dispatch per-tool so their hook object takes a "*" matcher;
// lifecycle events must omit the matcher key entirely — a present-but-empty
// matcher is a different, wrong shape to Claude Code.
var (
	toolHookEvents      = []string{"PreToolUse", "PostToolUse"}
	lifecycleHookEvents = []string{"UserPromptSubmit", "Stop", "Notification", "SessionEnd"}
)

// installHooks points settings.json at the installed app bundle, not the
// build-dir binary that `make clean` deletes.
func installHooks() error {
	settingsPath, binaryPath, err := defaultHookPaths()
	if err != nil {
		return err
	}
	return installHooksAt(settingsPath, binaryPath)
}

func uninstallHooks() error {
	settingsPath, binaryPath, err := defaultHookPaths()
	if err != nil {
		return err
	}
	return uninstallHooksAt(settingsPath, binaryPath)
}

func defaultHookPaths() (settingsPath, binaryPath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	settingsPath = filepath.Join(home, ".claude", "settings.json")
	binaryPath = filepath.Join(home, "Applications", "ClaudeSidecar.app", "Contents", "MacOS", "ClaudeSidecar")
	return settingsPath, binaryPath, nil
}

// installHooksAt is the testable core: params let tests use a t.TempDir()
// instead of the real settings file and app bundle.
func installHooksAt(settingsPath, binaryPath string) error {
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("hook command %q not installed — run `make install-agent` before `install-hooks`: %w", binaryPath, err)
	}

	settings, err := readSettingsFile(settingsPath)
	if err != nil {
		return err
	}
	if err := backupSettingsOnce(settingsPath); err != nil {
		return err
	}

	settings = mergeHooks(settings, binaryPath+" --hook")
	return writeSettingsFile(settingsPath, settings)
}

func uninstallHooksAt(settingsPath, binaryPath string) error {
	settings, err := readSettingsFile(settingsPath)
	if err != nil {
		return err
	}
	settings = removeHooks(settings, binaryPath+" --hook")
	return writeSettingsFile(settingsPath, settings)
}

// mergeHooks adds command under each event, skipping events that already have
// it (idempotent — safe on every `make install-hooks`) and leaving all other
// settings, including foreign hooks, untouched.
func mergeHooks(settings map[string]any, command string) map[string]any {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
	}

	for _, event := range toolHookEvents {
		hooks[event] = appendHookEntry(hooks[event], command, true)
	}
	for _, event := range lifecycleHookEvents {
		hooks[event] = appendHookEntry(hooks[event], command, false)
	}

	settings["hooks"] = hooks
	return settings
}

// appendHookEntry adds the hook entry unless one already carries this exact command.
func appendHookEntry(existing any, command string, withMatcher bool) []any {
	arr, _ := existing.([]any)
	if hookArrayHasCommand(arr, command) {
		return arr
	}

	entry := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	}
	if withMatcher {
		entry["matcher"] = "*"
	}
	return append(arr, entry)
}

func hookArrayHasCommand(arr []any, command string) bool {
	for _, item := range arr {
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
				return true
			}
		}
	}
	return false
}

// removeHooks deletes command's hook objects and prunes anything left empty. It
// never touches a top-level key that merely shares an event's name (e.g. a stray
// "Notification" outside "hooks"), since it only looks inside settings["hooks"].
func removeHooks(settings map[string]any, command string) map[string]any {
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return settings
	}

	for event, val := range hooks {
		arr, ok := val.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(arr))
		for _, item := range arr {
			entry, ok := item.(map[string]any)
			if !ok {
				kept = append(kept, item)
				continue
			}
			inner, _ := entry["hooks"].([]any)
			filtered := make([]any, 0, len(inner))
			for _, h := range inner {
				hookObj, ok := h.(map[string]any)
				if ok {
					if c, _ := hookObj["command"].(string); c == command {
						continue
					}
				}
				filtered = append(filtered, h)
			}
			if len(filtered) == 0 {
				// Drop the entry rather than leave an empty "hooks":[].
				continue
			}
			entry["hooks"] = filtered
			kept = append(kept, entry)
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}

	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return settings
}

// readSettingsFile treats a missing file as empty settings, not an error, so
// install-hooks works before the user has ever touched ~/.claude/settings.json.
func readSettingsFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

// backupSettingsOnce writes settingsPath+".bak" once and never overwrites it,
// preserving the user's pre-install original across repeated runs.
func backupSettingsOnce(path string) error {
	bakPath := path + ".bak"
	if _, err := os.Stat(bakPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(bakPath, data, 0644)
}

// writeSettingsFile writes atomically via a same-directory temp file + rename.
// Key reorder/reindent versus the original is fine; only semantic content must round-trip.
func writeSettingsFile(path string, settings map[string]any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return atomicWriteFile(dir, path, body)
}
