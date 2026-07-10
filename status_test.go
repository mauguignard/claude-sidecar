package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// summary.json trimmed to the fields this bar reads, with an incident added (the live endpoint rarely reports any).
const summaryFixture = `{
  "status": {"indicator": "major", "description": "Partial System Outage"},
  "components": [
    {"id": "rwppv331jlwc", "name": "claude.ai", "status": "operational"},
    {"id": "yyzkbfz2thpt", "name": "Claude Code", "status": "partial_outage"},
    {"id": "k8w3r06qmzrp", "name": "Claude API (api.anthropic.com)", "status": "degraded_performance"},
    {"id": "0scnb50nvy53", "name": "Claude for Government", "status": "major_outage"}
  ],
  "incidents": [
    {"id": "abc", "name": "Elevated errors on Claude Code", "status": "investigating",
     "components": [{"id": "yyzkbfz2thpt", "name": "Claude Code", "status": "partial_outage"}]},
    {"id": "def", "name": "Old thing", "status": "resolved",
     "components": [{"id": "rwppv331jlwc", "name": "claude.ai", "status": "operational"}]},
    {"id": "ghi", "name": "Gov-only maintenance", "status": "monitoring",
     "components": [{"id": "0scnb50nvy53", "name": "Claude for Government", "status": "major_outage"}]}
  ]
}`

func fetchFixture(t *testing.T, body string, code int) (*serviceStatus, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// fetchStatus pins statusURL, so rewrite the host onto the test server.
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &rewriteTransport{target}}
	return fetchStatus(context.Background(), client)
}

type rewriteTransport struct{ target *url.URL }

func (rt *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme, r.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestEffectiveIgnoresGovernment(t *testing.T) {
	s, err := fetchFixture(t, summaryFixture, http.StatusOK)
	if err != nil {
		t.Fatalf("fetchStatus: %v", err)
	}
	// Government is major_outage but untracked; the worst tracked component is Claude Code's partial_outage.
	if got := s.effective(); got != indicatorMajor {
		t.Errorf("effective() = %q, want %q", got, indicatorMajor)
	}
	if s.Status.Indicator != "major" {
		t.Errorf("page indicator not preserved: %q", s.Status.Indicator)
	}
}

func TestAffectedSortsWorstFirstAndSkipsGovernment(t *testing.T) {
	s, _ := fetchFixture(t, summaryFixture, http.StatusOK)
	aff := s.affected()
	if len(aff) != 2 {
		t.Fatalf("affected() = %d components, want 2: %+v", len(aff), aff)
	}
	if aff[0].Name != "Claude Code" {
		t.Errorf("worst-first ordering broken: %q leads", aff[0].Name)
	}
	for _, c := range aff {
		if c.Name == "Claude for Government" {
			t.Error("untracked component leaked into affected()")
		}
	}
}

func TestActiveIncidentsFiltersResolvedAndUntracked(t *testing.T) {
	s, _ := fetchFixture(t, summaryFixture, http.StatusOK)
	in := s.activeIncidents()
	if len(in) != 1 || in[0].ID != "abc" {
		t.Fatalf("activeIncidents() = %+v, want only incident abc", in)
	}
}

func TestHeadlineAndDetail(t *testing.T) {
	s, _ := fetchFixture(t, summaryFixture, http.StatusOK)
	if got, want := s.headline(), "Elevated errors on Claude Code"; got != want {
		t.Errorf("headline() = %q, want %q", got, want)
	}
	want := "Affects: Claude Code (partial outage), Claude API (api.anthropic.com) (degraded)"
	if got := s.detail(time.Now()); got != want {
		t.Errorf("detail() = %q, want %q", got, want)
	}
}

func TestHealthy(t *testing.T) {
	body := `{"status":{"indicator":"none","description":"All Systems Operational"},
	          "components":[{"id":"a","name":"Claude Code","status":"operational"}],"incidents":[]}`
	s, err := fetchFixture(t, body, http.StatusOK)
	if err != nil {
		t.Fatalf("fetchStatus: %v", err)
	}
	if got := s.effective(); got != indicatorNone {
		t.Errorf("effective() = %q, want none", got)
	}
	if got, want := s.headline(), "All Claude services operational"; got != want {
		t.Errorf("headline() = %q, want %q", got, want)
	}
	at := time.Date(2026, 7, 9, 15, 4, 0, 0, time.Local)
	if got, want := s.detail(at), "Checked at 3:04 PM"; got != want {
		t.Errorf("detail() = %q, want %q", got, want)
	}
}

// The row colour tracks components, not the page indicator, so an incident with no component attached is not an outage.
func TestPageIndicatorAloneDoesNotTripTheDot(t *testing.T) {
	body := `{"status":{"indicator":"minor","description":"Minor Service Outage"},
	          "components":[{"id":"a","name":"Claude Code","status":"operational"}],
	          "incidents":[{"id":"x","name":"Something","status":"investigating","components":[]}]}`
	s, _ := fetchFixture(t, body, http.StatusOK)
	if got := s.effective(); got != indicatorNone {
		t.Errorf("effective() = %q, want none", got)
	}
	if got, want := s.headline(), "All Claude services operational"; got != want {
		t.Errorf("headline() = %q, want %q", got, want)
	}
}

func TestErrors(t *testing.T) {
	if _, err := fetchFixture(t, "", http.StatusServiceUnavailable); err == nil {
		t.Error("HTTP 503 should be an error")
	}
	if _, err := fetchFixture(t, "not json", http.StatusOK); err == nil {
		t.Error("malformed body should be an error")
	}
	if _, err := fetchFixture(t, `{"components":[]}`, http.StatusOK); err == nil {
		t.Error("missing status.indicator should be an error")
	}
}
