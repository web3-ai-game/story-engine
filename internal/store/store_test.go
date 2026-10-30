package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer st.Close()

	// legacy CreateUser still works
	if err := st.CreateUser("hui", "夏晚棠"); err != nil {
		t.Fatalf("create hui: %v", err)
	}
	if err := st.CreateUser("wen", "沈晚"); err != nil {
		t.Fatalf("create wen: %v", err)
	}
	if u, err := st.GetUser("hui"); err != nil {
		t.Fatalf("get hui: %v", err)
	} else if u.ID != "hui" || u.HeroineName != "夏晚棠" {
		t.Errorf("hui mismatch: %+v", u)
	}

	// legacy progress path
	if err := st.UpdateUserProgress("hui", 2, 3); err != nil {
		t.Fatalf("UpdateUserProgress legacy: %v", err)
	}
	p, err := st.GetUserProgress("hui", "campus")
	if err != nil || p == nil {
		t.Fatalf("GetUserProgress: %v %v", err, p)
	}
	if p.CurrentChapter != 2 || p.CurrentSection != 3 {
		t.Fatalf("progress mismatch: %+v", p)
	}

	// Save/Get message with template
	if err := st.SaveMessageWithTemplate("hui", "campus", "user", "hi", 2, 3); err != nil {
		t.Fatalf("SaveMessageWithTemplate: %v", err)
	}
	msgs, err := st.GetMessagesWithTemplate("hui", "campus")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetMessagesWithTemplate: %v %d", err, len(msgs))
	}
	if msgs[0].TemplateID != "campus" || msgs[0].Content != "hi" {
		t.Fatalf("msg content mismatch: %+v", msgs[0])
	}

	// summary
	if err := st.SaveChapterSummaryWithTemplate("hui", "campus", 1, "摘要A"); err != nil {
		t.Fatalf("SaveChapterSummaryWithTemplate: %v", err)
	}
	sums, err := st.GetChapterSummariesWithTemplate("hui", "campus")
	if err != nil || len(sums) != 1 {
		t.Fatalf("GetChapterSummariesWithTemplate: %v %d", err, len(sums))
	}

	// new user with password
	if err := st.CreateUserWithPassword("new1", "alice", "hash123", "ceo"); err != nil {
		t.Fatalf("CreateUserWithPassword: %v", err)
	}
	uu, err := st.GetUserByUsername("alice")
	if err != nil || uu == nil || uu.PasswordHash != "hash123" {
		t.Fatalf("GetUserByUsername: %v %+v", err, uu)
	}
	pp, _ := st.GetUserProgress("new1", "ceo")
	if pp == nil || pp.CurrentChapter != 1 {
		t.Fatalf("default progress for new template user not seeded: %+v", pp)
	}
}

func TestMigrateExistingDB(t *testing.T) {
	// copy the backed up prod db into temp and ensure migrate runs without error
	src := "../../data/universe.db.backup-2026-06-25-before-step2"
	if _, err := os.Stat(src); err != nil {
		t.Skip("no backup db, skipping migration test")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "universe.db")
	data, _ := os.ReadFile(src)
	_ = os.WriteFile(dst, data, 0644)

	st, err := InitDB(dst)
	if err != nil {
		t.Fatalf("InitDB on migrated copy: %v", err)
	}
	st.Close()
}