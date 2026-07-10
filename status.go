package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// statusURL is the Statuspage v2 summary for Anthropic's services.
// status.anthropic.com 302s here, so hit the canonical host directly.
const statusURL = "https://status.claude.com/api/v2/summary.json"

// statusPageURL is what the status row opens when clicked.
const statusPageURL = "https://status.claude.com"

// statusInterval is slow: service status changes far more slowly than usage.
const statusInterval = 5 * time.Minute

// Statuspage indicators, worst last.
const (
	indicatorNone     = "none"
	indicatorMinor    = "minor"
	indicatorMajor    = "major"
	indicatorCritical = "critical"
)

type statusComponent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // operational | degraded_performance | partial_outage | major_outage | under_maintenance
}

type statusIncident struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"` // investigating | identified | monitoring | resolved | postmortem
	Components []statusComponent `json:"components"`
}

// serviceStatus is the slice of GET /api/v2/summary.json this bar renders.
type serviceStatus struct {
	Status struct {
		Indicator   string `json:"indicator"`
		Description string `json:"description"`
	} `json:"status"`
	Components []statusComponent `json:"components"`
	Incidents  []statusIncident  `json:"incidents"`
}

func fetchStatus(ctx context.Context, client *http.Client) (*serviceStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status endpoint returned HTTP %d", resp.StatusCode)
	}
	var s serviceStatus
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode status response: %w", err)
	}
	if s.Status.Indicator == "" {
		return nil, fmt.Errorf("status response missing status.indicator")
	}
	return &s, nil
}

// tracked excludes "Claude for Government" so a Gov-only outage doesn't light
// the bar for people who can't reach that endpoint.
func tracked(name string) bool { return !strings.Contains(name, "Government") }

// severity ranks a component status so the worst tracked component wins.
func severity(componentStatus string) int {
	switch componentStatus {
	case "major_outage":
		return 3
	case "partial_outage":
		return 2
	case "degraded_performance", "under_maintenance":
		return 1
	default: // operational, and anything Statuspage adds later
		return 0
	}
}

func indicatorFor(sev int) string {
	switch sev {
	case 3:
		return indicatorCritical
	case 2:
		return indicatorMajor
	case 1:
		return indicatorMinor
	default:
		return indicatorNone
	}
}

// effective ranks only tracked components; the page-level status.indicator
// covers every component, including ones we ignore.
func (s *serviceStatus) effective() string {
	worst := 0
	for _, c := range s.Components {
		if tracked(c.Name) && severity(c.Status) > worst {
			worst = severity(c.Status)
		}
	}
	return indicatorFor(worst)
}

// affected lists the tracked components that are not operational.
func (s *serviceStatus) affected() []statusComponent {
	var out []statusComponent
	for _, c := range s.Components {
		if tracked(c.Name) && severity(c.Status) > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return severity(out[i].Status) > severity(out[j].Status) })
	return out
}

// activeIncidents lists unresolved incidents touching a tracked component.
func (s *serviceStatus) activeIncidents() []statusIncident {
	var out []statusIncident
	for _, in := range s.Incidents {
		if in.Status == "resolved" || in.Status == "postmortem" {
			continue
		}
		for _, c := range in.Components {
			if tracked(c.Name) {
				out = append(out, in)
				break
			}
		}
	}
	return out
}

// statusHeadline is the row title: a summary of the tracked components.
func (s *serviceStatus) headline() string {
	if s.effective() == indicatorNone {
		return "All Claude services operational"
	}
	if in := s.activeIncidents(); len(in) > 0 {
		return in[0].Name
	}
	if d := s.Status.Description; d != "" {
		return d
	}
	return "Claude services degraded"
}

// statusDetail is the muted second line: what is broken, or when we last looked.
func (s *serviceStatus) detail(at time.Time) string {
	aff := s.affected()
	if len(aff) == 0 {
		return "Checked at " + at.Local().Format("3:04 PM")
	}
	parts := make([]string, 0, len(aff))
	for _, c := range aff {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.Name, componentLabel(c.Status)))
	}
	return "Affects: " + strings.Join(parts, ", ")
}

func componentLabel(componentStatus string) string {
	switch componentStatus {
	case "degraded_performance":
		return "degraded"
	case "partial_outage":
		return "partial outage"
	case "major_outage":
		return "major outage"
	case "under_maintenance":
		return "maintenance"
	default:
		return componentStatus
	}
}
