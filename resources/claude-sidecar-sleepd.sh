#!/bin/bash
# claude-sidecar-sleepd.sh
#
# Root-owned LaunchDaemon script. Reads the heartbeat state file written by
# the claude-sidecar menu-bar app and enforces (or restores) macOS's
# SleepDisabled flag so the machine stays awake (even lid-closed) only while
# a Claude Code agent is actively running, a fresh heartbeat is present, and
# the battery (if not on AC) is above the configured threshold.
#
# This script performs exactly one privileged action: `pmset -a disablesleep`.
# It never eval's or sources the state file — only grep/parameter-expansion
# parsing of three scalars (want, battMin, ts), each validated before use.
# Anything unexpected fails closed (target=0).
#
# Env overrides (test seams):
#   STATE_FILE    path to the heartbeat state file
#                 (default: /Users/Shared/claude-sidecar/keepawake.state)
#   BATT_OVERRIDE "<pct> <ac|battery>" — use instead of `pmset -g batt`
#   DRY_RUN=1     echo "target=$target" and exit before touching pmset -a

set -u

STATE_FILE="${STATE_FILE:-/Users/Shared/claude-sidecar/keepawake.state}"

# --- read raw values out of the flat key=value file (grep only, no eval/source) ---

raw_want=""
raw_batt_min=""
raw_ts=""

if [ -f "$STATE_FILE" ]; then
    raw_want="$(grep -m1 '^want=' "$STATE_FILE" 2>/dev/null | cut -d= -f2-)"
    raw_batt_min="$(grep -m1 '^battMin=' "$STATE_FILE" 2>/dev/null | cut -d= -f2-)"
    raw_ts="$(grep -m1 '^ts=' "$STATE_FILE" 2>/dev/null | cut -d= -f2-)"
fi

# want: missing file/key or anything other than exactly "1" -> false
want=0
if [ "$raw_want" = "1" ]; then
    want=1
fi

# ts: must be all digits; anything else -> not fresh
ts_valid=1
case "$raw_ts" in
    '' | *[!0-9]*)
        ts_valid=0
        ;;
esac

# battMin: must be all digits; anything else -> default 20; then clamp 5-80
battMin=20
case "$raw_batt_min" in
    '' | *[!0-9]*)
        battMin=20
        ;;
    *)
        battMin="$raw_batt_min"
        ;;
esac
if [ "$battMin" -lt 5 ] 2>/dev/null; then
    battMin=5
fi
if [ "$battMin" -gt 80 ] 2>/dev/null; then
    battMin=80
fi

# --- freshness ---

now="$(date +%s)"
fresh=0
if [ "$ts_valid" -eq 1 ]; then
    age=$((now - raw_ts))
    if [ "$age" -le 90 ] && [ "$age" -ge 0 ]; then
        fresh=1
    fi
fi

# --- battery ---

onAC=0
pct=0

if [ -n "${BATT_OVERRIDE:-}" ]; then
    # test seam: "<pct> <ac|battery>"
    set -- $BATT_OVERRIDE
    ov_pct="${1:-0}"
    ov_state="${2:-battery}"
    case "$ov_pct" in
        '' | *[!0-9]*)
            pct=0
            ;;
        *)
            pct="$ov_pct"
            ;;
    esac
    if [ "$ov_state" = "ac" ]; then
        onAC=1
    else
        onAC=0
    fi
else
    batt_out="$(pmset -g batt 2>/dev/null)"
    if printf '%s\n' "$batt_out" | grep -q "AC Power"; then
        onAC=1
    fi
    pct_raw="$(printf '%s\n' "$batt_out" | grep -o '[0-9]\{1,3\}%' | head -n1 | tr -d '%')"
    case "$pct_raw" in
        '' | *[!0-9]*)
            pct=0
            ;;
        *)
            pct="$pct_raw"
            ;;
    esac
fi

battok=0
if [ "$onAC" -eq 1 ]; then
    battok=1
elif [ "$pct" -gt "$battMin" ]; then
    battok=1
fi

# --- target ---

target=0
if [ "$want" -eq 1 ] && [ "$fresh" -eq 1 ] && [ "$battok" -eq 1 ]; then
    target=1
fi

# --- current state ---

current="$(pmset -g 2>/dev/null | awk '/SleepDisabled/{print $NF}')"
case "$current" in
    '' | *[!0-9]*)
        current=0
        ;;
esac

if [ "${DRY_RUN:-0}" = "1" ]; then
    echo "target=$target"
    exit 0
fi

if [ "$current" != "$target" ]; then
    pmset -a disablesleep "$target"
fi

exit 0
