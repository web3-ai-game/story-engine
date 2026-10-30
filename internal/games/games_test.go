package games

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"1001universe/internal/api"
)

func TestAll_HasNineGames(t *testing.T) {
	got := All()
	if len(got) != 9 {
		t.Fatalf("All() = %d games, want 9: %v", len(got), got)
	}
}

func TestIsValid(t *testing.T) {
	for _, g := range All() {
		if !IsValid(g) {
			t.Errorf("IsValid(%q) = false", g)
		}
	}
	for _, g := range []string{"", "unknown", "ACROSTIC", "Acrostic"} {
		if IsValid(g) {
			t.Errorf("IsValid(%q) = true, want false", g)
		}
	}
}

func TestPrompt_KnownGames(t *testing.T) {
	cases := []struct {
		game     string
		mode     string
		mustHave []string
	}{
		{GameAcrostic, "", []string{"藏头诗", "七言"}},
		{GameDream, "", []string{"梦境", "心理学"}},
		{GameOpening, "", []string{"开篇", "600-900"}},
		{GameNameMaster, "", []string{"命名", "字义"}},
		{GameRoast, "", []string{"吐槽", "200-400"}},
		{GameResume, "", []string{"简历", "优化"}},
		{GameDaily, "", []string{"文案", "50-150"}},
		{GameBrain, "", []string{"脑筋急转弯", "脑洞"}},
		{GamePickup, "", []string{"回复", "高情商"}},
	}
	for _, c := range cases {
		got := Prompt(c.game, c.mode)
		if got == "" {
			t.Errorf("%s/%s prompt empty", c.game, c.mode)
			continue
		}
		for _, s := range c.mustHave {
			if !strings.Contains(got, s) {
				t.Errorf("%s/%s prompt missing %q", c.game, c.mode, s)
			}
		}
	}
}

func TestPrompt_UnknownFallsBack(t *testing.T) {
	got := Prompt("not-a-game", "")
	if !strings.Contains(got, "有趣的AI助手") {
		t.Errorf("unknown game should return fallback prompt, got %q", got[:60])
	}
}

// TestBuildMessages 驗證三層結構 + 歷史截斷。
func TestBuildMessages(t *testing.T) {
	hist := make([]api.Message, MaxHistoryMsgs+10)
	for i := range hist {
		hist[i] = api.Message{Role: "user", Content: "old"}
	}
	got := buildMessages(GameAcrostic, "", hist, "hi")
	if len(got) != MaxHistoryMsgs+2 {
		t.Errorf("len = %d, want %d (system + truncated history + user)", len(got), MaxHistoryMsgs+2)
	}
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "藏头诗") {
		t.Errorf("system prompt wrong: %+v", got[0])
	}
	if got[len(got)-1].Role != "user" || got[len(got)-1].Content != "hi" {
		t.Errorf("last msg wrong: %+v", got[len(got)-1])
	}
	// 截斷後應保留最新的 MaxHistoryMsgs 條
	if got[1].Content != "old" {
		t.Errorf("history should be oldest truncated, got[1]=%q", got[1].Content)
	}
}

func TestBuildMessages_NoTruncationWhenShort(t *testing.T) {
	hist := []api.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	got := buildMessages(GameAcrostic, "", hist, "c")
	if len(got) != 4 { // system + 2 history + user
		t.Errorf("len = %d, want 4", len(got))
	}
}

// TestChatHandler_Success 走 httptest server 驗證完整請求 → 回應路徑。
// 重點：驗證上游收到的 messages 結構（system 為首，user 在尾，model 正確）。
func TestChatHandler_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		if req.Model != ModelForGames {
			t.Errorf("upstream model = %q, want %q", req.Model, ModelForGames)
		}
		if len(req.Messages) < 2 {
			t.Fatalf("messages too short: %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content, "藏头诗") {
			t.Errorf("system prompt wrong: role=%q content-head=%q", req.Messages[0].Role, req.Messages[0].Content[:40])
		}
		if req.Messages[len(req.Messages)-1].Content != "夏晚棠" {
			t.Errorf("last user msg = %q, want 夏晚棠", req.Messages[len(req.Messages)-1].Content)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "夏日清辉映碧空"}},
			},
		})
	}))
	defer srv.Close()

	client := newRewrittenClient(srv.URL)
	h := ChatHandler(client)

	body := `{"game":"acrostic","mode":"","messages":[{"role":"user","content":"在吗"}],"content":"夏晚棠"}`
	r := httptest.NewRequest(http.MethodPost, "/api/games/chat", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["content"] != "夏日清辉映碧空" {
		t.Errorf("content = %q", resp["content"])
	}
}

func TestChatHandler_EmptyContent(t *testing.T) {
	client := newRewrittenClient("http://invalid")
	h := ChatHandler(client)

	body := `{"game":"acrostic","content":""}`
	r := httptest.NewRequest(http.MethodPost, "/api/games/chat", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestChatHandler_BadJSON(t *testing.T) {
	client := newRewrittenClient("http://invalid")
	h := ChatHandler(client)

	r := httptest.NewRequest(http.MethodPost, "/api/games/chat", strings.NewReader("not-json{"))
	w := httptest.NewRecorder()
	h(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = "http"
	r2.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(r2)
}

func newRewrittenClient(serverURL string) *api.Client {
	return api.NewClientWithTransport("test-key", rewriteTransport{target: serverURL})
}