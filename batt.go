package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// battOK: app and daemon must share this exact comparison, or they disagree at the threshold.
func battOK(pct int, onAC bool, battMin int) bool {
	return onAC || pct > battMin
}

// parseBatt reads pct and onAC from `pmset -g batt`. The first line ("Now
// drawing from '…Power'") is the source of truth for onAC; second-line
// sub-states like "AC attached" can otherwise mislead.
func parseBatt(out string) (pct int, onAC bool, err error) {
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) == 0 {
		return 0, false, fmt.Errorf("parseBatt: empty output")
	}
	onAC = strings.Contains(lines[0], "AC Power")

	idx := strings.Index(out, "%")
	if idx < 0 {
		return 0, false, fmt.Errorf("parseBatt: no %% found in %q", out)
	}
	start := idx
	for start > 0 && out[start-1] >= '0' && out[start-1] <= '9' {
		start--
	}
	if start == idx {
		return 0, false, fmt.Errorf("parseBatt: no digits before %% in %q", out)
	}
	pct, err = strconv.Atoi(out[start:idx])
	if err != nil {
		return 0, false, fmt.Errorf("parseBatt: %w", err)
	}
	return pct, onAC, nil
}

func readBatt() (pct int, onAC bool, err error) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return 0, false, fmt.Errorf("pmset -g batt: %w", err)
	}
	return parseBatt(string(out))
}
