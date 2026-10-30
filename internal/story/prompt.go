package story

import (
	"bytes"
	"fmt"
	"text/template"

	"1001universe/internal/story/templates"
)

// BuildSystemPrompt 渲染指定模板的 system prompt。leadName 用于替换模板中的
// 女主角/主角名占位符；chapter/section 用于在 prompt 中标识当前进度。
// 末尾自动附加章节情绪指引，让 AI 在不同章节生成不同基调的内容。
func BuildSystemPrompt(templateID, leadName string, chapter, section int) (string, error) {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", fmt.Errorf("unknown template %q", templateID)
	}
	personality := tpl.Personality(leadName)
	t, err := template.New("system").Parse(tpl.SystemTmpl)
	if err != nil {
		return "", fmt.Errorf("parse system template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{
		"LeadName":    leadName,
		"Personality": personality,
		"Chapter":     chapter,
		"Section":     section,
	}); err != nil {
		return "", fmt.Errorf("execute system template: %w", err)
	}
	base := buf.String()
	if mood := chapterMoodGuide(chapter); mood != "" {
		base += "\n\n" + mood
	}
	return base, nil
}

// chapterMoodGuide 返回当前章节的情绪基调指引，追加到所有模板的 system prompt 末尾。
// 让 AI 在不同章节自动切换叙事重心，避免5章节奏感一样。
func chapterMoodGuide(chapter int) string {
	guides := [6]string{
		"",
		// 第一章
		"## 本节叙事基调（第一章·命运初见）\n初见即是命运——感官冲击优先，好奇与戒备并存。重点写男主对女主的「不受控反应」（不经意失神、话说到一半停住、身体比嘴诚实）。命运感要强：太多「恰好」，像整个世界在撮合。第一句话必须有冲击力——把读者直接丢进场景正中央。",
		// 第二章
		"## 本节叙事基调（第二章·日常升温）\n甜蜜渗进日常缝隙——重点写「暗恋的微小动作」：等在教室门口的那双鞋、不经意传过来的视线、三人争着帮她的暗战。让读者替女主心跳。每节要有一个「微甜钩子」收尾，吊着人想看下一节。不要让任何一章感觉平静——甜里要带一点刺。",
		// 第三章
		"## 本节叙事基调（第三章·暗涌与误会）\n情绪开始绷紧——嫉妒、误会、赌气，三者轮番出现。写「明明在乎却偏要逞强」的张力，至少一句台词让读者急着替女主解释。误会要有逻辑，不要无脑冲突。吃醋场景要克制，靠动作和微表情表达，不靠台词直说。",
		// 第四章
		"## 本节叙事基调（第四章·摊牌）\n情绪推到顶点——这一章是整个故事的高潮区。告白、质问、决裂，至少一个让人屏息的高潮时刻。可以有眼泪或愤怒，但不要小打小闹；要么不爆发，一爆就惊天动地。每节结尾要强，不留平淡的尾。",
		// 第五章
		"## 本节叙事基调（第五章·命运结局）\n命运感在这章达到顶峰——平行宇宙的选择与告别。有甜有虐，每节都要留下余韵，像整个宇宙在温柔地说再见。结局画面要有记忆点：不需要大团圆，但要让读者久久回味，像看完一部好电影走出影院时的那种感觉。",
	}
	if chapter < 1 || chapter > 5 {
		return ""
	}
	return guides[chapter]
}

// BuildFirstSectionPrompt 返回"开篇第一节"的种子用户输入。
// 当 userInput 为空时由 engine 层使用以触发首次自动生成。
func BuildFirstSectionPrompt(templateID, leadName string) (string, error) {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", fmt.Errorf("unknown template %q", templateID)
	}
	t, err := template.New("first").Parse(tpl.FirstTmpl)
	if err != nil {
		return "", fmt.Errorf("parse first template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]any{"LeadName": leadName}); err != nil {
		return "", fmt.Errorf("execute first template: %w", err)
	}
	return buf.String(), nil
}

// BuildSummaryPrompt 渲染章节摘要生成 prompt（基础部分，调用方在末尾追加章节内容）。
func BuildSummaryPrompt(templateID string) (string, error) {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", fmt.Errorf("unknown template %q", templateID)
	}
	return tpl.SummaryTmpl, nil
}

// ChapterName 返回指定模板第 N 章的中文名（N 必须在 1..5）。
func ChapterName(templateID string, chapter int) (string, bool) {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", false
	}
	if chapter < 1 || chapter > 5 {
		return "", false
	}
	return tpl.ChapterNames[chapter], true
}

// ChapterNum 返回指定模板第 N 章的中文数字（一/二/三/四/五）。
func ChapterNum(templateID string, chapter int) (string, bool) {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return "", false
	}
	if chapter < 1 || chapter > 5 {
		return "", false
	}
	return tpl.ChNums[chapter], true
}

// MaxChapters 返回模板的总章节数。
func MaxChapters(templateID string) int {
	tpl := templates.Get(templateID)
	if tpl == nil {
		return 5
	}
	return tpl.MaxChapters
}
