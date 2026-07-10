package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const usageURL = "https://api.anthropic.com/api/oauth/usage"

type window struct {
	Utilization float64   `json:"utilization"` // percent used, 0-100
	ResetsAt    time.Time `json:"resets_at"`   // RFC3339
}

// usageResponse is the (undocumented) shape of GET /api/oauth/usage.
type usageResponse struct {
	FiveHour       window `json:"five_hour"`
	SevenDay       window `json:"seven_day"`
	SevenDaySonnet window `json:"seven_day_sonnet"`
}

type usageError struct{ status int }

func (e *usageError) Error() string {
	return fmt.Sprintf("usage endpoint returned HTTP %d", e.status)
}

// fetchUsage's User-Agent must look like Claude Code or the endpoint aggressively 429s.
func fetchUsage(ctx context.Context, client *http.Client, token, userAgent string) (*usageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &usageError{status: resp.StatusCode}
	}
	var u usageResponse
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}
	// A 200 missing the required windows decodes to zero-valued structs; reject it so we keep the last good reading.
	if u.FiveHour.ResetsAt.IsZero() || u.SevenDay.ResetsAt.IsZero() {
		return nil, fmt.Errorf("usage response missing five_hour/seven_day windows")
	}
	return &u, nil
}

func resetClock(t time.Time) string {
	if t.IsZero() {
		return "Reset time unknown"
	}
	return "Resets at " + t.Local().Format("3:04 PM")
}

func resetDate(t time.Time) string {
	if t.IsZero() {
		return "Reset time unknown"
	}
	return "Resets on " + t.Local().Format("2 Jan 2006 at 3:04 PM")
}
