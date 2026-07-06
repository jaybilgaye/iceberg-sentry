package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jaybilgaye/iceberg-sentry/internal/health"
)

// SARIF 2.1.0 output. We model each health dimension whose Severity is
// worse than OK as a SARIF result. The driver rule list mirrors the full
// dimension catalogue so consumers like GitHub Code Scanning can display
// "passed" rules consistently across runs.

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Results     []sarifResult     `json:"results"`
	Invocations []sarifInvocation `json:"invocations,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Version        string      `json:"version"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	ShortDescription sarifText   `json:"shortDescription"`
	HelpURI          string      `json:"helpUri,omitempty"`
	DefaultConfig    sarifConfig `json:"defaultConfiguration"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifConfig struct {
	Level string `json:"level"`
}

type sarifResult struct {
	RuleID     string          `json:"ruleId"`
	Level      string          `json:"level"`
	Message    sarifText       `json:"message"`
	Locations  []sarifLocation `json:"locations"`
	Properties map[string]any  `json:"properties,omitempty"`
}

type sarifLocation struct {
	LogicalLocations []sarifLogicalLocation `json:"logicalLocations"`
}

type sarifLogicalLocation struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool `json:"executionSuccessful"`
}

func renderSARIF(w io.Writer, r health.Report) error {
	log := sarifLog{
		Schema:  "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "iceberg-sentry",
				InformationURI: "https://github.com/jaybilgaye/iceberg-sentry",
				Version:        "phase-2",
				Rules:          buildRuleCatalogue(r),
			}},
			Invocations: []sarifInvocation{{ExecutionSuccessful: true}},
			Results:     buildResults(r),
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func buildRuleCatalogue(r health.Report) []sarifRule {
	rules := make([]sarifRule, 0, len(r.Dimensions))
	for _, d := range r.Dimensions {
		rules = append(rules, sarifRule{
			ID:               "sentry/" + d.Name,
			Name:             d.Name,
			ShortDescription: sarifText{Text: fmt.Sprintf("Iceberg %s health dimension", d.Name)},
			DefaultConfig:    sarifConfig{Level: "warning"},
		})
	}
	return rules
}

func buildResults(r health.Report) []sarifResult {
	out := make([]sarifResult, 0, len(r.Dimensions))
	for _, d := range r.Dimensions {
		if d.Severity == health.SeverityOK {
			continue
		}
		out = append(out, sarifResult{
			RuleID:  "sentry/" + d.Name,
			Level:   sarifLevel(d.Severity),
			Message: sarifText{Text: dimensionMessage(d)},
			Locations: []sarifLocation{{
				LogicalLocations: []sarifLogicalLocation{{
					Name: r.TableID,
					Kind: "iceberg-table",
				}},
			}},
			Properties: map[string]any{
				"score":     d.Score,
				"max_score": d.MaxScore,
				"severity":  string(d.Severity),
			},
		})
	}
	return out
}

func sarifLevel(s health.Severity) string {
	switch s {
	case health.SeverityCritical:
		return "error"
	case health.SeverityWarning:
		return "warning"
	case health.SeverityInfo:
		return "note"
	}
	return "none"
}

func dimensionMessage(d health.Dimension) string {
	if d.Remediation != "" {
		return fmt.Sprintf("%s: %s — %s", d.Name, d.Summary, d.Remediation)
	}
	return fmt.Sprintf("%s: %s", d.Name, d.Summary)
}
