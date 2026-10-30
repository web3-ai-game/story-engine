package story

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"1001universe/internal/api"
	"1001universe/internal/store"
	"1001universe/internal/story/templates"
)

const (
	// ModelDefaultPrimary 是当前默认文本生成模型（OpenRouter 上的 Grok 4.3 官方 ID）。
	ModelDefaultPrimary = "x-ai/grok-4.3"
	// ModelFallback 是任何主模型失败后的降级模型。
	ModelFallback = "anthropic/claude-haiku-4.5"
	// Temperature 控制生成随机性。
	Temperature = 0.85
	// MaxTokens 覆盖 1000-2000 字/小节的上限（含标点+格式损耗）。
	MaxTokens = 4000
	// SectionsPerChapter 每章固定小节数。
	SectionsPerChapter = 10
	// rollingWindow 当前章节保留的最近小节消息数。
	rollingWindow = 3
)

// Engine 是故事生成核心：组装 3 层上下文、调 LLM、保存消息与摘要、推进进度。
type Engine struct {
	store  *store.Store
	client *api.Client
}

func NewEngine(st *store.Store, cl *api.Client) *Engine {
	return &Engine{store: st, client: cl}
}

// GenerateSectionMeta 生成指定用户/模板的当前小节内容，同时返回该小节的 (chapter, section)
// 以及是否已完结 (next chapter 超过模板的 max chapters)。
// 返回的 chapter/section 是"刚刚生成的小节"，不是"下一节"，便于前端 badge 显示用户正在读的内容。
// - templateID: 模板 id（"campus"/"ceo"/"xianxia"）
// - userInput: 用户最新输入；为空时视为首次进入，使用模板的 FirstTmpl 触发开篇
// - model: 主模型 id；为空时使用 ModelDefaultPrimary
func (e *Engine) GenerateSectionMeta(userID, templateID, userInput, model string) (text string, chapter int, section int, ended bool, err error) {
	if templateID == "" {
		templateID = "campus"
	}
	if model == "" {
		model = ModelDefaultPrimary
	}

	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", 0, 0, false, fmt.Errorf("unknown template %q", templateID)
	}

	prog, err := e.store.GetUserProgress(userID, templateID)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("get progress: %w", err)
	}
	if prog == nil {
		return "", 0, 0, false, fmt.Errorf("no progress for user %q template %q", userID, templateID)
	}
	chapter, section = prog.CurrentChapter, prog.CurrentSection
	leadName := e.leadNameForUser(userID, tpl)

	msgs, err := e.buildContext(userID, templateID, leadName, chapter, section, userInput)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("build context: %w", err)
	}

	text, err = e.callWithFallback(model, msgs)
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("llm call: %w", err)
	}

	if userInput != "" {
		if err := e.store.SaveMessageWithTemplate(userID, templateID, "user", userInput, chapter, section); err != nil {
			return "", 0, 0, false, fmt.Errorf("save user message: %w", err)
		}
	}
	if err := e.store.SaveMessageWithTemplate(userID, templateID, "assistant", text, chapter, section); err != nil {
		return "", 0, 0, false, fmt.Errorf("save assistant message: %w", err)
	}

	nextChapter, nextSection := advanceProgress(chapter, section)
	if err := e.store.UpdateUserProgressTemplate(userID, templateID, nextChapter, nextSection); err != nil {
		return "", 0, 0, false, fmt.Errorf("update progress: %w", err)
	}

	ended = nextChapter > tpl.MaxChapters

	if nextChapter > chapter {
		go func() {
			if err := e.generateSummary(userID, templateID, chapter); err != nil {
				slog.Error("chapter summary failed", "userID", userID, "template", templateID, "chapter", chapter, "err", err)
			}
		}()
	}

	return text, chapter, section, ended, nil
}

// leadNameForUser 优先使用用户的 HeroineName（旧 hui/wen 已存），否则使用模板默认 LeadName。
func (e *Engine) leadNameForUser(userID string, tpl *templates.Template) string {
	u, err := e.store.GetUser(userID)
	if err == nil && u != nil && u.HeroineName != "" {
		return u.HeroineName
	}
	return tpl.LeadName
}

// buildContext 组装 3 层上下文：system prompt（含历史摘要）+ 滚动窗口消息 + 当前用户输入。
func (e *Engine) buildContext(userID, templateID, leadName string, chapter, section int, userInput string) ([]api.Message, error) {
	systemContent, err := BuildSystemPrompt(templateID, leadName, chapter, section)
	if err != nil {
		return nil, err
	}

	summaries, err := e.store.GetChapterSummariesWithTemplate(userID, templateID)
	if err != nil {
		return nil, fmt.Errorf("get summaries: %w", err)
	}
	if len(summaries) > 0 {
		var sb strings.Builder
		sb.WriteString(systemContent)
		sb.WriteString("\n\n## 往章摘要")
		for _, s := range summaries {
			fmt.Fprintf(&sb, "\n第%d章摘要：%s", s.Chapter, s.Summary)
		}
		systemContent = sb.String()
	}

	allMsgs, err := e.store.GetMessagesWithTemplate(userID, templateID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	result := []api.Message{{Role: "system", Content: systemContent}}
	for _, m := range rollingWindowMessages(allMsgs, chapter, section) {
		result = append(result, api.Message{Role: m.Role, Content: m.Content})
	}

	input := userInput
	if input == "" {
		input, err = BuildFirstSectionPrompt(templateID, leadName)
		if err != nil {
			return nil, fmt.Errorf("build first prompt: %w", err)
		}
	}
	result = append(result, api.Message{Role: "user", Content: input})

	return result, nil
}

// callWithFallback 先调主模型（参数传入），失败后等 1 秒用降级模型重试一次。
func (e *Engine) callWithFallback(primary string, msgs []api.Message) (string, error) {
	mt := MaxTokens
	opts := &api.ChatOptions{
		Temperature: float64Ptr(Temperature),
		MaxTokens:   &mt,
	}
	text, err := e.client.ChatCompletion(primary, msgs, opts)
	if err == nil {
		return text, nil
	}
	slog.Warn("primary model failed, switching to fallback", "model", primary, "err", err)
	time.Sleep(time.Second)
	return e.client.ChatCompletion(ModelFallback, msgs, opts)
}

func float64Ptr(f float64) *float64 { return &f }

// advanceProgress 返回生成完当前小节后的新进度。
func advanceProgress(chapter, section int) (nextChapter, nextSection int) {
	if section >= SectionsPerChapter {
		return chapter + 1, 1
	}
	return chapter, section + 1
}

// rollingWindowMessages 从所有消息中取当前章节最近 rollingWindow 小节的消息（按时间正序）。
func rollingWindowMessages(allMsgs []store.Message, chapter, currentSection int) []store.Message {
	var chapterMsgs []store.Message
	for _, m := range allMsgs {
		if m.Chapter == chapter && m.Section < currentSection {
			chapterMsgs = append(chapterMsgs, m)
		}
	}
	if len(chapterMsgs) == 0 {
		return nil
	}

	seen := make(map[int]bool)
	var sectionNums []int
	for _, m := range chapterMsgs {
		if !seen[m.Section] {
			seen[m.Section] = true
			sectionNums = append(sectionNums, m.Section)
		}
	}

	if len(sectionNums) > rollingWindow {
		sectionNums = sectionNums[len(sectionNums)-rollingWindow:]
	}
	keep := make(map[int]bool, len(sectionNums))
	for _, s := range sectionNums {
		keep[s] = true
	}

	var result []store.Message
	for _, m := range chapterMsgs {
		if keep[m.Section] {
			result = append(result, m)
		}
	}
	return result
}