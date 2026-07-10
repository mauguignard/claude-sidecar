package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// usageFixture is a well-formed /api/oauth/usage body with all three windows.
const usageFixture = `{
  "five_hour":        {"utilization": 23, "resets_at": "2026-01-31T07:59:00Z"},
  "seven_day":        {"utilization": 41, "resets_at": "2026-02-05T07:59:00Z"},
  "seven_day_sonnet": {"utilization": 12, "resets_at": "2026-02-05T07:59:00Z"}
}`

// fetchUsageFixture points fetchUsage at a throwaway server serving body/code,
// and captures the outgoing request so header assertions are possible.
func fetchUsageFixture(t *testing.T, body string, code int) (*usageResponse, *http.Request, error) {
	t.Helper()
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &rewriteTransport{target}}
	u, err := fetchUsage(context.Background(), client, "tok-123", "claude-code/9.9.9")
	return u, got, err
}

func TestFetchUsageHappyPath(t *testing.T) {
	u, req, err := fetchUsageFixture(t, usageFixture, http.StatusOK)
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if u.FiveHour.Utilization != 23 || u.SevenDay.Utilization != 41 || u.SevenDaySonnet.Utilization != 12 {
		t.Errorf("utilization decoded wrong: %+v", u)
	}
	if u.FiveHour.ResetsAt.IsZero() || u.SevenDay.ResetsAt.IsZero() {
		t.Errorf("resets_at not decoded: %+v", u)
	}

	// The endpoint 429s without a Claude-Code-shaped UA and a bearer token, so the request must carry both.
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok-123")
	}
	if got := req.Header.Get("User-Agent"); got != "claude-code/9.9.9" {
		t.Errorf("User-Agent = %q, want %q", got, "claude-code/9.9.9")
	}
	if got := req.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q, want %q", got, "oauth-2025-04-20")
	}
}

func TestFetchUsageNon200ReturnsUsageError(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		_, _, err := fetchUsageFixture(t, "", code)
		ue, ok := err.(*usageError)
		if !ok {
			t.Fatalf("code %d: error = %v (%T), want *usageError", code, err, err)
		}
		if ue.status != code {
			t.Errorf("code %d: usageError.status = %d", code, ue.status)
		}
	}
}

func TestFetchUsageMalformedBody(t *testing.T) {
	if _, _, err := fetchUsageFixture(t, "not json", http.StatusOK); err == nil {
		t.Error("malformed 200 body should be an error")
	}
}

// A 200 that decodes to zero-valued windows must be rejected so a garbage
// response can't overwrite the last good reading.
func TestFetchUsageRejectsMissingWindows(t *testing.T) {
	cases := map[string]string{
		"empty object":      `{}`,
		"only five_hour":    `{"five_hour": {"utilization": 5, "resets_at": "2026-01-31T07:59:00Z"}}`,
		"only seven_day":    `{"seven_day": {"utilization": 5, "resets_at": "2026-01-31T07:59:00Z"}}`,
		"windows w/o reset": `{"five_hour": {"utilization": 5}, "seven_day": {"utilization": 5}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := fetchUsageFixture(t, body, http.StatusOK); err == nil {
				t.Error("expected rejection of a 200 missing required windows")
			}
		})
	}
}

// The Sonnet window is optional; a valid response without it must still succeed.
func TestFetchUsageSonnetOptional(t *testing.T) {
	body := `{
	  "five_hour": {"utilization": 10, "resets_at": "2026-01-31T07:59:00Z"},
	  "seven_day": {"utilization": 20, "resets_at": "2026-02-05T07:59:00Z"}
	}`
	u, _, err := fetchUsageFixture(t, body, http.StatusOK)
	if err != nil {
		t.Fatalf("fetchUsage: %v", err)
	}
	if !u.SevenDaySonnet.ResetsAt.IsZero() || u.SevenDaySonnet.Utilization != 0 {
		t.Errorf("absent Sonnet window should be zero-valued, got %+v", u.SevenDaySonnet)
	}
}

func TestResetClock(t *testing.T) {
	if got := resetClock(time.Time{}); got != "Reset time unknown" {
		t.Errorf("resetClock(zero) = %q, want %q", got, "Reset time unknown")
	}
	// Built in Local and formatted in Local, so the wall-clock reads the same in any TZ.
	at := time.Date(2026, 1, 31, 7, 59, 0, 0, time.Local)
	if got, want := resetClock(at), "Resets at 7:59 AM"; got != want {
		t.Errorf("resetClock() = %q, want %q", got, want)
	}
}

func TestResetDate(t *testing.T) {
	if got := resetDate(time.Time{}); got != "Reset time unknown" {
		t.Errorf("resetDate(zero) = %q, want %q", got, "Reset time unknown")
	}
	at := time.Date(2026, 1, 31, 7, 59, 0, 0, time.Local)
	if got, want := resetDate(at), "Resets on 31 Jan 2026 at 7:59 AM"; got != want {
		t.Errorf("resetDate() = %q, want %q", got, want)
	}
}
