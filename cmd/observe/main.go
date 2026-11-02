// Offline context walk. No OPENROUTER_API_KEY required.
package main

import (
	"fmt"

	"1001universe/internal/story"
)

func main() {
	fmt.Println("story-engine observe")
	fmt.Println("provider: FallbackMockLLM")
	fmt.Println("key: none")
	fmt.Printf("budget: %d estimated tokens\n", story.PromptTokenBudget)
	fmt.Println("rule: chapter keeps 10 sections; the prompt keeps the latest 3; older sections fold into Summary")
	fmt.Println()
	fmt.Printf("%-7s %-10s %-10s %-10s %-8s %s\n", "section", "raw", "summary", "window", "tokens", "curve")
	max := 1
	rows := story.ObserveChapter()
	for _, row := range rows {
		if row.PromptTokens > max {
			max = row.PromptTokens
		}
	}
	for _, row := range rows {
		bar := bar(row.PromptTokens, max, 24)
		fmt.Printf("%-7d %-10d %-10d %-10d %-8d %s\n", row.Section, row.RawSections, row.SummaryCovers, row.WindowSections, row.PromptTokens, bar)
	}
	fmt.Println()
	fmt.Println("section 10 summary covers the first 6 sections. sections 7-9 stay in the window.")
	fmt.Println("the prompt does not resend the raw chapter.")
}

func bar(n, max, width int) string {
	if max <= 0 {
		return ""
	}
	fill := n * width / max
	if fill < 1 {
		fill = 1
	}
	out := make([]byte, width)
	for i := 0; i < width; i++ {
		if i < fill {
			out[i] = '#'
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}
