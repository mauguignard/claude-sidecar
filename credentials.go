package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// credentials mirrors Claude Code's stored shape, both in ~/.claude/.credentials.json and the macOS Keychain value.
type credentials struct {
	ClaudeAiOauth struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"` // unix millis
	} `json:"claudeAiOauth"`
}

// loadToken reads the token fresh each call so a refresh by an active Claude Code session is picked up on the next poll.
func loadToken() (token string, expiresAt time.Time, err error) {
	if c, e := tokenFromFile(); e == nil && c.ClaudeAiOauth.AccessToken != "" {
		return c.ClaudeAiOauth.AccessToken, millisToTime(c.ClaudeAiOauth.ExpiresAt), nil
	}
	if c, e := tokenFromKeychain(); e == nil && c.ClaudeAiOauth.AccessToken != "" {
		return c.ClaudeAiOauth.AccessToken, millisToTime(c.ClaudeAiOauth.ExpiresAt), nil
	}
	return "", time.Time{}, fmt.Errorf("no Claude Code OAuth token found (checked ~/.claude/.credentials.json and Keychain)")
}

func tokenFromFile() (credentials, error) {
	var c credentials
	home, err := os.UserHomeDir()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func tokenFromKeychain() (credentials, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return credentials{}, err
	}
	return parseKeychainBlob(string(out)), nil
}

// parseKeychainBlob accepts either the JSON credentials blob Claude Code normally
// stores or, on some versions, the bare access token as a raw string.
func parseKeychainBlob(out string) credentials {
	var c credentials
	blob := strings.TrimSpace(out)
	if err := json.Unmarshal([]byte(blob), &c); err != nil {
		c.ClaudeAiOauth.AccessToken = blob
	}
	return c
}

func millisToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
