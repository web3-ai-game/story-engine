package templates

import "testing"

// TestAllTemplatesRegistered 確保 main.go 裡的 availableTemplates 與 registry 完全對齊。
// 此測試防呆：新增/刪除模板後兩邊必須同步，否則模板會在前端不可見。
func TestAllTemplatesRegistered(t *testing.T) {
	want := []string{
		"campus", "ceo", "xianxia", "palace", "esports",
		"apocalypse", "ming", "wasteland", "academy",
	}
	got := AllList()
	if len(got) != len(want) {
		t.Fatalf("AllList returned %d templates, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("AllList[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestAllList_StableOrder(t *testing.T) {
	first := AllList()
	second := AllList()
	if len(first) != len(second) {
		t.Fatalf("length mismatch")
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("order changed at %d: %q vs %q", i, first[i].ID, second[i].ID)
		}
	}
}

func TestGet_KnownAndUnknown(t *testing.T) {
	campus := Get("campus")
	if campus == nil {
		t.Fatal("campus template missing")
	}
	if campus.MaxChapters != 5 {
		t.Errorf("campus MaxChapters = %d, want 5", campus.MaxChapters)
	}
	if campus.LeadName == "" {
		t.Error("campus LeadName empty")
	}
	if campus.DisplayName == "" {
		t.Error("campus DisplayName empty")
	}
	for i := 1; i <= 5; i++ {
		if campus.ChapterNames[i] == "" {
			t.Errorf("campus ChapterNames[%d] empty", i)
		}
	}
	if Get("nope-not-a-template") != nil {
		t.Error("unknown id should return nil")
	}
}

func TestIsValid(t *testing.T) {
	for _, id := range []string{"campus", "ceo", "xianxia", "palace", "esports", "apocalypse", "ming", "wasteland", "academy"} {
		if !IsValid(id) {
			t.Errorf("IsValid(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "unknown", "CAMPUS", "Campus"} {
		if IsValid(id) {
			t.Errorf("IsValid(%q) = true, want false", id)
		}
	}
}

func TestAllMapConsistency(t *testing.T) {
	// All() 應等於 AllList() 按 ID 索引
	all := All()
	list := AllList()
	if len(all) != len(list) {
		t.Fatalf("All()=%d vs AllList()=%d", len(all), len(list))
	}
	for _, tpl := range list {
		if got := all[tpl.ID]; got != tpl {
			t.Errorf("All()[%q] != AllList entry", tpl.ID)
		}
	}
}

// TestEveryTemplateHasRequiredFields 確保所有模板都填了必要欄位。
// 缺失任何欄位會在 chat handler 觸發 nil panic。
func TestEveryTemplateHasRequiredFields(t *testing.T) {
	for _, tpl := range AllList() {
		if tpl.ID == "" {
			t.Error("template with empty ID")
		}
		if tpl.DisplayName == "" {
			t.Errorf("%s: DisplayName empty", tpl.ID)
		}
		if tpl.LeadName == "" {
			t.Errorf("%s: LeadName empty", tpl.ID)
		}
		if tpl.MaxChapters != 5 {
			t.Errorf("%s: MaxChapters = %d, want 5", tpl.ID, tpl.MaxChapters)
		}
		if tpl.SystemTmpl == "" {
			t.Errorf("%s: SystemTmpl empty", tpl.ID)
		}
		if tpl.FirstTmpl == "" {
			t.Errorf("%s: FirstTmpl empty", tpl.ID)
		}
		if tpl.SummaryTmpl == "" {
			t.Errorf("%s: SummaryTmpl empty", tpl.ID)
		}
		if tpl.Personality == nil {
			t.Errorf("%s: Personality nil", tpl.ID)
		}
		for i := 1; i <= 5; i++ {
			if tpl.ChapterNames[i] == "" {
				t.Errorf("%s: ChapterNames[%d] empty", tpl.ID, i)
			}
			if tpl.ChNums[i] == "" {
				t.Errorf("%s: ChNums[%d] empty", tpl.ID, i)
			}
		}
	}
}