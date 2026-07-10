BINARY = claude-sidecar
APP    = build/ClaudeSidecar.app

.PHONY: build app run once status install-agent uninstall-agent install-sleepd uninstall-sleepd install-hooks uninstall-hooks uninstall-all clean

build:
	CGO_ENABLED=1 go build -o $(BINARY) .

# Bundle into a proper .app so it launches from Finder with no Dock icon (LSUIElement).
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp $(BINARY) $(APP)/Contents/MacOS/ClaudeSidecar
	cp resources/Info.plist $(APP)/Contents/Info.plist
	cp resources/icon.png $(APP)/Contents/Resources/icon.png
	@echo "Built $(APP). Open it with: open $(APP)"

run: build
	./$(BINARY)

# Print the raw usage JSON once and exit (no menu bar). Handy for debugging.
once: build
	./$(BINARY) --once

status: build
	./$(BINARY) --status

# Launch at login via a per-user LaunchAgent.
install-agent: app
	mkdir -p $(HOME)/Applications
	rm -rf $(HOME)/Applications/ClaudeSidecar.app
	cp -R $(APP) $(HOME)/Applications/ClaudeSidecar.app
	mkdir -p $(HOME)/Library/LaunchAgents
	sed "s|@APP@|$(HOME)/Applications/ClaudeSidecar.app/Contents/MacOS/ClaudeSidecar|" \
		resources/io.github.mauguignard.claudesidecar.plist > $(HOME)/Library/LaunchAgents/io.github.mauguignard.claudesidecar.plist
	launchctl unload $(HOME)/Library/LaunchAgents/io.github.mauguignard.claudesidecar.plist 2>/dev/null || true
	launchctl load $(HOME)/Library/LaunchAgents/io.github.mauguignard.claudesidecar.plist
	@echo "Installed and loaded LaunchAgent."

uninstall-agent:
	launchctl unload $(HOME)/Library/LaunchAgents/io.github.mauguignard.claudesidecar.plist 2>/dev/null || true
	rm -f $(HOME)/Library/LaunchAgents/io.github.mauguignard.claudesidecar.plist
	rm -rf $(HOME)/Applications/ClaudeSidecar.app
	@echo "Uninstalled LaunchAgent."

# Full setup order: make install-agent install-sleepd install-hooks
# (hooks reference the installed app binary path, so install-agent must run first).

# Root LaunchDaemon that enforces `pmset -a disablesleep` based on the app's
# heartbeat file. Requires sudo: run as `sudo make install-sleepd`.
install-sleepd:
	mkdir -p /usr/local/libexec
	install -o root -g wheel -m 755 resources/claude-sidecar-sleepd.sh /usr/local/libexec/claude-sidecar-sleepd.sh
	install -o root -g wheel -m 644 resources/io.github.mauguignard.claudesidecar.sleepd.plist /Library/LaunchDaemons/io.github.mauguignard.claudesidecar.sleepd.plist
	launchctl bootout system/io.github.mauguignard.claudesidecar.sleepd 2>/dev/null || true
	launchctl bootstrap system /Library/LaunchDaemons/io.github.mauguignard.claudesidecar.sleepd.plist
	@echo "Installed and bootstrapped sleepd LaunchDaemon."

# Requires sudo: run as `sudo make uninstall-sleepd`.
uninstall-sleepd:
	launchctl bootout system/io.github.mauguignard.claudesidecar.sleepd 2>/dev/null || true
	rm -f /Library/LaunchDaemons/io.github.mauguignard.claudesidecar.sleepd.plist
	rm -f /usr/local/libexec/claude-sidecar-sleepd.sh
	pmset -a disablesleep 0
	@echo "Uninstalled sleepd LaunchDaemon and restored normal sleep."

# Merge Claude Code hook entries into ~/.claude/settings.json. Refuses if the
# installed app binary (install-agent) is not present yet.
install-hooks: build
	./$(BINARY) --install-hooks

uninstall-hooks: build
	./$(BINARY) --uninstall-hooks

# Full teardown in the safe order: hooks first (so settings.json never points
# at a deleted binary), then the sleepd daemon, then the LaunchAgent bundle.
uninstall-all: uninstall-hooks uninstall-sleepd uninstall-agent

clean:
	rm -f $(BINARY)
	rm -rf $(APP)
