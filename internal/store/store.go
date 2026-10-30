package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// UserCtxKey is the shared context key for injecting the authenticated user.
type UserCtxKey struct{}

func SetUserInCtx(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, UserCtxKey{}, u)
}

func UserFromCtx(ctx context.Context) *User {
	u, _ := ctx.Value(UserCtxKey{}).(*User)
	return u
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id              TEXT PRIMARY KEY,
	username        TEXT UNIQUE,
	heroine_name    TEXT,
	password_hash   TEXT,
	current_chapter INTEGER NOT NULL DEFAULT 1,
	current_section INTEGER NOT NULL DEFAULT 1,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     TEXT    NOT NULL,
	template_id TEXT    NOT NULL DEFAULT 'campus',
	role        TEXT    NOT NULL,
	content     TEXT    NOT NULL,
	chapter     INTEGER NOT NULL,
	section     INTEGER NOT NULL,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS chapter_summaries (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id     TEXT    NOT NULL,
	template_id TEXT    NOT NULL DEFAULT 'campus',
	chapter     INTEGER NOT NULL,
	summary     TEXT    NOT NULL,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (user_id) REFERENCES users(id),
	UNIQUE(user_id, template_id, chapter)
);

CREATE TABLE IF NOT EXISTS user_progress (
	user_id     TEXT    NOT NULL,
	template_id TEXT    NOT NULL DEFAULT 'campus',
	current_chapter INTEGER NOT NULL DEFAULT 1,
	current_section INTEGER NOT NULL DEFAULT 1,
	updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (user_id, template_id),
	FOREIGN KEY (user_id) REFERENCES users(id)
);
`

type Store struct {
	db *sql.DB
}

type User struct {
	ID             string
	Username       string
	HeroineName    string
	PasswordHash   string
	CurrentChapter int
	CurrentSection int
	CreatedAt      time.Time
}

// Progress holds per-template progress for a user.
type Progress struct {
	UserID         string
	TemplateID     string
	CurrentChapter int
	CurrentSection int
}

type Message struct {
	ID         int64
	UserID     string
	TemplateID string // "campus" | "ceo" | "xianxia"
	Role       string // "assistant" | "user"
	Content    string
	Chapter    int
	Section    int
	CreatedAt  time.Time
}

type ChapterSummary struct {
	ID         int64
	UserID     string
	TemplateID string
	Chapter    int
	Summary    string
	CreatedAt  time.Time
}

func InitDB(dbPath string) (*Store, error) {
	// _busy_timeout=5000: 等待 5 秒而非立刻返回 SQLITE_BUSY（避免并发写入时 500 错误）
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite 单写模式：1 个连接即可避免写冲突；_busy_timeout 保证排队等待而非报错
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// run idempotent migration for existing prod DBs (adds columns/tables + backfill)
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// CreateUser 创建用户，若已存在则忽略（幂等）
func (s *Store) CreateUser(id, heroineName string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO users (id, heroine_name) VALUES (?, ?)`,
		id, heroineName,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// GetUser 查询用户，不存在时返回 nil, nil
func (s *Store) GetUser(id string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, COALESCE(username,''), COALESCE(heroine_name,''), COALESCE(password_hash,''), current_chapter, current_section, created_at FROM users WHERE id = ?`,
		id,
	)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.HeroineName, &u.PasswordHash, &u.CurrentChapter, &u.CurrentSection, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// UpdateUserProgress (legacy 2-arg) keeps old call sites working by delegating to campus
func (s *Store) UpdateUserProgress(userID string, chapter, section int) error {
	return s.UpdateUserProgressTemplate(userID, "campus", chapter, section)
}

// SaveMessage 持久化一条消息（legacy，默认 campus）
func (s *Store) SaveMessage(userID, role, content string, chapter, section int) error {
	return s.SaveMessageWithTemplate(userID, "campus", role, content, chapter, section)
}

// GetMessages 返回用户所有消息，按时间正序（legacy，默认为 campus）
func (s *Store) GetMessages(userID string) ([]Message, error) {
	return s.GetMessagesWithTemplate(userID, "campus")
}

// GetChapterSummaries 返回用户所有章节摘要，按章节正序（legacy，默认 campus）
func (s *Store) GetChapterSummaries(userID string) ([]ChapterSummary, error) {
	return s.GetChapterSummariesWithTemplate(userID, "campus")
}

// SaveChapterSummary legacy wrapper
func (s *Store) SaveChapterSummary(userID string, chapter int, summary string) error {
	return s.SaveChapterSummaryWithTemplate(userID, "campus", chapter, summary)
}

// --- new template-aware + auth methods ---

// GetUserProgress returns per-template progress. Falls back to legacy user columns if no row in user_progress.
func (s *Store) GetUserProgress(userID, templateID string) (*Progress, error) {
	if templateID == "" {
		templateID = "campus"
	}
	p := &Progress{UserID: userID, TemplateID: templateID}
	row := s.db.QueryRow(
		`SELECT current_chapter, current_section FROM user_progress WHERE user_id = ? AND template_id = ?`,
		userID, templateID,
	)
	err := row.Scan(&p.CurrentChapter, &p.CurrentSection)
	if err == sql.ErrNoRows {
		// fallback to legacy users table (for old hui/wen or pre-migration)
		row2 := s.db.QueryRow(`SELECT current_chapter, current_section FROM users WHERE id = ?`, userID)
		if err2 := row2.Scan(&p.CurrentChapter, &p.CurrentSection); err2 != nil {
			if err2 == sql.ErrNoRows {
				return nil, nil
			}
			return nil, fmt.Errorf("get legacy progress: %w", err2)
		}
		return p, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get progress: %w", err)
	}
	return p, nil
}

// UpdateUserProgressTemplate sets per-template progress and also updates legacy columns when template=='campus' for compat.
func (s *Store) UpdateUserProgressTemplate(userID, templateID string, chapter, section int) error {
	if templateID == "" {
		templateID = "campus"
	}
	_, err := s.db.Exec(
		`INSERT INTO user_progress (user_id, template_id, current_chapter, current_section)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, template_id) DO UPDATE SET current_chapter=excluded.current_chapter, current_section=excluded.current_section, updated_at=CURRENT_TIMESTAMP`,
		userID, templateID, chapter, section,
	)
	if err != nil {
		return fmt.Errorf("update progress: %w", err)
	}
	if templateID == "campus" {
		// keep legacy in sync for old paths
		_, _ = s.db.Exec(`UPDATE users SET current_chapter=?, current_section=? WHERE id=?`, chapter, section, userID)
	}
	return nil
}

// ResetProgress 把指定模板的进度重置为 (1,1)，并清空该模板下所有消息 + 章节摘要。
// 用于首页「重新生成」按钮：用户可从头开始一段故事，旧历史保留语义删除但前端不可见。
func (s *Store) ResetProgress(userID, templateID string) error {
	if templateID == "" {
		templateID = "campus"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE user_id=? AND template_id=?`, userID, templateID); err != nil {
		return fmt.Errorf("delete messages: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chapter_summaries WHERE user_id=? AND template_id=?`, userID, templateID); err != nil {
		return fmt.Errorf("delete summaries: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO user_progress (user_id, template_id, current_chapter, current_section)
		VALUES (?, ?, 1, 1)
		ON CONFLICT(user_id, template_id) DO UPDATE SET current_chapter=1, current_section=1, updated_at=CURRENT_TIMESTAMP`,
		userID, templateID); err != nil {
		return fmt.Errorf("reset progress: %w", err)
	}
	if templateID == "campus" {
		// keep legacy columns in sync
		if _, err := tx.Exec(`UPDATE users SET current_chapter=1, current_section=1 WHERE id=?`, userID); err != nil {
			return fmt.Errorf("reset legacy: %w", err)
		}
	}
	return tx.Commit()
}

// CountMessagesByTemplate returns the number of assistant+user messages for a template.
// Used by /api/home to decide whether a universe has any progress.
func (s *Store) CountMessagesByTemplate(userID, templateID string) (int, error) {
	if templateID == "" {
		templateID = "campus"
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE user_id=? AND template_id=?`, userID, templateID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return n, nil
}

// LatestAssistantMessage returns the most recent assistant message for a template (id+chapter+section+preview).
// Used by /api/home to show "最近一节" snippet on the dashboard cards.
func (s *Store) LatestAssistantMessage(userID, templateID string) (*Message, error) {
	if templateID == "" {
		templateID = "campus"
	}
	row := s.db.QueryRow(
		`SELECT id, user_id, template_id, role, content, chapter, section, created_at
		 FROM messages WHERE user_id=? AND template_id=? AND role='assistant'
		 ORDER BY created_at DESC LIMIT 1`,
		userID, templateID,
	)
	var m Message
	err := row.Scan(&m.ID, &m.UserID, &m.TemplateID, &m.Role, &m.Content, &m.Chapter, &m.Section, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest assistant: %w", err)
	}
	return &m, nil
}

// SaveMessageWithTemplate 持久化一条消息（带 template）
func (s *Store) SaveMessageWithTemplate(userID, templateID, role, content string, chapter, section int) error {
	if templateID == "" {
		templateID = "campus"
	}
	_, err := s.db.Exec(
		`INSERT INTO messages (user_id, template_id, role, content, chapter, section) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, templateID, role, content, chapter, section,
	)
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}
	return nil
}

// GetMessagesWithTemplate 返回用户指定模板的消息，按时间正序
func (s *Store) GetMessagesWithTemplate(userID, templateID string) ([]Message, error) {
	if templateID == "" {
		templateID = "campus"
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, template_id, role, content, chapter, section, created_at
		 FROM messages WHERE user_id = ? AND template_id = ? ORDER BY created_at ASC`,
		userID, templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.UserID, &m.TemplateID, &m.Role, &m.Content, &m.Chapter, &m.Section, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// SaveChapterSummaryWithTemplate 保存或覆盖章节摘要（带 template）
func (s *Store) SaveChapterSummaryWithTemplate(userID, templateID string, chapter int, summary string) error {
	if templateID == "" {
		templateID = "campus"
	}
	_, err := s.db.Exec(
		`INSERT INTO chapter_summaries (user_id, template_id, chapter, summary)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, template_id, chapter) DO UPDATE SET summary = excluded.summary`,
		userID, templateID, chapter, summary,
	)
	if err != nil {
		return fmt.Errorf("save chapter summary: %w", err)
	}
	return nil
}

// GetChapterSummariesWithTemplate 返回用户指定模板的章节摘要，按章节正序
func (s *Store) GetChapterSummariesWithTemplate(userID, templateID string) ([]ChapterSummary, error) {
	if templateID == "" {
		templateID = "campus"
	}
	rows, err := s.db.Query(
		`SELECT id, user_id, template_id, chapter, summary, created_at
		 FROM chapter_summaries WHERE user_id = ? AND template_id = ? ORDER BY chapter ASC`,
		userID, templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("get chapter summaries: %w", err)
	}
	defer rows.Close()

	var summaries []ChapterSummary
	for rows.Next() {
		var cs ChapterSummary
		if err := rows.Scan(&cs.ID, &cs.UserID, &cs.TemplateID, &cs.Chapter, &cs.Summary, &cs.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chapter summary: %w", err)
		}
		summaries = append(summaries, cs)
	}
	return summaries, rows.Err()
}

// CreateUserWithPassword 创建或更新带密码的用户（注册/升级用）
// heroine_name 设与 username 相同以兼容 prod DB 中遗留的 NOT NULL 约束；
// 实际渲染时 engine.leadNameForUser 会优先 username 而非 heroine_name。
func (s *Store) CreateUserWithPassword(id, username, passwordHash, defaultTemplate string) error {
	if defaultTemplate == "" {
		defaultTemplate = "campus"
	}
	_, err := s.db.Exec(
		`INSERT INTO users (id, username, password_hash, heroine_name) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET username=excluded.username, password_hash=excluded.password_hash`,
		id, username, passwordHash, username,
	)
	if err != nil {
		return fmt.Errorf("create user with pass: %w", err)
	}
	// ensure a progress row exists for default template
	_, _ = s.db.Exec(
		`INSERT OR IGNORE INTO user_progress (user_id, template_id) VALUES (?, ?)`,
		id, defaultTemplate,
	)
	return nil
}

// GetUserByUsername 按用户名查询（登录用）
func (s *Store) GetUserByUsername(username string) (*User, error) {
	row := s.db.QueryRow(
		`SELECT id, COALESCE(username,''), COALESCE(heroine_name,''), COALESCE(password_hash,''), current_chapter, current_section, created_at FROM users WHERE username = ?`,
		username,
	)
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.HeroineName, &u.PasswordHash, &u.CurrentChapter, &u.CurrentSection, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

// --- migration helper (called from InitDB after schema) ---

func columnExists(db *sql.DB, table, column string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ, dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name.Valid && name.String == column {
			return true
		}
	}
	return false
}

func migrate(db *sql.DB) error {
	// ensure new columns on users
	if !columnExists(db, "users", "username") {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN username TEXT`); err != nil {
			return fmt.Errorf("add username: %w", err)
		}
	}
	if !columnExists(db, "users", "password_hash") {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN password_hash TEXT`); err != nil {
			return fmt.Errorf("add password_hash: %w", err)
		}
	}

	// messages.template_id
	if !columnExists(db, "messages", "template_id") {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN template_id TEXT NOT NULL DEFAULT 'campus'`); err != nil {
			return fmt.Errorf("add messages.template_id: %w", err)
		}
	}

	// messages.image_path（legacy 异步生成的图像相对路径）— 已廢棄，2026-06-27 移除。
	// 為相容舊 DB（升級前已通過遷移加上此列），冪等 DROP COLUMN。
	// SQLite 3.35+ 支持 ALTER TABLE ... DROP COLUMN。
	if columnExists(db, "messages", "image_path") {
		if _, err := db.Exec(`ALTER TABLE messages DROP COLUMN image_path`); err != nil {
			return fmt.Errorf("drop messages.image_path: %w", err)
		}
	}

	// chapter_summaries.template_id
	if !columnExists(db, "chapter_summaries", "template_id") {
		if _, err := db.Exec(`ALTER TABLE chapter_summaries ADD COLUMN template_id TEXT NOT NULL DEFAULT 'campus'`); err != nil {
			return fmt.Errorf("add chapter_summaries.template_id: %w", err)
		}
	}

	// ensure user_progress table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS user_progress (
		user_id TEXT NOT NULL,
		template_id TEXT NOT NULL DEFAULT 'campus',
		current_chapter INTEGER NOT NULL DEFAULT 1,
		current_section INTEGER NOT NULL DEFAULT 1,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, template_id),
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`); err != nil {
		return fmt.Errorf("ensure user_progress: %w", err)
	}

	// backfill
	_, _ = db.Exec(`UPDATE messages SET template_id='campus' WHERE template_id IS NULL OR template_id=''`)
	_, _ = db.Exec(`UPDATE chapter_summaries SET template_id='campus' WHERE template_id IS NULL OR template_id=''`)
	// seed progress from legacy users for campus
	_, _ = db.Exec(`
		INSERT OR IGNORE INTO user_progress (user_id, template_id, current_chapter, current_section)
		SELECT id, 'campus', current_chapter, current_section FROM users
	`)

	// 性能索引：(user_id, template_id) 组合查询在无索引时会全表扫描
	for _, ddl := range []string{
		`CREATE INDEX IF NOT EXISTS idx_messages_user_tpl ON messages(user_id, template_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_user_tpl ON chapter_summaries(user_id, template_id, chapter)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	// companion tables
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS companion_sessions (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     TEXT NOT NULL,
		persona     TEXT NOT NULL DEFAULT 'female',
		title       TEXT NOT NULL DEFAULT '新的对话',
		msg_count   INTEGER NOT NULL DEFAULT 0,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`); err != nil {
		return fmt.Errorf("create companion_sessions: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS companion_messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id  INTEGER NOT NULL,
		user_id     TEXT NOT NULL,
		role        TEXT NOT NULL,
		content     TEXT NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (session_id) REFERENCES companion_sessions(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("create companion_messages: %w", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS companion_memory (
		user_id     TEXT PRIMARY KEY,
		profile     TEXT NOT NULL DEFAULT '{}',
		updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`); err != nil {
		return fmt.Errorf("create companion_memory: %w", err)
	}
	for _, ddl := range []string{
		`CREATE INDEX IF NOT EXISTS idx_comp_sess_user ON companion_sessions(user_id, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_comp_msg_sess ON companion_messages(session_id, created_at)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("create companion index: %w", err)
		}
	}

	return nil
}

// ── Companion DB types ────────────────────────────────────────────────────

type CompanionSession struct {
	ID        int64
	UserID    string
	Persona   string
	Title     string
	MsgCount  int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CompanionMessage struct {
	ID        int64
	SessionID int64
	UserID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

// ── Companion CRUD ────────────────────────────────────────────────────────

func (s *Store) CreateCompanionSession(userID, persona, title string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO companion_sessions (user_id, persona, title) VALUES (?, ?, ?)`,
		userID, persona, title,
	)
	if err != nil {
		return 0, fmt.Errorf("create companion session: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetCompanionSessions(userID string) ([]CompanionSession, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, persona, title, msg_count, created_at, updated_at
		 FROM companion_sessions WHERE user_id = ?
		 ORDER BY updated_at DESC LIMIT 50`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get companion sessions: %w", err)
	}
	defer rows.Close()
	var out []CompanionSession
	for rows.Next() {
		var cs CompanionSession
		if err := rows.Scan(&cs.ID, &cs.UserID, &cs.Persona, &cs.Title, &cs.MsgCount, &cs.CreatedAt, &cs.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan companion session: %w", err)
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

func (s *Store) GetCompanionSession(sessionID int64, userID string) (*CompanionSession, error) {
	row := s.db.QueryRow(
		`SELECT id, user_id, persona, title, msg_count, created_at, updated_at
		 FROM companion_sessions WHERE id = ? AND user_id = ?`,
		sessionID, userID,
	)
	var cs CompanionSession
	err := row.Scan(&cs.ID, &cs.UserID, &cs.Persona, &cs.Title, &cs.MsgCount, &cs.CreatedAt, &cs.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get companion session: %w", err)
	}
	return &cs, nil
}

func (s *Store) DeleteCompanionSession(sessionID int64, userID string) error {
	_, err := s.db.Exec(
		`DELETE FROM companion_sessions WHERE id = ? AND user_id = ?`,
		sessionID, userID,
	)
	return err
}

func (s *Store) UpdateCompanionSessionTitle(sessionID int64, title string) error {
	_, err := s.db.Exec(
		`UPDATE companion_sessions SET title = ? WHERE id = ?`,
		title, sessionID,
	)
	return err
}

func (s *Store) UpdateCompanionSessionTime(sessionID int64) error {
	_, err := s.db.Exec(
		`UPDATE companion_sessions SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		sessionID,
	)
	return err
}

func (s *Store) IncrCompanionMsgCount(sessionID int64) error {
	_, err := s.db.Exec(
		`UPDATE companion_sessions SET msg_count = msg_count + 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		sessionID,
	)
	return err
}

func (s *Store) SaveCompanionMessage(sessionID int64, userID, role, content string) error {
	_, err := s.db.Exec(
		`INSERT INTO companion_messages (session_id, user_id, role, content) VALUES (?, ?, ?, ?)`,
		sessionID, userID, role, content,
	)
	return err
}

func (s *Store) GetCompanionMessages(sessionID int64, userID string, limit int) ([]CompanionMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, session_id, user_id, role, content, created_at
		 FROM companion_messages
		 WHERE session_id = ?
		 ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("get companion messages: %w", err)
	}
	defer rows.Close()
	var out []CompanionMessage
	for rows.Next() {
		var m CompanionMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.UserID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan companion message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *Store) GetCompanionMemoryRaw(userID string) (string, error) {
	var profile string
	err := s.db.QueryRow(
		`SELECT profile FROM companion_memory WHERE user_id = ?`, userID,
	).Scan(&profile)
	if err == sql.ErrNoRows {
		return "{}", nil
	}
	if err != nil {
		return "{}", fmt.Errorf("get companion memory: %w", err)
	}
	return profile, nil
}

func (s *Store) SaveCompanionMemory(userID string, mem any) error {
	data, err := json.Marshal(mem)
	if err != nil {
		return fmt.Errorf("marshal memory: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO companion_memory (user_id, profile) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET profile = excluded.profile, updated_at = CURRENT_TIMESTAMP`,
		userID, string(data),
	)
	return err
}
