package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"1001universe/internal/api"
	"1001universe/internal/companion"
	"1001universe/internal/gate"
	"1001universe/internal/store"
	"1001universe/internal/story"
)

//go:embed static
var staticFiles embed.FS

const cookieName = "uid"
const minPasswordLen = 6

// availableTemplates 是当前系统所有可用模板的列表，按顺序用于前端展示与选择。
var availableTemplates = []string{"campus", "ceo", "xianxia", "palace", "esports", "apocalypse", "ming", "wasteland", "academy"}


// ── server ────────────────────────────────────────────────────────────────

type server struct {
	store  *store.Store
	engine *story.Engine
	api    *api.Client
}

// ── main ──────────────────────────────────────────────────────────────────

func main() {
	loadEnv(".env")

	port := envOr("PORT", "8080")
	dbPath := envOr("DB_PATH", "./data/universe.db")
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENROUTER_API_KEY is required")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("create db dir: %v", err)
	}

	st, err := store.InitDB(dbPath)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer st.Close()

	cl := api.NewClient(apiKey)
	eng := story.NewEngine(st, cl)
	srv := &server{store: st, engine: eng, api: cl}

	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	// chi.Compress 处理 gzip/deflate/brotli；对 text/html、application/json、text/css、application/javascript 自动压缩。
	// Caddy 反代时也会再压一次；此处保险起见打开，CPU 几乎可忽略（HTML/JSON < 50KB）。
	r.Use(chimw.Compress(5))
	// 不再使用 chi.Logger：每次请求都打 slog 在高并发时是 I/O 瓶颈；改为只记录 API 请求的 middleware 单独输出。
	r.Use(apiAccessLog)

	// 静态页面（不需要认证）—— HTML 不缓存（Caddy 层再覆盖一次）
	r.Get("/", serveEmbedded("static/index.html", "text/html; charset=utf-8"))
	r.Get("/home", serveEmbedded("static/index.html", "text/html; charset=utf-8"))
	r.Get("/login", serveEmbedded("static/auth.html", "text/html; charset=utf-8"))
	r.Get("/register", serveEmbedded("static/auth.html", "text/html; charset=utf-8"))
	// /dashboard 已廢棄：index.html 對登入用戶已渲染 3 卡片 dashboard 視圖。
	// 保留 URL 以相容書籤／nav 連結，無條件 301 跳到首頁。
	r.Get("/dashboard", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/", http.StatusMovedPermanently)
	})
	r.Get("/chat", serveEmbedded("static/chat.html", "text/html; charset=utf-8"))

	// 数字伴侣页面（需登录）
	r.With(srv.requireAuthPage).Get("/companion", serveEmbedded("static/companion.html", "text/html; charset=utf-8"))

	// JS / CSS
	staticAssetHandler := longCacheHandler
	r.Get("/style.css", staticAssetHandler(serveEmbedded("static/style.css", "text/css; charset=utf-8")))
	r.Get("/app.1ec8229e.js", staticAssetHandler(serveEmbedded("static/app.1ec8229e.js", "application/javascript; charset=utf-8")))
	r.Get("/app.js", staticAssetHandler(serveEmbedded("static/app.1ec8229e.js", "application/javascript; charset=utf-8")))
	r.Get("/auth.js", staticAssetHandler(serveEmbedded("static/auth.js", "application/javascript; charset=utf-8")))
	r.Get("/home.444df366.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // 当前 (2026-06-29 新后宫群像)
	r.Get("/home.e4b7a903.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // legacy alias
	r.Get("/home.c9f5d834.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // legacy alias
	r.Get("/home.a3f8c124.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // legacy alias
	r.Get("/home.dc3a9f12.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // legacy alias
	r.Get("/home.ffdd5225.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8"))) // legacy alias
	r.Get("/home.js", staticAssetHandler(serveEmbedded("static/home.444df366.js", "application/javascript; charset=utf-8")))
	r.Get("/companion.v4.js", staticAssetHandler(serveEmbedded("static/companion.v4.js", "application/javascript; charset=utf-8")))
	r.Get("/companion.v3.js", staticAssetHandler(serveEmbedded("static/companion.v4.js", "application/javascript; charset=utf-8"))) // legacy alias → v4
	r.Get("/companion.v2.js", staticAssetHandler(serveEmbedded("static/companion.v4.js", "application/javascript; charset=utf-8"))) // legacy alias → v4
	r.Get("/companion.v1.js", staticAssetHandler(serveEmbedded("static/companion.v4.js", "application/javascript; charset=utf-8"))) // legacy alias → v4

	// UI 资产：favicon + 嵌入的静态图（logo / hero 装饰等）—— 公开访问、长缓存
	r.Get("/favicon.png", staticAssetHandler(serveEmbedded("static/favicon.png", "image/png")))
	r.Get("/favicon.webp", staticAssetHandler(serveEmbedded("static/favicon.webp", "image/webp")))
	r.Get("/img/logo.webp", staticAssetHandler(serveEmbedded("static/img/logo.webp", "image/webp")))
	r.Get("/img/hero-deco.webp", staticAssetHandler(serveEmbedded("static/img/hero-deco.webp", "image/webp")))
	// 兼容旧 PNG 路径（首次部署期间）
	r.Get("/img/logo.png", staticAssetHandler(serveEmbedded("static/img/logo.webp", "image/webp"))) // 内容已替换为 webp
	r.Get("/img/hero-deco.png", staticAssetHandler(serveEmbedded("static/img/hero-deco.webp", "image/webp")))
	r.Get("/img/footer-deco.webp", staticAssetHandler(serveEmbedded("static/img/footer-deco.webp", "image/webp"))) // 首页 footer 装饰带
	r.Get("/img/companion/male.webp",   staticAssetHandler(serveEmbedded("static/img/companion/male.webp",   "image/webp")))
	r.Get("/img/companion/female.webp", staticAssetHandler(serveEmbedded("static/img/companion/female.webp", "image/webp")))
	r.Get("/img/companion/huo.webp",    staticAssetHandler(serveEmbedded("static/img/companion/huo.webp",    "image/webp")))
	r.Get("/img/companion/su.webp",     staticAssetHandler(serveEmbedded("static/img/companion/su.webp",     "image/webp")))
	r.Get("/img/companion/shen.webp",   staticAssetHandler(serveEmbedded("static/img/companion/shen.webp",   "image/webp")))
	r.Get("/img/companion/pei.webp",    staticAssetHandler(serveEmbedded("static/img/companion/pei.webp",    "image/webp")))
	r.Get("/img/companion/hero-group.webp", staticAssetHandler(serveEmbedded("static/img/companion/hero-group.webp", "image/webp"))) // 首页 hero 6 角色群像

	// 9 个故事封面（home / dashboard 卡用）—— 公开访问、长缓存
	r.Get("/img/covers/campus.webp",     staticAssetHandler(serveEmbedded("static/img/covers/campus.webp",     "image/webp")))
	r.Get("/img/covers/ceo.webp",        staticAssetHandler(serveEmbedded("static/img/covers/ceo.webp",        "image/webp")))
	r.Get("/img/covers/xianxia.webp",    staticAssetHandler(serveEmbedded("static/img/covers/xianxia.webp",    "image/webp")))
	r.Get("/img/covers/palace.webp",     staticAssetHandler(serveEmbedded("static/img/covers/palace.webp",     "image/webp")))
	r.Get("/img/covers/esports.webp",    staticAssetHandler(serveEmbedded("static/img/covers/esports.webp",    "image/webp")))
	r.Get("/img/covers/apocalypse.webp", staticAssetHandler(serveEmbedded("static/img/covers/apocalypse.webp", "image/webp")))
	r.Get("/img/covers/ming.webp",       staticAssetHandler(serveEmbedded("static/img/covers/ming.webp",       "image/webp")))
	r.Get("/img/covers/wasteland.webp",  staticAssetHandler(serveEmbedded("static/img/covers/wasteland.webp",  "image/webp")))
	r.Get("/img/covers/academy.webp",    staticAssetHandler(serveEmbedded("static/img/covers/academy.webp",    "image/webp")))
	// 2 个首页装饰 banner（hero） + 3 个故事选项小插画（feature）
	r.Get("/img/covers/hero-multiverse.webp", staticAssetHandler(serveEmbedded("static/img/covers/hero-multiverse.webp", "image/webp")))
	r.Get("/img/covers/hero-heroes.webp",     staticAssetHandler(serveEmbedded("static/img/covers/hero-heroes.webp",     "image/webp")))
	r.Get("/img/covers/hero-story.webp",      staticAssetHandler(serveEmbedded("static/img/covers/hero-story.webp",      "image/webp")))
	r.Get("/img/covers/hero-games.webp",      staticAssetHandler(serveEmbedded("static/img/covers/hero-games.webp",      "image/webp")))
	r.Get("/img/covers/illus-ming.webp",      staticAssetHandler(serveEmbedded("static/img/covers/illus-ming.webp",      "image/webp")))
	r.Get("/img/covers/illus-wasteland.webp", staticAssetHandler(serveEmbedded("static/img/covers/illus-wasteland.webp", "image/webp")))
	r.Get("/img/covers/illus-academy.webp",   staticAssetHandler(serveEmbedded("static/img/covers/illus-academy.webp",   "image/webp")))

	// 公开 API（不需要认证）
	r.Post("/api/gate", srv.handleGate)            // 旧版门控（保留至迁移完成）
	r.Post("/api/register", srv.handleRegister)
	r.Post("/api/login", srv.handleLogin)
	r.Get("/api/templates", srv.handleListTemplates) // 首页卡片元数据

	// 需要认证的接口
	r.Group(func(r chi.Router) {
		r.Use(srv.authMiddleware)
		r.Get("/api/messages", srv.handleMessages)
		r.Post("/api/chat", srv.handleChat)
		r.Get("/api/progress", srv.handleProgress)
		r.Get("/api/whoami", srv.handleWhoami)
		r.Post("/api/logout", srv.handleLogout)
		r.Get("/api/home", srv.handleHome)        // 首页聚合数据：模板 + 每模板进度 + 最新预览
		r.Post("/api/reset", srv.handleReset)
		// 数字伴侣 API
		r.Get("/api/companion/sessions", companion.SessionsHandler(srv.store))
		r.Post("/api/companion/sessions", companion.CreateSessionHandler(srv.store))
		r.Delete("/api/companion/sessions/{id}", companion.DeleteSessionHandler(srv.store))
		r.Get("/api/companion/sessions/{id}/messages", companion.MessagesHandler(srv.store))
		r.Post("/api/companion/chat", companion.ChatHandler(srv.store, srv.api))
		r.Post("/api/companion/chat/stream", companion.StreamChatHandler(srv.store, srv.api))
		r.Get("/api/companion/memory", companion.MemoryHandler(srv.store))
	})

	slog.Info("server starting", "port", port)
	// 自定义 http.Server：调优 keep-alive、timeouts、HTTP/2。
	// 默认 ListenAndServe 用零值，连接复用和超时都不友好 —— 慢响应的根因之一。
	srv2 := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second, // /api/chat 可能要等 LLM 60s
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64KB
	}
	if err := srv2.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// ── middleware ─────────────────────────────────────────────────────────────

func (s *server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized"))
			return
		}
		u, err := s.store.GetUser(c.Value)
		if err != nil || u == nil {
			writeJSON(w, http.StatusUnauthorized, errResp("unauthorized"))
			return
		}
		ctx := store.SetUserInCtx(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userFromCtx(r *http.Request) *store.User {
	return store.UserFromCtx(r.Context())
}

// requireAuthPage 是 HTML 页面的认证中间件：cookie 无效则 302 跳到 /login，
// 而不是像 authMiddleware 那样返回 401 JSON。
// 用于 /dashboard 这类「未登录就不该看到」的页面，避免 JS 二次校验带来的竞态闪烁。
func (s *server) requireAuthPage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
			return
		}
		u, err := s.store.GetUser(c.Value)
		if err != nil || u == nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
			return
		}
		ctx := store.SetUserInCtx(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ── handlers ──────────────────────────────────────────────────────────────

// POST /api/gate
func (s *server) handleGate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
		return
	}

	userID, heroineName, ok := gate.Validate(req.Key)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errResp("钥匙不对哦"))
		return
	}

	if err := s.store.CreateUser(userID, heroineName); err != nil {
		slog.Error("create user", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}

	u, err := s.store.GetUser(userID)
	if err != nil {
		slog.Error("get user after create", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    userID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // HTTPS 生产环境生效；HTTP 本地开发浏览器会忽略
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":                  u.ID,
		"username":                 u.Username,
		"heroine_name":             u.HeroineName,
		"chapter":                  u.CurrentChapter,
		"section":                  u.CurrentSection,
		"available_templates":      availableTemplates,
		"needs_password_upgrade":   u.PasswordHash == "",
		"upgrade_hint":             "已升级为账号体系，建议注册用户名+密码以防丢失钥匙。可访问 /register 用新账号后再回头迁移数据。",
	})
}

