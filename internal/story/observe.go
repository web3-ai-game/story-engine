package story

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// PromptTokenBudget is the demo ceiling for one section prompt.
	PromptTokenBudget = 1800
	observeLead       = "Lin"
	observeTemplate   = "campus"
)

// ObserveRow is one section of the offline context walk.
type ObserveRow struct {
	Section        int
	RawSections    int
	SummaryCovers  int
	WindowSections int
	PromptTokens   int
	WithinBudget   bool
	Summary        string
}

// ObserveChapter walks 10 sections without a model call.
// Sections older than the rolling window are folded into a summary.
func ObserveChapter() []ObserveRow {
	var raw []string
	var summaryParts []string
	rows := make([]ObserveRow, 0, SectionsPerChapter)
	for section := 1; section <= SectionsPerChapter; section++ {
		covered := 0
		if section > rollingWindow+1 {
			covered = section - 1 - rollingWindow
			summaryParts = raw[:covered]
		}
		window := section - 1
		if window > rollingWindow {
			window = rollingWindow
		}
		system := observeSystem(summaryParts)
		prompt := system + "\n" + strings.Repeat("recent section text. ", window) + "\nuser: continue"
		tokens := estimateTokens(prompt)
		rows = append(rows, ObserveRow{
			Section:        section,
			RawSections:    section - 1,
			SummaryCovers:  covered,
			WindowSections: window,
			PromptTokens:   tokens,
			WithinBudget:   tokens <= PromptTokenBudget,
			Summary:        clipSummary(strings.Join(summaryParts, " ")),
		})
		raw = append(raw, fmt.Sprintf("section %d scene", section))
	}
	return rows
}

func observeSystem(summaryParts []string) string {
	base := "system: campus template. lead=" + observeLead + ". keep the latest 3 sections."
	if len(summaryParts) == 0 {
		return base
	}
	return base + " summary: " + strings.Join(summaryParts, "; ")
}

func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 1
	}
	tokens := n / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func clipSummary(s string) string {
	if s == "" {
		return "-"
	}
	if len(s) > 72 {
		return s[:72]
	}
	return s
}
