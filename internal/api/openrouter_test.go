package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewClient_Basic 確保 client 初始化時 timeout 設定正確。
func TestNewClient_Basic(t *testing.T) {
	c := NewClient("test-key")
	if c == nil || c.apiKey != "test-key" {
		t.Fatal("client init failed")
	}
	if c.httpClient.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", c.httpClient.Timeout)
	}
}

// TestChatCompletion_Success 走完整 happy path：httptest server 回 200 + 合法 JSON。
func TestChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		// 確認 body 是合法 chatRequest
		var req chatRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("body unmarshal: %v", err)
		}
		if req.Model != "x-ai/grok-4.3" {
			t.Errorf("model = %q", req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Errorf("messages malformed: %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "你好世界"}, "finish_reason": "stop"},
			},
		})
	}))
	defer srv.Close()

	// 重寫 package-level URL 透過自定義 transport
	c := &Client{
		apiKey: "test-key",
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: rewriteTransport{target: srv.URL},
		},
	}

	got, err := c.ChatCompletion("x-ai/grok-4.3", []Message{{Role: "user", Content: "hi"}}, &ChatOptions{
		Temperature: float64PtrLocal(0.8),
		MaxTokens:   intPtrLocal(100),
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "你好世界" {
		t.Errorf("got = %q", got)
	}
}

// TestChatCompletion_HTTPError 驗證非 200 會返回錯誤（帶 body 內容）。
// 注意：server body 必須是合法 JSON 且不含 "error" 欄位，否則會走 decode/openrouter error 分支。
func TestChatCompletion_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	_, err := c.ChatCompletion("x-ai/grok-4.3", []Message{{Role: "user", Content: "x"}}, &ChatOptions{
		Temperature: float64PtrLocal(0.5),
		MaxTokens:   intPtrLocal(10),
	})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("err should mention status 429: %v", err)
	}
}

// TestChatCompletion_OpenRouterError 驗證 OpenRouter 結構化錯誤 (HTTP 200 但含 error 欄位)。
func TestChatCompletion_OpenRouterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": 401, "message": "invalid api key"},
		})
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	_, err := c.ChatCompletion("x-ai/grok-4.3", []Message{{Role: "user", Content: "x"}}, &ChatOptions{
		Temperature: float64PtrLocal(0.5),
		MaxTokens:   intPtrLocal(10),
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("err should mention upstream msg: %v", err)
	}
}

// TestChatCompletion_EmptyChoices 驗證 choices 為空 / 內容為空 → 報錯而非 panic。
func TestChatCompletion_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	_, err := c.ChatCompletion("x-ai/grok-4.3", []Message{{Role: "user", Content: "x"}}, &ChatOptions{
		Temperature: float64PtrLocal(0.5),
		MaxTokens:   intPtrLocal(10),
	})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("want empty-response error, got %v", err)
	}
}

func TestChatCompletion_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json{"))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL)
	_, err := c.ChatCompletion("x-ai/grok-4.3", []Message{{Role: "user", Content: "x"}}, &ChatOptions{
		Temperature: float64PtrLocal(0.5),
		MaxTokens:   intPtrLocal(10),
	})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("want decode error, got %v", err)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func float64PtrLocal(f float64) *float64 { return &f }
func intPtrLocal(n int) *int             { return &n }

// rewriteTransport 把請求重定向到 httptest server（生產代碼裡 chatCompletionsURL 是常量）。
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = "http"
	r2.URL.Host = strings.TrimPrefix(rt.target, "http://")
	return http.DefaultTransport.RoundTrip(r2)
}

func newTestClient(serverURL string) *Client {
	return &Client{
		apiKey: "test-key",
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: rewriteTransport{target: serverURL},
		},
	}
}