// GET /api/messages?template=campus
func (s *server) handleMessages(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	templateID := r.URL.Query().Get("template")
	if templateID == "" {
		templateID = "campus"
	}
	msgs, err := s.store.GetMessagesWithTemplate(u.ID, templateID)
	if err != nil {
		slog.Error("get messages", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// POST /api/chat {content, template, model}
func (s *server) handleChat(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var req struct {
		Content  string `json:"content"`
		Template string `json:"template"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
		return
	}
	if req.Template == "" {
		req.Template = "campus"
	}

	text, chapter, section, ended, err := s.engine.GenerateSectionMeta(u.ID, req.Template, req.Content, req.Model)
	if err != nil {
		slog.Error("generate section", "userID", u.ID, "template", req.Template, "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("generation failed"))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"content": text,
		"chapter": chapter,
		"section": section,
		"ended":   ended,
	})
}

// GET /api/progress?template=campus
func (s *server) handleProgress(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	templateID := r.URL.Query().Get("template")
	if templateID == "" {
		templateID = "campus"
	}
	prog, err := s.store.GetUserProgress(u.ID, templateID)
	if err != nil {
		slog.Error("get progress", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}
	if prog == nil {
		writeJSON(w, http.StatusOK, map[string]int{"chapter": 1, "section": 1})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"chapter": prog.CurrentChapter,
		"section": prog.CurrentSection,
	})
}

// ── auth handlers (register / login / logout / whoami) ────────────────────

// POST /api/register {username, password}
func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeJSON(w, http.StatusBadRequest, errResp("用户名不能为空"))
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, errResp("密码至少6位"))
		return
	}

	// check existing
	existing, _ := s.store.GetUserByUsername(username)
	if existing != nil {
		// 不泄露用户名是否存在，统一返回模糊错误
		writeJSON(w, http.StatusUnauthorized, errResp("注册失败，请检查输入"))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("bcrypt hash", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}

	userID := generateUserID(username)
	if err := s.store.CreateUserWithPassword(userID, username, string(hash), "campus"); err != nil {
		slog.Error("create user with pass", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
		return
	}

	setAuthCookie(w, userID)
	writeJSON(w, http.StatusOK, userInfoResponse(userID, username))
}

// POST /api/login {username, password}
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeJSON(w, http.StatusUnauthorized, errResp("用户名或密码错误"))
		return
	}

	u, err := s.store.GetUserByUsername(username)
	if err != nil || u == nil || u.PasswordHash == "" {
		// 不泄露用户名是否存在
		writeJSON(w, http.StatusUnauthorized, errResp("用户名或密码错误"))
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, errResp("用户名或密码错误"))
		return
	}

	setAuthCookie(w, u.ID)
	writeJSON(w, http.StatusOK, userInfoResponse(u.ID, u.Username))
}

// POST /api/logout
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/whoami
func (s *server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	resp := userInfoResponse(u.ID, u.Username)
	resp["needs_password_upgrade"] = u.PasswordHash == ""
	if u.PasswordHash == "" {
		resp["upgrade_hint"] = "你正在使用旧版门控登录。建议注册用户名+密码以防丢失钥匙。"
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/templates — 返回首页卡片所需的模板元数据（公开接口）。
// 卡片内容是静态的，但 chapter names / display name 等元信息由后端单一来源管理，
// 避免前端硬编码与后端 prompt 漂移。
func (s *server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	type tplMeta struct {
		ID           string   `json:"id"`
		DisplayName  string   `json:"display_name"`
		Tagline      string   `json:"tagline"`
		Keywords     []string `json:"keywords"`
		Teaser       string   `json:"teaser"`
		ChapterNames []string `json:"chapter_names"`
		MaxChapters  int      `json:"max_chapters"`
		CoverURL     string   `json:"cover_url"`
		LeadName     string   `json:"lead_name"`
	}
	out := []tplMeta{
		{
			ID:          "campus",
			DisplayName: "校园三男主",
			Tagline:     "平行日本大学 · 三个男主角同时追求穿越女主",
			Keywords:    []string{"樱花", "学生会", "吉他", "舞蹈"},
			Teaser:     "夏晚棠穿越到这个世界第一天，就被学生会长壁咚、音乐系学长弹歌、舞蹈社学长无声守护。命运偏心她，三人同时失控。微虐回甘，每一节都让人心跳骤停。",
			ChapterNames: []string{"命运相遇", "日常升温", "暗涌与误会", "摊牌", "平行结局"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/campus.webp",
			LeadName:     "夏晚棠",
		},
		{
			ID:          "ceo",
			DisplayName: "契约总裁",
			Tagline:     "现代都市权谋 · 3年契约女友，两个男人撕扯",
			Keywords:    []string{"契约", "霸总", "商业战", "壁咚"},
			Teaser:     "林晚签下契约成为顾氏总裁的「女友」。冷峻顾霆用领带把她拉近，商业对手沈越温柔设局。合同条款随时能改写她的命运，每一次靠近都是权力的游戏。",
			ChapterNames: []string{"契约签订", "公开秀恩爱", "暗流涌动", "撕破脸", "真心选择"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/ceo.webp",
			LeadName:     "林晚",
		},
		{
			ID:          "xianxia",
			DisplayName: "仙门争锋",
			Tagline:     "九霄宗门 · 外门废柴女主，三峰主同时心动",
			Keywords:    []string{"修仙", "秘境", "心魔", "飞剑"},
			Teaser:     "苏璃穿越成外门废柴弟子，玄清峰主冷眼旁观却暗中护短，大师兄温柔救场，魔族质子危险又迷人。灵力共鸣、宗门脸面、心魔劫——每一步都是命悬一线。",
			ChapterNames: []string{"入门试炼", "秘境历练", "宗门大比", "心魔劫", "飞升抉择"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/xianxia.webp",
			LeadName:     "苏璃",
		},
		{
			ID:          "palace",
			DisplayName: "帝王独宠",
			Tagline:     "古风宫廷穿越 · 冷宫废妃到三位皇子争夺的心尖宠",
			Keywords:    []string{"宫廷", "帝王", "摄政王", "少年将军"},
			Teaser:     "沈昭重生为冷宫废妃，前世她把真心错付，今生她步步为营。少年帝王夜夜「恰巧」路过她的宫殿，摄政王转佛珠的手因她停顿，少年将军连夜从岭南背回她想要的花。帝王榻上坐，江湖策马奔——三个男人为她争得天下大乱。",
			ChapterNames: []string{"冷宫初醒", "暗流涌动", "锋芒初露", "帝王心动", "凤位之争"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/palace.webp",
			LeadName:     "沈昭",
		},
		{
			ID:          "esports",
			DisplayName: "电竞甜心",
			Tagline:     "LPL 顶级联赛 · 女队替补被三位男职业选手暗恋",
			Keywords:    []string{"电竞", "少年", "教练", "暗恋"},
			Teaser:     "林念一不小心录屏点评「这打野时机选得太差了」爆火全网，被 LPL 顶级战队破格招募为女队替补。毒舌队长赛前说「别拖后腿」赛后却红着耳尖给她递水，国民弟弟 ADC 把限定款键盘塞给她试，金丝眼镜教练深夜给她送夜宵——电竞修罗场，她是唯一的女王。",
			ChapterNames: []string{"破格入选", "训练日常", "赛场初啼", "心动信号", "冠军之夜"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/esports.webp",
			LeadName:     "林念",
		},
		{
			ID:          "apocalypse",
			DisplayName: "末世女王",
			Tagline:     "末世重生 · 前世仇人与今生大佬的三重纠缠",
			Keywords:    []string{"末世", "重生", "异能", "丧尸"},
			Teaser:     "苏瑶前世被最信任的未婚夫亲手推入尸潮而亡，重生回到末世爆发前三个月。这一世她布局一切、囤物资、建安全屋、查清所有背叛她的人。北境基地首领是她前世的仇人，研究院院长是她前世错过她的人，少年狙击手是她前世为她挡枪的人——三个男人，这一世都要为她神魂颠倒。",
			ChapterNames: []string{"重生归来", "废土求生", "强者云集", "前世真相", "末世之王"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/apocalypse.webp",
			LeadName:     "苏瑶",
		},
		{
			ID:          "ming",
			DisplayName: "民国谍影",
			Tagline:     "1930上海滩 · 穿越名媛在三方势力间周旋",
			Keywords:    []string{"民国", "谍战", "上海", "三角"},
			Teaser:     "沈微穿越成上海滩沈家千金，在百乐门舞厅的第一晚就被三方势力盯上。国民党少帅陆时渊冷峻克制却为她耳根发烫，日本军官藤原纪温润如玉却给她致命温柔，地下党联络员江潮生少年意气却深夜来送糖藕。十里洋场的霓虹下，谁是真心谁是棋子——她自己就是最大的变数。",
			ChapterNames: []string{"十里洋场", "雾都暗战", "身份暴露", "生死抉择", "新的黎明"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/ming.webp",
			LeadName:     "沈微",
		},
		{
			ID:          "wasteland",
			DisplayName: "废土拾光",
			Tagline:     "末世植物系异能 · 重生虐恋与废土新生",
			Keywords:    []string{"末世", "异能", "重生", "废土"},
			Teaser:     "苏晚穿越到末世第三年的废土，手腕上那株会回应她心跳的小藤蔓是她的植物系异能火种。北境基地长沈夜是前世亲手推她入尸潮的前男友，研究院首席江潮是前世错过她的人，变异少年傅野是前世为她挡枪的人——三个男人，这一世都要为她神魂颠倒。废土里开出的藤蔓花，是她这一世最锋利的武器。",
			ChapterNames: []string{"末日降临", "废土求生", "异能觉醒", "基地风云", "新的黎明"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/wasteland.webp",
			LeadName:     "苏晚",
		},
		{
			ID:          "academy",
			DisplayName: "魔法学院",
			Tagline:     "西方奥术学院 · 东方交换生的禁忌魔法与跨文化甜恋",
			Keywords:    []string{"魔法", "学院", "跨文化", "禁忌"},
			Teaser:     "叶清鸢穿越成艾瑟兰皇家奥术学院建校五百年来第一位东方交换生。冰山学长艾德温用雷系魔法压制一切却被她的东方符箓反超，血族王子莱昂哈德用永生花示爱却被她的道术戳破皇室秘密，傲娇会长雷欧尼板着脸塞给她零食说「才不是给你的」。东方道术 vs 西方魔法，现代女性 vs 异世界贵族规矩——跨文化的碰撞，是她最致命的武器。",
			ChapterNames: []string{"入学之日", "魔法觉醒", "禁忌之书", "血族秘密", "王座抉择"},
			MaxChapters:  5,
			CoverURL:     "/img/covers/academy.webp",
			LeadName:     "叶清鸢",
		},
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/home — 首页聚合数据（需登录）。
// 返回每模板的：(1) 进度 (chapter, section) (2) 消息数 (3) 最新一节 assistant 预览。
// 前端一次请求拿到所有数据，避免连续串行 /api/messages + /api/progress 抖动。
func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	type progressInfo struct {
		TemplateID    string `json:"template_id"`
		DisplayName   string `json:"display_name"`
		Tagline       string `json:"tagline"`
		Teaser        string `json:"teaser"`
		LeadName      string `json:"lead_name"`
		CoverURL      string `json:"cover_url"`
		Chapter       int    `json:"chapter"`
		Section       int    `json:"section"`
		MessageCount  int    `json:"message_count"`
		Ended         bool   `json:"ended"`
		HasStarted    bool   `json:"has_started"`
		LatestChapter int    `json:"latest_chapter"`
		LatestSection int    `json:"latest_section"`
		LatestPreview string `json:"latest_preview"`
		LatestTime    string `json:"latest_at"`
	}
	type homeResp struct {
		Username  string         `json:"username"`
		UserID    string         `json:"user_id"`
		Templates []progressInfo `json:"templates"`
	}
	out := homeResp{Username: u.Username, UserID: u.ID}
	for _, t := range story.AllTemplates() {
		// 从 templates.AllTemplates() 返回的 *Template 拿静态元信息
		meta := getTemplateMeta(t.ID)
		pi := progressInfo{
			TemplateID:  t.ID,
			DisplayName: meta.DisplayName,
			Tagline:     meta.Tagline,
			Teaser:      meta.Teaser,
			LeadName:    meta.LeadName,
			CoverURL:    meta.CoverURL,
		}
		if p, err := s.store.GetUserProgress(u.ID, t.ID); err == nil && p != nil {
			pi.Chapter = p.CurrentChapter
			pi.Section = p.CurrentSection
		} else {
			pi.Chapter, pi.Section = 1, 1
		}
		if n, err := s.store.CountMessagesByTemplate(u.ID, t.ID); err == nil {
			pi.MessageCount = n
			pi.HasStarted = n > 0
		}
		if pi.HasStarted {
			if m, err := s.store.LatestAssistantMessage(u.ID, t.ID); err == nil && m != nil {
				pi.LatestChapter = m.Chapter
				pi.LatestSection = m.Section
				pi.LatestTime = m.CreatedAt.Format("2006-01-02 15:04")
				// preview: 取前 80 字符，去掉换行
				runes := []rune(m.Content)
				preview := string(runes)
				if len(runes) > 80 {
					preview = string(runes[:80]) + "…"
				}
				pi.LatestPreview = preview
			}
			if pi.Chapter >= t.MaxChapters && pi.Section >= 10 {
				pi.Ended = true
			}
		}
		out.Templates = append(out.Templates, pi)
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/reset {template} — 重置某个宇宙的消息+摘要+进度到 (1,1)。
// 用于首页「重新阅读」按钮：保留模板选择权，但丢掉旧故事重新开局。
func (s *server) handleReset(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var req struct {
		Template string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
		return
	}
	if !story.IsValidTemplate(req.Template) {
		writeJSON(w, http.StatusBadRequest, errResp("unknown template"))
		return
	}
	if err := s.store.ResetProgress(u.ID, req.Template); err != nil {
		slog.Error("reset progress", "err", err)
		writeJSON(w, http.StatusInternalServerError, errResp("reset failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"template": req.Template,
		"chapter":  1,
		"section":  1,
	})
}

// templateMeta 是模板的客户端可见元数据；与 handleListTemplates 的字段一一对应，
// handleHome 用它把 cover_url / display_name 注入到 dashboard 的进度数据里。
type templateMeta struct {
	DisplayName string
	Tagline     string
	Teaser      string
	LeadName    string
	CoverURL    string
}

// getTemplateMeta 从下方静态注册表里读元数据；displayed 与 /api/templates 一致。
func getTemplateMeta(id string) templateMeta {
	if m, ok := templateMetaRegistry[id]; ok {
		return m
	}
	return templateMeta{}
}

// templateMetaRegistry 与 handleListTemplates 严格同步。
// 这里独立维护一份是为了 handleHome 拿静态元信息（不需要 chapter_names / max_chapters）。
var templateMetaRegistry = map[string]templateMeta{
	"campus":     {DisplayName: "校园三男主", Tagline: "平行日本大学 · 三个男主角同时追求穿越女主", LeadName: "夏晚棠", CoverURL: "/img/covers/campus.webp", Teaser: "夏晚棠穿越到这个世界第一天，就被学生会长壁咚、音乐系学长弹歌、舞蹈社学长无声守护。命运偏心她，三人同时失控。微虐回甘，每一节都让人心跳骤停。"},
	"ceo":        {DisplayName: "契约总裁", Tagline: "现代都市权谋 · 3年契约女友，两个男人撕扯", LeadName: "林晚", CoverURL: "/img/covers/ceo.webp", Teaser: "林晚签下契约成为顾氏总裁的「女友」。冷峻顾霆用领带把她拉近，商业对手沈越温柔设局。合同条款随时能改写她的命运，每一次靠近都是权力的游戏。"},
	"xianxia":    {DisplayName: "仙门争锋", Tagline: "九霄宗门 · 外门废柴女主，三峰主同时心动", LeadName: "苏璃", CoverURL: "/img/covers/xianxia.webp", Teaser: "苏璃穿越成外门废柴弟子，玄清峰主冷眼旁观却暗中护短，大师兄温柔救场，魔族质子危险又迷人。灵力共鸣、宗门脸面、心魔劫——每一步都是命悬一线。"},
	"palace":     {DisplayName: "帝王独宠", Tagline: "古风宫廷穿越 · 冷宫废妃到三位皇子争夺的心尖宠", LeadName: "沈昭", CoverURL: "/img/covers/palace.webp", Teaser: "沈昭重生为冷宫废妃，前世她把真心错付，今生她步步为营。少年帝王夜夜「恰巧」路过她的宫殿，摄政王转佛珠的手因她停顿，少年将军连夜从岭南背回她想要的花。帝王榻上坐，江湖策马奔——三个男人为她争得天下大乱。"},
	"esports":    {DisplayName: "电竞甜心", Tagline: "LPL 顶级联赛 · 女队替补被三位男职业选手暗恋", LeadName: "林念", CoverURL: "/img/covers/esports.webp", Teaser: "林念一不小心录屏点评「这打野时机选得太差了」爆火全网，被 LPL 顶级战队破格招募为女队替补。毒舌队长赛前说「别拖后腿」赛后却红着耳尖给她递水，国民弟弟 ADC 把限定款键盘塞给她试，金丝眼镜教练深夜给她送夜宵——电竞修罗场，她是唯一的女王。"},
	"apocalypse": {DisplayName: "末世女王", Tagline: "末世重生 · 前世仇人与今生大佬的三重纠缠", LeadName: "苏瑶", CoverURL: "/img/covers/apocalypse.webp", Teaser: "苏瑶前世被最信任的未婚夫亲手推入尸潮而亡，重生回到末世爆发前三个月。这一世她布局一切、囤物资、建安全屋、查清所有背叛她的人。北境基地首领是她前世的仇人，研究院院长是她前世错过她的人，少年狙击手是她前世为她挡枪的人——三个男人，这一世都要为她神魂颠倒。"},
	"ming":       {DisplayName: "民国谍影", Tagline: "1930上海滩 · 穿越名媛在三方势力间周旋", LeadName: "沈微", CoverURL: "/img/covers/ming.webp", Teaser: "沈微穿越成上海滩沈家千金，在百乐门舞厅的第一晚就被三方势力盯上。国民党少帅陆时渊冷峻克制却为她耳根发烫，日本军官藤原纪温润如玉却给她致命温柔，地下党联络员江潮生少年意气却深夜来送糖藕。十里洋场的霓虹下，谁是真心谁是棋子——她自己就是最大的变数。"},
	"wasteland":  {DisplayName: "废土拾光", Tagline: "末世植物系异能 · 重生虐恋与废土新生", LeadName: "苏晚", CoverURL: "/img/covers/wasteland.webp", Teaser: "苏晚穿越到末世第三年的废土，手腕上那株会回应她心跳的小藤蔓是她的植物系异能火种。北境基地长沈夜是前世亲手推她入尸潮的前男友，研究院首席江潮是前世错过她的人，变异少年傅野是前世为她挡枪的人——三个男人，这一世都要为她神魂颠倒。废土里开出的藤蔓花，是她这一世最锋利的武器。"},
	"academy":    {DisplayName: "魔法学院", Tagline: "西方奥术学院 · 东方交换生的禁忌魔法与跨文化甜恋", LeadName: "叶清鸢", CoverURL: "/img/covers/academy.webp", Teaser: "叶清鸢穿越成艾瑟兰皇家奥术学院建校五百年来第一位东方交换生。冰山学长艾德温用雷系魔法压制一切却被她的东方符箓反超，血族王子莱昂哈德用永生花示爱却被她的道术戳破皇室秘密，傲娇会长雷欧尼板着脸塞给她零食说「才不是给你的」。东方道术 vs 西方魔法，现代女性 vs 异世界贵族规矩——跨文化的碰撞，是她最致命的武器。"},
}

// ── auth helpers ──────────────────────────────────────────────────────────

func setAuthCookie(w http.ResponseWriter, userID string) {
	// Max-Age=86400 (1 天) 关键：
	//   - Safari 对纯 session cookie + Secure 在某些 ITP 场景下不持久化，加上 Max-Age 后
	//     浏览器更倾向于写入持久 cookie 存储，从而保证后续跨页面 fetch 能读到。
	//   - HttpOnly 防止 XSS 读取；Secure 保证只在 HTTPS 下发送；SameSite=Lax 让
	//     顶层 GET 跳转和同源 fetch 都带上 cookie。
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    userID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})
}

func userInfoResponse(userID, username string) map[string]any {
	return map[string]any{
		"user_id":             userID,
		"username":            username,
		"available_templates": availableTemplates,
	}
}

// generateUserID 从用户名派生一个稳定且唯一的前缀，附带随机后缀避免冲突。
func generateUserID(username string) string {
	safe := make([]byte, 0, len(username))
	for _, c := range []byte(username) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			safe = append(safe, c)
		default:
			safe = append(safe, '_')
		}
		if len(safe) >= 16 {
			break
		}
	}
	if len(safe) == 0 {
		safe = []byte("u")
	}
	rnd := make([]byte, 4)
	_, _ = rand.Read(rnd)
	return "u_" + string(safe) + "_" + hex.EncodeToString(rnd)
}

// _ ensures time import is used elsewhere if needed
var _ = time.Now

// ── helpers ───────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}

// serveEmbedded 返回一个从嵌入 FS 读文件并写出的 handler。
// 用 sync.Once + bytes.Buffer 把每个文件预读到内存 —— embed.FS.ReadFile 本身不缓存，
// 每次请求都从 mmap 重新读取。预先一次性读到内存可避免在 I/O 路径上做任何文件系统操作。
var (
	embeddedOnce sync.Once
	embedded     = map[string]struct {
		data []byte
		ct   string
	}{}
)

func prewarmEmbedded() {
	for _, it := range []struct {
		path string
		ct   string
	}{
		{"static/index.html", "text/html; charset=utf-8"},
		{"static/auth.html", "text/html; charset=utf-8"},
		{"static/chat.html", "text/html; charset=utf-8"},
		{"static/companion.html", "text/html; charset=utf-8"},
		{"static/style.css", "text/css; charset=utf-8"},
		{"static/app.js", "application/javascript; charset=utf-8"},
		{"static/auth.js", "application/javascript; charset=utf-8"},
		{"static/home.444df366.js", "application/javascript; charset=utf-8"},
		{"static/home.js", "application/javascript; charset=utf-8"},
		{"static/companion.v4.js", "application/javascript; charset=utf-8"},
		{"static/img/companion/male.webp", "image/webp"},
		{"static/img/companion/female.webp", "image/webp"},
		{"static/img/companion/huo.webp", "image/webp"},
		{"static/img/companion/su.webp", "image/webp"},
		{"static/img/companion/shen.webp", "image/webp"},
		{"static/img/companion/pei.webp", "image/webp"},
		{"static/favicon.png", "image/png"},
		{"static/favicon.webp", "image/webp"},
		{"static/img/logo.webp", "image/webp"},
		{"static/img/hero-deco.webp", "image/webp"},
		{"static/img/footer-deco.webp", "image/webp"},
		{"static/img/covers/campus.webp", "image/webp"},
		{"static/img/covers/ceo.webp", "image/webp"},
		{"static/img/covers/xianxia.webp", "image/webp"},
		{"static/img/covers/palace.webp", "image/webp"},
		{"static/img/covers/esports.webp", "image/webp"},
		{"static/img/covers/apocalypse.webp", "image/webp"},
		{"static/img/covers/ming.webp", "image/webp"},
		{"static/img/covers/wasteland.webp", "image/webp"},
		{"static/img/covers/academy.webp", "image/webp"},
		{"static/img/covers/hero-multiverse.webp", "image/webp"},
		{"static/img/covers/hero-heroes.webp", "image/webp"},
		{"static/img/covers/hero-story.webp", "image/webp"},
		{"static/img/covers/illus-ming.webp", "image/webp"},
		{"static/img/covers/illus-wasteland.webp", "image/webp"},
		{"static/img/covers/illus-academy.webp", "image/webp"},
	} {
		data, err := staticFiles.ReadFile(it.path)
		if err != nil {
			continue
		}
		// 复制到独立 slice，避免 embed 内部 buffer 被引用泄漏
		buf := make([]byte, len(data))
		copy(buf, data)
		embedded[it.path] = struct {
			data []byte
			ct   string
		}{buf, it.ct}
	}
}

func serveEmbedded(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		embeddedOnce.Do(prewarmEmbedded)
		e, ok := embedded[name]
		if !ok {
			// 缓存未命中（罕见）：再读一次并补回缓存
			data, err := staticFiles.ReadFile(name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			e.data = data
			e.ct = contentType
			embedded[name] = e
		}
		w.Header().Set("Content-Type", e.ct)
		w.Header().Set("Content-Length", strconv.Itoa(len(e.data)))
		w.WriteHeader(http.StatusOK)
		w.Write(e.data) //nolint:errcheck
	}
}

// longCacheHandler 给静态资源打 1 年 immutable 缓存头。HandlerFunc 形态方便直接传入 r.Get。
func longCacheHandler(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h(w, r)
	}
}

// longCache 是 Handler 接口版的同名函数（供 r.Handle 等需要 http.Handler 的场合）。
func longCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}

// apiAccessLog 精简版访问日志：只记 /api/*，跳过静态资源与图像。
// 不记录响应体大小、不格式化时间；高并发下日志 I/O 不再是瓶颈。
func apiAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		slog.Info("api",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

// loadEnv 简单解析 .env 文件并写入环境变量（已设置的不覆盖）。
// 生产环境由 systemd EnvironmentFile 处理，此函数仅用于本地开发。
func loadEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v) //nolint:errcheck
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
