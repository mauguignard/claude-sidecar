package main

import (
	"testing"
	"time"
)

func TestParseKeychainBlobJSON(t *testing.T) {
	blob := `{"claudeAiOauth":{"accessToken":"tok-abc","refreshToken":"ref-xyz","expiresAt":1783695600000}}`
	c := parseKeychainBlob(blob)
	if c.ClaudeAiOauth.AccessToken != "tok-abc" {
		t.Errorf("accessToken = %q, want tok-abc", c.ClaudeAiOauth.AccessToken)
	}
	if c.ClaudeAiOauth.ExpiresAt != 1783695600000 {
		t.Errorf("expiresAt = %d, want 1783695600000", c.ClaudeAiOauth.ExpiresAt)
	}
}

// Some Keychain entries hold the bare token, not a JSON blob; it becomes the access token verbatim.
func TestParseKeychainBlobRawToken(t *testing.T) {
	c := parseKeychainBlob("  sk-ant-oat-raw-token\n")
	if c.ClaudeAiOauth.AccessToken != "sk-ant-oat-raw-token" {
		t.Errorf("accessToken = %q, want the trimmed raw token", c.ClaudeAiOauth.AccessToken)
	}
	if c.ClaudeAiOauth.RefreshToken != "" {
		t.Errorf("raw token must not populate refreshToken, got %q", c.ClaudeAiOauth.RefreshToken)
	}
}

func TestMillisToTime(t *testing.T) {
	if got := millisToTime(0); !got.IsZero() {
		t.Errorf("millisToTime(0) = %v, want zero time", got)
	}
	if got := millisToTime(-5); !got.IsZero() {
		t.Errorf("millisToTime(negative) = %v, want zero time", got)
	}
	if got := millisToTime(1783695600000); !got.Equal(time.UnixMilli(1783695600000)) {
		t.Errorf("millisToTime(positive) = %v, want the corresponding instant", got)
	}
}
