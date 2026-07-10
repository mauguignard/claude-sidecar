package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
)

var (
	httpClient = &http.Client{Timeout: 10 * time.Second}
	// Lazy: detectUserAgent execs `claude --version`; --hook mode runs on every tool call and must not pay that cost.
	userAgent = sync.OnceValue(detectUserAgent)

	menuSession      *systray.MenuItem
	menuSessionReset *systray.MenuItem
	menuWeekly       *systray.MenuItem
	menuWeeklyReset  *systray.MenuItem
	menuSonnet       *systray.MenuItem
	menuSonnetReset  *systray.MenuItem
	menuStatus       *systray.MenuItem
	menuStatusDetail *systray.MenuItem
	menuAgents       *systray.MenuItem
	menuAgentsDetail *systray.MenuItem
	menuKeepAwake    *systray.MenuItem
	menuUpdated      *systray.MenuItem
	menuRefresh      *systray.MenuItem
	menuQuit         *systray.MenuItem

	refreshNow       = make(chan struct{}, 1)
	refreshStatusNow = make(chan struct{}, 1)

	last   *usageResponse
	lastAt time.Time

	lastStatus *serviceStatus
)

func main() {
	once := flag.Bool("once", false, "fetch usage once, print JSON, and exit (for debugging)")
	status := flag.Bool("status", false, "fetch service status once, print the two menu lines, and exit (for debugging)")
	hook := flag.Bool("hook", false, "Claude Code hook receiver: read one hook event from stdin, update session state, and exit (never touches stdout)")
	agentsFlag := flag.Bool("agents", false, "scan sessionsDir once, print the running/blocked aggregate, and exit (for debugging)")
	installHooksFlag := flag.Bool("install-hooks", false, "merge Claude Code hook entries into ~/.claude/settings.json and exit")
	uninstallHooksFlag := flag.Bool("uninstall-hooks", false, "remove this app's hook entries from ~/.claude/settings.json and exit")
	flag.Parse()

	switch {
	case *hook:
		runHook()
	case *once:
		runOnce()
	case *status:
		statusOnce()
	case *agentsFlag:
		agentsOnce()
	case *installHooksFlag:
		if err := installHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("hooks installed")
	case *uninstallHooksFlag:
		if err := uninstallHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("hooks uninstalled")
	default:
		keepAwakeEnabledSet(loadConfigKeepAwakeEnabled())
		systray.Run(onReady, onExit)
	}
}

// onExit best-effort clears the keep-awake flag; SIGKILL skips it, so the daemon watchdog is the real cleanup.
func onExit() {
	keepawakeShutdown()
}

func runOnce() {
	token, _, err := loadToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, err := fetchUsage(ctx, httpClient, token, userAgent())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(u, "", "  ")
	fmt.Println(string(b))
}

func statusOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := fetchStatus(ctx, httpClient)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	now := time.Now()
	fmt.Printf("indicator: %s (page-level: %s)\n", s.effective(), s.Status.Indicator)
	fmt.Println(s.headline())
	fmt.Println(s.detail(now))
}

