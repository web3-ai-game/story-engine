# story-engine

[繁體中文](#繁體中文) · [English](#english)

互動小說引擎。每個使用者、每個模板的進度分開存。公開樹不含頁面、封面、部署腳本和主機位址。

<a id="繁體中文"></a>

## 繁體中文

一章固定 10 節。提示詞只留最近 3 節，更早的內容收成 Summary。模板在 `internal/story/templates`，世界觀、人設和結局條件是資料。陪伴對話在 `internal/companion`：最近 30 輪進上下文，記憶每 5 輪更新，標題在第 4 輪生成。進度在 SQLite，同一個模板的不同使用者互不覆蓋。

### 沒有金鑰也能看壓縮

`FallbackMockLLM` 不打網路。它把引擎的窗口規則走一遍，印出第 1 到第 10 節的 token 曲線。需要 Go 1.25，跟 `go.mod` 和 `golang.org/x/crypto` v0.52.0 對齊。

```bash
go test ./internal/story
go run ./cmd/observe
```

2026-11-02 實跑。預算 1,800 個估算 token（字元數 / 4）。第 10 節的 Summary 蓋住前 6 節，窗口留下第 7–9 節。Gemini 口號裡的「前 7 節」和引擎不一致，這裡跟程式走。

| 節 | 已寫節數 | Summary 覆蓋 | 窗口 | 估算 token |
|---:|---:|---:|---:|---:|
| 1 | 0 | 0 | 0 | 19 |
| 2 | 1 | 0 | 1 | 24 |
| 3 | 2 | 0 | 2 | 30 |
| 4 | 3 | 0 | 3 | 35 |
| 5 | 4 | 1 | 3 | 41 |
| 6 | 5 | 2 | 3 | 45 |
| 7 | 6 | 3 | 3 | 50 |
| 8 | 7 | 4 | 3 | 54 |
| 9 | 8 | 5 | 3 | 58 |
| 10 | 9 | 6 | 3 | 62 |

十節都在預算內。曲線是平的，因為摘要是短句，不是把原文再送一次。

`go run .` 仍要 `OPENROUTER_API_KEY`。沒有金鑰時，生成路徑會失敗。看壓縮請用上面的 `cmd/observe`。

### 目錄

| 路徑 | 職責 |
|---|---|
| `internal/story/engine.go` | 組上下文、呼叫模型、推進章節 |
| `cmd/observe` | 無金鑰的 10 節壓縮曲線 |
| `internal/story/templates` | 模板資料與測試 |
| `internal/store` | 使用者與進度隔離 |
| `internal/companion` | 記憶、標題、備援 |
| `internal/api` | OpenRouter 客戶端 |
| `main.go` | 路由與嵌入的靜態入口 |

部署腳本、域名、資料庫、封面圖留在私有庫。授權是 [MIT](LICENSE)。

<a id="english"></a>

## English

Progress is isolated per user and per template. Pages, covers, deploy scripts, and the host address are not in this tree.

A chapter is 10 sections. The prompt keeps the latest 3. Older text is a summary. Templates live in `internal/story/templates`. Companion chat keeps the last 30 turns, refreshes memory every 5 turns, and titles the thread at turn 4. SQLite stores progress so two users in the same template do not overwrite each other.

### No-key compression walk

`FallbackMockLLM` does not open a network connection. It walks the engine window and prints the token curve for sections 1–10. Needs Go 1.25, matching `go.mod` and `golang.org/x/crypto` v0.52.0.

```bash
go test ./internal/story
go run ./cmd/observe
```

Measured on 2026-11-02. Budget is 1,800 estimated tokens (rune count / 4). At section 10 the summary covers the first 6 sections, and the window holds sections 7–9. That matches the engine. A “first 7 sections” slogan does not.

| Section | Raw | Summary covers | Window | Estimated tokens |
|---:|---:|---:|---:|---:|
| 1 | 0 | 0 | 0 | 19 |
| 2 | 1 | 0 | 1 | 24 |
| 3 | 2 | 0 | 2 | 30 |
| 4 | 3 | 0 | 3 | 35 |
| 5 | 4 | 1 | 3 | 41 |
| 6 | 5 | 2 | 3 | 45 |
| 7 | 6 | 3 | 3 | 50 |
| 8 | 7 | 4 | 3 | 54 |
| 9 | 8 | 5 | 3 | 58 |
| 10 | 9 | 6 | 3 | 62 |

All ten stay inside the budget. The curve is flat because the summary is a short line, not a resend of the raw chapter.

`go run .` still needs `OPENROUTER_API_KEY`. Without a key, generation fails. Use `cmd/observe` to see the fold.

| Path | Role |
|---|---|
| `internal/story/engine.go` | Assemble context, call the model, advance the chapter |
| `cmd/observe` | 10-section fold, no key |
| `internal/story/templates` | Template data and tests |
| `internal/store` | User and progress isolation |
| `internal/companion` | Memory, title, fallback |
| `internal/api` | OpenRouter client |
| `main.go` | Routes and the embedded entry |

Deploy scripts, the hostname, the database, and the cover images stay private. License: [MIT](LICENSE).
