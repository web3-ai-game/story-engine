package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const chatCompletionsURL = "https://openrouter.ai/api/v1/chat/completions"

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewClientWithTransport 創建帶自定義 RoundTripper 的 client。
// 主要用於測試（httptest server 轉發）；生產代碼應使用 NewClient。
func NewClientWithTransport(apiKey string, transport http.RoundTripper) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		},
	}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions 控制生成参数。零值字段走 OpenRouter 默认。
type ChatOptions struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	MaxTokens        *int     `json:"max_tokens,omitempty"`
}

type chatRequest struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	Temperature      *float64  `json:"temperature,omitempty"`
	TopP             *float64  `json:"top_p,omitempty"`
	FrequencyPenalty *float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64  `json:"presence_penalty,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
}

type streamRequest struct {
	Model            string    `json:"model"`
	Messages         []Message `json:"messages"`
	Temperature      *float64  `json:"temperature,omitempty"`
	TopP             *float64  `json:"top_p,omitempty"`
	FrequencyPenalty *float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64  `json:"presence_penalty,omitempty"`
	MaxTokens        *int      `json:"max_tokens,omitempty"`
	Stream           bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// ChatCompletion 调用 OpenRouter chat completions 接口，返回首个 choice 的文本内容。
// opts 为 nil 时使用合理默认（temperature=0.85, max_tokens 不限）。
func (c *Client) ChatCompletion(model string, messages []Message, opts *ChatOptions) (string, error) {
	if opts == nil {
		opts = &ChatOptions{Temperature: float64Ptr(0.85)}
	}
	body, err := json.Marshal(chatRequest{
		Model:            model,
		Messages:         messages,
		Temperature:      opts.Temperature,
		TopP:             opts.TopP,
		FrequencyPenalty: opts.FrequencyPenalty,
		PresencePenalty:  opts.PresencePenalty,
		MaxTokens:        opts.MaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, chatCompletionsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if cr.Error != nil {
		return "", fmt.Errorf("openrouter error %d: %s", cr.Error.Code, cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, raw)
	}
	if len(cr.Choices) == 0 || cr.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response from model")
	}

	return cr.Choices[0].Message.Content, nil
}

// StreamChatCompletion streams the chat response via SSE.
// onChunk is called with the raw SSE data line for each chunk.
// Returns the full accumulated text and any error.
// maxTokens 为 nil 时不限制输出长度。
func (c *Client) StreamChatCompletion(ctx context.Context, model string, messages []Message, opts *ChatOptions, onChunk func(rawData string)) (string, error) {
	if opts == nil {
		opts = &ChatOptions{Temperature: float64Ptr(0.85)}
	}
	body, err := json.Marshal(streamRequest{
		Model:            model,
		Messages:         messages,
		Temperature:      opts.Temperature,
		TopP:             opts.TopP,
		FrequencyPenalty: opts.FrequencyPenalty,
		PresencePenalty:  opts.PresencePenalty,
		MaxTokens:        opts.MaxTokens,
		Stream:           true,
	})
	if err != nil {
		return "", fmt.Errorf("marshal stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	// Use a longer-timeout transport for streaming
	streamClient := &http.Client{Timeout: 2 * time.Minute}
	resp, err := streamClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("stream http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("stream http %d: %s", resp.StatusCode, raw)
	}

	var fullText strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// Extract delta content for accumulation
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err == nil {
			if len(chunk.Choices) > 0 {
				fullText.WriteString(chunk.Choices[0].Delta.Content)
			}
		}

		if onChunk != nil {
			onChunk(data)
		}
	}

	if err := scanner.Err(); err != nil {
		return fullText.String(), fmt.Errorf("stream scan: %w", err)
	}
	return fullText.String(), nil
}

func float64Ptr(f float64) *float64 { return &f }
func intPtr(n int) *int             { return &n }
