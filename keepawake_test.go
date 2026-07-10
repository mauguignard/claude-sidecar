package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeepAwakeStateBody(t *testing.T) {
	ts := time.Unix(1783695600, 0)
	got := string(keepAwakeStateBody(true, 20, ts))
	want := "want=1\nbattMin=20\nts=1783695600\n"
	if got != want {
		t.Errorf("keepAwakeStateBody(true) = %q, want %q", got, want)
	}

	got = string(keepAwakeStateBody(false, 20, ts))
	want = "want=0\nbattMin=20\nts=1783695600\n"
	if got != want {
		t.Errorf("keepAwakeStateBody(false) = %q, want %q", got, want)
	}
}

func TestKeepAwakeBattMinClamp(t *testing.T) {
	cases := []struct {
		env  string
		want int
	}{
		{"", 20},
		{"0", 5},
		{"3", 5},
		{"5", 5},
		{"20", 20},
		{"80", 80},
		{"200", 80},
		{"not-a-number", 20},
	}
	for _, c := range cases {
		t.Run(c.env, func(t *testing.T) {
			if c.env == "" {
				os.Unsetenv("CLAUDE_USAGE_BATT_MIN")
			} else {
				os.Setenv("CLAUDE_USAGE_BATT_MIN", c.env)
			}
			defer os.Unsetenv("CLAUDE_USAGE_BATT_MIN")
			if got := keepAwakeBattMin(); got != c.want {
				t.Errorf("keepAwakeBattMin() with env=%q = %d, want %d", c.env, got, c.want)
			}
		})
	}
}

func TestKeepAwakeWriteStateAtomic(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "claude-sidecar")
	statePath := filepath.Join(base, "keepawake.state")

	write := func(want bool, battMin int, ts time.Time) error {
		if err := os.MkdirAll(base, 0755); err != nil {
			return err
		}
		return atomicWriteFile(base, statePath, keepAwakeStateBody(want, battMin, ts))
	}

	now := time.Now()
	if err := write(true, 20, now); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(b), "want=1\n") {
		t.Errorf("body = %q, want prefix want=1", string(b))
	}

	if err := write(false, 20, now); err != nil {
		t.Fatalf("write want=0: %v", err)
	}
	b, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.HasPrefix(string(b), "want=0\n") {
		t.Errorf("body = %q, want prefix want=0", string(b))
	}
}

func TestKeepAwakeEnabledGetSet(t *testing.T) {
	orig := keepAwakeEnabledGet()
	defer keepAwakeEnabledSet(orig)

	keepAwakeEnabledSet(false)
	if keepAwakeEnabledGet() {
		t.Error("keepAwakeEnabledGet() = true after Set(false)")
	}
	keepAwakeEnabledSet(true)
	if !keepAwakeEnabledGet() {
		t.Error("keepAwakeEnabledGet() = false after Set(true)")
	}
}
