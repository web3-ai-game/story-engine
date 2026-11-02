package story

import "testing"

func TestObserveKeepsBudget(t *testing.T) {
	rows := ObserveChapter()
	if len(rows) != 10 {
		t.Fatalf("want 10 sections, got %d", len(rows))
	}
	if rows[9].SummaryCovers != 6 {
		t.Fatalf("section 10 should fold the first 6 sections, got %d", rows[9].SummaryCovers)
	}
	if rows[9].WindowSections != 3 {
		t.Fatalf("window should stay at 3, got %d", rows[9].WindowSections)
	}
	for _, row := range rows {
		if !row.WithinBudget || row.PromptTokens > PromptTokenBudget {
			t.Fatalf("section %d exceeds budget: %+v", row.Section, row)
		}
	}
}
