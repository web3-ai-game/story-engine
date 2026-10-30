# story-engine

互動小說引擎。每個使用者、每個模板的進度分開存。生成前先組三層上下文，主模型失敗才改走備援。

An interactive fiction engine. Progress is isolated per user and per template. Each section is assembled from three context layers. The fallback model runs only after the primary model fails.

這是脫敏後的公開匯出。頁面、封面、部署腳本和主機位址沒有放進來。私有原庫保持私有。

This is a sanitized public export. The pages, covers, deploy scripts, and host address are not here. The private original stays private.

## 引擎在做什麼

一章固定 10 節。當前章節只把最近 3 節放進提示詞，更早的內容靠摘要。這樣章節變長時，上下文不會把整本歷史再送一次。

A chapter is 10 sections. The prompt keeps the latest 3 sections of the current chapter. Older text is carried as a summary, so a long chapter does not resend its whole history.

模板在 `internal/story/templates`。世界觀、人設和結局條件是資料。handler 不自己拼提示詞。

Templates live in `internal/story/templates`. Setting, cast, and ending conditions are data. Handlers do not assemble the prompt themselves.

陪伴對話在 `internal/companion`。最近 30 輪進上下文。記憶每 5 輪更新一次。標題在第 4 輪生成。主模型失敗時改走較小的備援模型。

Companion chat is in `internal/companion`. The last 30 turns enter the context. Memory updates every 5 turns. The title is generated at turn 4. If the primary model fails, a smaller fallback runs.

進度存在 SQLite。同一個模板，不同使用者的章節互不覆蓋。

Progress is stored in SQLite. Two users in the same template do not overwrite each other's chapter.

## 目錄

| 路徑 | 職責 |
|---|---|
| `internal/story/engine.go` | 組上下文、呼叫模型、推進章節。Assemble context, call the model, advance the chapter. |
| `internal/story/templates` | 模板資料與測試。Template data and tests. |
| `internal/store` | 使用者與進度隔離。User and progress isolation. |
| `internal/companion` | 記憶、標題、備援。Memory, title, fallback. |
| `internal/api` | 上游客戶端。Upstream client. |
| `main.go` | 路由與嵌入的靜態入口。Routes and the embedded entry. |

## 本地執行

需要 Go 1.23 以上。金鑰用環境變數，不寫進這個庫。

Needs Go 1.23 or newer. The key comes from the environment. It is not in this repo.

```bash
export OPENROUTER_API_KEY=
go test ./internal/story/... ./internal/store/...
go run .
```

預設聽本機。沒有金鑰時，生成路徑會失敗，測試仍可跑。

It listens on localhost. Without a key, generation fails. The tests still run.

## 沒有放進來的東西

部署腳本、域名、資料庫、封面圖。那些留在私有庫。

Deploy scripts, the hostname, the database, and the cover images. Those stay in the private repo.

## 授權

未另附授權檔。看原始碼可以，拿去商用前先來信 one@oece-x.com。

No license file is attached. Reading the source is fine. Ask before commercial use: one@oece-x.com.