func onReady() {
	setStatus(colorIdle, "—")
	systray.SetTooltip("Claude usage")

	header := systray.AddMenuItem("Claude Usage", "")
	header.Disable()
	systray.AddSeparator()

	menuSession, menuSessionReset = addUsageRow("Session (5 hour)", "5-hour rolling session limit")
	menuWeekly, menuWeeklyReset = addUsageRow("Weekly (7 day)", "7-day limit across all models")
	menuSonnet, menuSonnetReset = addUsageRow("Weekly Sonnet (7 day)", "7-day Sonnet limit")
	menuSonnet.Hide()
	menuSonnetReset.Hide()

	systray.AddSeparator()
	menuStatus = systray.AddMenuItem("Claude status: checking…", "Open the Anthropic status page")
	menuStatus.SetIcon(dotIcon(colorIdle))
	menuStatusDetail = systray.AddMenuItem("…", "")
	menuStatusDetail.SetIcon(blankIcon())
	menuStatusDetail.Disable()

	systray.AddSeparator()
	menuAgents = systray.AddMenuItem("Agents: —", "Claude Code agents currently running (hooks required — see README)")
	menuAgents.SetIcon(dotIcon(colorIdle))
	menuAgentsDetail = systray.AddMenuItem("…", "")
	menuAgentsDetail.SetIcon(blankIcon())
	menuAgentsDetail.Disable()
	menuKeepAwake = systray.AddMenuItemCheckbox("Keep awake while agents run", "Disable idle/lid sleep while a Claude Code agent is working", keepAwakeEnabledGet())
	renderAgents()

	systray.AddSeparator()
	menuUpdated = systray.AddMenuItem("Last updated: never", "")
	menuUpdated.Disable()
	menuRefresh = systray.AddMenuItem("Refresh", "Fetch usage and service status immediately")
	systray.AddSeparator()
	menuQuit = systray.AddMenuItem("Quit ClaudeSidecar", "Quit ClaudeSidecar")

	go pollLoop(pollInterval())
	go statusPollLoop(statusInterval)
	go agentsPollLoop(5 * time.Second)
	go keepAwakeLoop(keepAwakeEvalInterval, keepAwakeHeartbeatInterval)
	go agentsUIPollLoop(5 * time.Second)
	go handleClicks()
}

// addUsageRow keeps the headline enabled so macOS draws it in full white; its clicks are ignored since nothing reads ClickedCh.
func addUsageRow(label, tooltip string) (row, reset *systray.MenuItem) {
	row = systray.AddMenuItem(label, tooltip)
	row.SetIcon(dotIcon(colorIdle))
	reset = systray.AddMenuItem("…", "")
	reset.SetIcon(blankIcon())
	reset.Disable()
	return row, reset
}

func setStatus(c color.NRGBA, text string) {
	systray.SetIcon(sparkIcon(c))
	systray.SetTitle(" " + text)
}

