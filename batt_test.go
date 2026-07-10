package main

import (
	"os/exec"
	"testing"
)

// Captured real `pmset -g batt` output shapes.
const (
	battACFull      = "Now drawing from 'AC Power'\n -InternalBattery-0 (id=36110435)\t100%; charged; 0:00 remaining present: true\n"
	battACCharging  = "Now drawing from 'AC Power'\n -InternalBattery-0 (id=36110435)\t83%; AC attached; not charging present: true\n"
	battDischarging = "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=36110435)\t57%; discharging; 3:20 remaining present: true\n"
)

func TestParseBattAC(t *testing.T) {
	pct, onAC, err := parseBatt(battACFull)
	if err != nil {
		t.Fatalf("parseBatt: %v", err)
	}
	if pct != 100 || !onAC {
		t.Errorf("parseBatt(ACFull) = (%d, %v), want (100, true)", pct, onAC)
	}
}

func TestParseBattACCharging(t *testing.T) {
	pct, onAC, err := parseBatt(battACCharging)
	if err != nil {
		t.Fatalf("parseBatt: %v", err)
	}
	if pct != 83 || !onAC {
		t.Errorf("parseBatt(ACCharging) = (%d, %v), want (83, true)", pct, onAC)
	}
}

func TestParseBattDischarging(t *testing.T) {
	pct, onAC, err := parseBatt(battDischarging)
	if err != nil {
		t.Fatalf("parseBatt: %v", err)
	}
	if pct != 57 || onAC {
		t.Errorf("parseBatt(Discharging) = (%d, %v), want (57, false)", pct, onAC)
	}
}

func TestParseBattMalformed(t *testing.T) {
	if _, _, err := parseBatt("garbage, no percent sign here"); err == nil {
		t.Error("parseBatt on garbage input should error")
	}
	if _, _, err := parseBatt(""); err == nil {
		t.Error("parseBatt on empty input should error")
	}
}

func TestBattOK(t *testing.T) {
	cases := []struct {
		pct, battMin int
		onAC, want   bool
	}{
		{pct: 5, battMin: 20, onAC: true, want: true},    // AC always OK regardless of pct
		{pct: 21, battMin: 20, onAC: false, want: true},  // strictly above battMin
		{pct: 20, battMin: 20, onAC: false, want: false}, // equal to battMin is NOT ok
		{pct: 19, battMin: 20, onAC: false, want: false},
	}
	for _, c := range cases {
		if got := battOK(c.pct, c.onAC, c.battMin); got != c.want {
			t.Errorf("battOK(%d, %v, %d) = %v, want %v", c.pct, c.onAC, c.battMin, got, c.want)
		}
	}
}

func TestParseBattAgainstLiveMachine(t *testing.T) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		t.Skipf("pmset -g batt unavailable: %v", err)
	}
	pct, _, err := parseBatt(string(out))
	if err != nil {
		t.Fatalf("parseBatt(live output) failed on %q: %v", string(out), err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("parseBatt(live output) = %d%%, want 0-100", pct)
	}
}
