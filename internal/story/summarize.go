package story

import (
	"fmt"
	"log/slog"
	"strings"

	"1001universe/internal/api"
)

// generateSummary 取指定模板 + 章节的全部 assistant 消息，调 LLM 生成摘要并持久化。
// 由 GenerateSection 在章节结束时异步调用。
func (e *Engine) generateSummary(userID, templateID string, chapter int) error {
	allMsgs, err := e.store.GetMessagesWithTemplate(userID, templateID)
	if err != nil {
		return fmt.Errorf("get messages: %w", err)
	}

	var parts []string
	for _, m := range allMsgs {
		if m.Chapter == chapter && m.Role == "assistant" {
			parts = append(parts, m.Content)
		}
	}

	if len(parts) == 0 {
		slog.Warn("no assistant messages for chapter, skipping summary", "userID", userID, "template", templateID, "chapter", chapter)
		return nil
	}

	summaryPrompt, err := BuildSummaryPrompt(templateID)
	if err != nil {
		return fmt.Errorf("build summary prompt: %w", err)
	}
	content := summaryPrompt + "\n\n" + strings.Join(parts, "\n\n---\n\n")
	msgs := []api.Message{{Role: "user", Content: content}}

	summary, err := e.callWithFallback(ModelDefaultPrimary, msgs)
	if err != nil {
		return fmt.Errorf("summary llm call: %w", err)
	}

	if err := e.store.SaveChapterSummaryWithTemplate(userID, templateID, chapter, summary); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}

	slog.Info("chapter summary saved", "userID", userID, "template", templateID, "chapter", chapter, "len", len(summary))
	return nil
}