func handleClicks() {
	for {
		select {
		case <-menuRefresh.ClickedCh:
			nudge(refreshNow)
			nudge(refreshStatusNow)
		case <-menuStatus.ClickedCh:
			openURL(statusPageURL)
		case <-menuKeepAwake.ClickedCh:
			toggleKeepAwakeEnabled()
		case <-menuQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func openURL(url string) {
	_ = exec.Command("open", url).Start()
}

func pollLoop(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	poll()
	for {
		select {
		case <-tick.C:
			poll()
		case <-refreshNow:
			poll()
		}
	}
}

func poll() {
	token, _, err := loadToken()
	if err != nil {
		setFatal("No Claude Code OAuth token found — sign in with Claude Code first.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u, err := fetchUsage(ctx, httpClient, token, userAgent())
	if err != nil {
		handleFetchErr(err)
		return
	}
	last = u
	lastAt = time.Now()
	render(u, lastAt)
}

func statusPollLoop(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	pollStatus()
	for {
		select {
		case <-tick.C:
			pollStatus()
		case <-refreshStatusNow:
			pollStatus()
		}
	}
}

func pollStatus() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := fetchStatus(ctx, httpClient)
	if err != nil {
		// Keep the last reading — its "Checked at …" line already dates it.
		if lastStatus == nil {
			menuStatus.SetIcon(dotIcon(colorIdle))
			menuStatus.SetTitle("Claude status unavailable")
			menuStatusDetail.SetTitle("Could not reach status.claude.com")
		}
		return
	}
	lastStatus = s
	renderStatus(s, time.Now())
}

func renderStatus(s *serviceStatus, at time.Time) {
	menuStatus.SetIcon(dotIcon(statusColor(s.effective())))
	menuStatus.SetTitle(s.headline())
	menuStatusDetail.SetTitle(s.detail(at))
}

// agentsUIPollLoop repaints on the same cadence agentsPollLoop refreshes on, avoiding a second scan of sessionsDir.
func agentsUIPollLoop(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for range tick.C {
		renderAgents()
	}
}

// renderAgents reads the battery fresh because it can change independently of the session count.
func renderAgents() {
	running := int(agentsRunning.Load())
	blocked := int(agentsBlocked.Load())

	title, c := agentsRowTitle(running, blocked)
	menuAgents.SetIcon(dotIcon(c))
	menuAgents.SetTitle(title)

	pct, onAC, err := readBatt()
	menuAgentsDetail.SetTitle(keepAwakeDetailLine(
		sleepdInstalled(), keepAwakeEnabledGet(), running, err == nil, pct, onAC, keepAwakeBattMin(),
	))
}

func handleFetchErr(err error) {
	if ue, ok := err.(*usageError); ok {
		switch ue.status {
		case http.StatusUnauthorized, http.StatusForbidden:
			setFatal("Token expired/invalid — run any Claude Code command to refresh it.")
			return
		case http.StatusTooManyRequests:
			markStale("rate-limited")
			return
		}
	}
	markStale("offline")
}

func render(u *usageResponse, at time.Time) {
	p5, p7 := u.FiveHour.Utilization, u.SevenDay.Utilization
	setStatus(usageColor(p5), fmt.Sprintf("%.0f%%", p5))
	systray.SetTooltip(fmt.Sprintf("Claude usage — session %.0f%% · weekly %.0f%%", p5, p7))

	setUsageRow(menuSession, menuSessionReset, "Session (5 hour)", u.FiveHour, resetClock)
	setUsageRow(menuWeekly, menuWeeklyReset, "Weekly (7 day)", u.SevenDay, resetDate)
	if s := u.SevenDaySonnet; s.Utilization > 0 || !s.ResetsAt.IsZero() {
		setUsageRow(menuSonnet, menuSonnetReset, "Weekly Sonnet (7 day)", s, resetDate)
		menuSonnet.Show()
		menuSonnetReset.Show()
	} else {
		menuSonnet.Hide()
		menuSonnetReset.Hide()
	}
	menuUpdated.SetTitle("Last updated: " + at.Format("3:04 PM"))
}

func setUsageRow(row, reset *systray.MenuItem, label string, w window, resetFmt func(time.Time) string) {
	row.SetIcon(dotIcon(usageColor(w.Utilization)))
	row.SetTitle(fmt.Sprintf("%s — %.0f%% used", label, w.Utilization))
	reset.SetTitle(resetFmt(w.ResetsAt))
}

func markStale(reason string) {
	if last != nil {
		menuUpdated.SetTitle(fmt.Sprintf("Last updated: %s (%s)", lastAt.Format("3:04 PM"), reason))
		return
	}
	setFatal("Could not reach usage endpoint (" + reason + ")")
}

func setFatal(detail string) {
	setStatus(colorIdle, "—")
	systray.SetTooltip(detail)
	for _, row := range []*systray.MenuItem{menuSession, menuWeekly} {
		row.SetIcon(dotIcon(colorIdle))
	}
	menuSession.SetTitle("Session (5 hour) — no data")
	menuSessionReset.SetTitle(detail)
	menuWeekly.SetTitle("Weekly (7 day) — no data")
	menuWeeklyReset.SetTitle("—")
	menuSonnet.Hide()
	menuSonnetReset.Hide()
	menuUpdated.SetTitle("Last updated: never")
}

func pollInterval() time.Duration {
	if v := os.Getenv("CLAUDE_USAGE_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 15 {
			return time.Duration(n) * time.Second
		}
	}
	return 60 * time.Second
}

// detectUserAgent builds a claude-code/<version> UA (the endpoint 429s without one); launchd login PATH is minimal, so probe common install paths before the hard-coded fallback.
func detectUserAgent() string {
	ver := "2.1.205"
	if v := claudeVersion(); v != "" {
		ver = v
	}
	return "claude-code/" + ver
}

func claudeVersion() string {
	candidates := []string{"claude"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			"/opt/homebrew/bin/claude",
			"/usr/local/bin/claude",
			filepath.Join(home, ".claude", "local", "claude"),
			filepath.Join(home, ".local", "bin", "claude"),
		)
	}
	for _, c := range candidates {
		out, err := exec.Command(c, "--version").Output()
		if err != nil {
			continue
		}
		if f := strings.Fields(strings.TrimSpace(string(out))); len(f) > 0 && f[0] != "" {
			return f[0]
		}
	}
	return ""
}
