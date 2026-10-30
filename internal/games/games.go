// Package games 提供 9 個輕量合規文字遊戲的 system prompt 與 HTTP 處理。
//
// 9 個遊戲：acrostic / dream / opening / namemaster / roast / resume /
// daily / brain / pickup — 全部合規、無 NSFW、單次或短多輪交互、純文字、
// 適配網頁端載入與操作。
//
// 設計：無狀態（stateless）。每次 /api/games/chat 請求都帶完整 messages 歷史，
// 服務端只負責組裝 system + history + user 三層並調用 LLM。
//
// 這樣設計的好處：
//   - 前端可自由決定何時切換 mode、reset context，不需後端額外狀態機
//   - 後端可橫向擴展（無 session 共享負擔）
//   - 易於單元測試：只需要構造 messages 數組就能驗證 prompt 拼接
package games

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"1001universe/internal/api"
)

// Game IDs — 與 main.go 的路由 / static/games/*.html 對齊
const (
	GameAcrostic    = "acrostic"    // 藏頭詩
	GameDream       = "dream"       // 周公解夢
	GameOpening     = "opening"     // 故事開頭
	GameNameMaster  = "namemaster"  // 命名師
	GameRoast       = "roast"       // 吐槽大會
	GameResume      = "resume"      // 簡歷潤色
	GameDaily       = "daily"       // 日簽文案
	GameBrain       = "brain"       // 腦筋急轉彎
	GamePickup      = "pickup"      // 高情商回覆
)

// MaxHistoryMsgs 限制單次請求攜帶的歷史消息數量，防止 token 爆炸。
const MaxHistoryMsgs = 20

// ModelForGames 文字遊戲用的主模型。固定為 grok-4.3：遊戲 prompt 設計依賴其文風。
const ModelForGames = "x-ai/grok-4.3"

// All 按穩定順序返回全部 9 個遊戲 ID。
func All() []string {
	return []string{"acrostic", "dream", "opening", "namemaster", "roast", "resume", "daily", "brain", "pickup"}
}

func float64Ptr(f float64) *float64 { return &f }

// IsValid 判斷 game ID 是否為已註冊遊戲。
func IsValid(game string) bool {
	for _, g := range All() {
		if g == game {
			return true
		}
	}
	return false
}

// Prompt 返回指定遊戲 + 可選 mode 的 system prompt。
// 未知遊戲返回通用 fallback。
func Prompt(game, mode string) string {
	switch game {
	case GameAcrostic:
		return acrosticPrompt
	case GameDream:
		return dreamPrompt
	case GameOpening:
		return openingPrompt
	case GameNameMaster:
		return namemasterPrompt
	case GameRoast:
		return roastPrompt
	case GameResume:
		return resumePrompt
	case GameDaily:
		return dailyPrompt
	case GameBrain:
		return brainPrompt
	case GamePickup:
		return pickupPrompt
	}
	return "你是一个有趣的AI助手，用中文回复。"
}

// ── HTTP handler ───────────────────────────────────────────────────────

// chatRequest 是 POST /api/games/chat 的請求體。
type chatRequest struct {
	Game     string        `json:"game"`
	Mode     string        `json:"mode"`
	Messages []api.Message `json:"messages"`
	Content  string        `json:"content"`
}

// ChatHandler 返回一個處理 /api/games/chat 的 http.HandlerFunc。
// 需要登入認證的路由組使用；handler 本身不做認證（authMiddleware 統一處理）。
func ChatHandler(client *api.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Game     string      `json:"game"`
			Mode     string      `json:"mode,omitempty"`
			Messages []api.Message `json:"messages"`
			Content  string      `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		if req.Game == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing game"})
			return
		}
		if req.Content == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty content"})
			return
		}
		msgs := buildMessages(req.Game, req.Mode, req.Messages, req.Content)
		maxT := 1000
		opts := &api.ChatOptions{
			Temperature: float64Ptr(0.95),
			MaxTokens:   &maxT,
		}
		result, err := client.ChatCompletion(ModelForGames, msgs, opts)
		if err != nil {
			slog.Error("games chat", "game", req.Game, "mode", req.Mode, "err", err)
			writeErr(w, http.StatusInternalServerError, "AI generation failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"content": result})
	}
}

// buildMessages 組裝 system + 截斷後的 history + 當前 user 訊息。
func buildMessages(game, mode string, history []api.Message, content string) []api.Message {
	msgs := make([]api.Message, 0, len(history)+2)
	msgs = append(msgs, api.Message{Role: "system", Content: Prompt(game, mode)})
	if len(history) > MaxHistoryMsgs {
		history = history[len(history)-MaxHistoryMsgs:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, api.Message{Role: "user", Content: content})
	return msgs
}

// ── helpers ───────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// 防止 fmt 在這個檔案被標 unused（將來可能要拼更多 debug log）
var _ = fmt.Sprintf