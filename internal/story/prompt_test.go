package story

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_AllTemplates(t *testing.T) {
	cases := []struct {
		tpl       string
		lead      string
		mustHave  []string
		mustNot   []string
	}{
		{
		tpl: "campus", lead: "夏晚棠",
		mustHave: []string{"夏晚棠", "陆辰逸", "林屿阳", "谢九霄", "1000-2000字"},
		mustNot:  []string{"顾霆", "玄清"},
	},
		{
			tpl: "ceo", lead: "林晚",
			mustHave: []string{"林晚", "顾霆", "沈越", "契约", "1000-2000字"},
			mustNot:  []string{"陆辰逸", "玄清", "校园"},
		},
		{
			tpl: "xianxia", lead: "苏璃",
			mustHave: []string{"苏璃", "玄清", "洛尘", "夜冥", "灵力", "1000-2000字"},
			mustNot:  []string{"顾霆", "陆辰逸", "契约"},
		},
	}
	for _, c := range cases {
		got, err := BuildSystemPrompt(c.tpl, c.lead, 1, 1)
		if err != nil {
			t.Errorf("%s: %v", c.tpl, err)
			continue
		}
		for _, s := range c.mustHave {
			if !strings.Contains(got, s) {
				t.Errorf("%s missing %q", c.tpl, s)
			}
		}
		for _, s := range c.mustNot {
			if strings.Contains(got, s) {
				t.Errorf("%s should not contain %q", c.tpl, s)
			}
		}
	}
}

func TestBuildFirstSectionPrompt_PerTemplate(t *testing.T) {
	cases := []struct {
		tpl      string
		lead     string
		mustHave []string
	}{
		{"campus", "夏晚棠", []string{"夏晚棠", "校园"}},
		{"ceo", "林晚", []string{"林晚", "契约", "顾霆"}},
		{"xianxia", "苏璃", []string{"苏璃", "玄清", "灵力"}},
	}
	for _, c := range cases {
		got, err := BuildFirstSectionPrompt(c.tpl, c.lead)
		if err != nil {
			t.Errorf("%s: %v", c.tpl, err)
			continue
		}
		for _, s := range c.mustHave {
			if !strings.Contains(got, s) {
				t.Errorf("%s first-prompt missing %q\n%s", c.tpl, s, got)
			}
		}
	}
}

func TestChapterMetadata(t *testing.T) {
	name, ok := ChapterName("ceo", 1)
	if !ok || !strings.Contains(name, "契约") {
		t.Errorf("ceo ch1 name = %q ok=%v", name, ok)
	}
	num, _ := ChapterNum("xianxia", 3)
	if num != "三" {
		t.Errorf("xianxia ch3 num = %q", num)
	}
	if MaxChapters("campus") != 5 {
		t.Errorf("campus max chapters != 5")
	}
	if _, err := BuildSystemPrompt("nope", "x", 1, 1); err == nil {
		t.Error("unknown template should return error")
	}
}
