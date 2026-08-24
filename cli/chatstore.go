package cli

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// ChatStore persists chat conversations in a dedicated SQLite database.
//
// Schema: a chats row per conversation plus one messages row per turn, so
// history survives restarts and is shared by every browser that points at
// this webui instance (unlike the previous localStorage approach).
type ChatStore struct {
	db *sql.DB
}

// ChatMessage is a single stored turn of a conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatMeta mirrors one chats row. Timestamps are unix milliseconds in JSON
// (what the frontend expects) and unix seconds in the database.
type ChatMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`

	CreatedAtSec int64 `json:"-"`
}

// DefaultChatDBPath resolves where the conversation database lives:
// $HAILO_OLLAMA_DB if set, otherwise <user cache dir>/hailo-ollama/chats.db.
func DefaultChatDBPath() string {
	if p := os.Getenv("HAILO_OLLAMA_DB"); p != "" {
		return p
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "hailo-ollama", "chats.db")
}

// OpenChatStore opens (creating if needed) the SQLite database at path.
func OpenChatStore(path string) (*ChatStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty chat database path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open chat database: %w", err)
	}
	// WAL survives concurrent reads during writes; busy_timeout avoids
	// spurious SQLITE_BUSY under parallel requests.
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply pragma: %w", err)
		}
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS chats (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
			seq     INTEGER NOT NULL,
			role    TEXT NOT NULL,
			content TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_chat ON messages(chat_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_chats_updated ON chats(updated_at DESC)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("init chat schema: %w", err)
		}
	}
	return &ChatStore{db: db}, nil
}

func (s *ChatStore) Close() error { return s.db.Close() }

func nowSec() int64 { return time.Now().Unix() }

func secToMs(sec int64) int64 {
	if sec == 0 {
		return 0
	}
	return sec * 1000
}

func newChatID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("c%d", time.Now().UnixNano())
	}
	return "c" + hex.EncodeToString(b)
}

// ListChats returns every conversation, newest activity first.
func (s *ChatStore) ListChats() ([]ChatMeta, error) {
	rows, err := s.db.Query(
		`SELECT id, title, created_at, updated_at FROM chats ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ChatMeta, 0, 16)
	for rows.Next() {
		var m ChatMeta
		var created, updated int64
		if err := rows.Scan(&m.ID, &m.Title, &created, &updated); err != nil {
			return nil, err
		}
		m.CreatedAt = secToMs(created)
		m.UpdatedAt = secToMs(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetChat returns one conversation with its full message list.
func (s *ChatStore) GetChat(id string) (*ChatMeta, []ChatMessage, error) {
	var m ChatMeta
	err := s.db.QueryRow(
		`SELECT id, title, created_at, updated_at FROM chats WHERE id = ?`, id).
		Scan(&m.ID, &m.Title, &m.CreatedAtSec, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, os.ErrNotExist
	}
	if err != nil {
		return nil, nil, err
	}
	m.CreatedAt = secToMs(m.CreatedAtSec)
	m.UpdatedAt = secToMs(m.UpdatedAt)

	rows, err := s.db.Query(
		`SELECT role, content FROM messages WHERE chat_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	msgs := make([]ChatMessage, 0, 16)
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.Role, &msg.Content); err != nil {
			return nil, nil, err
		}
		msgs = append(msgs, msg)
	}
	return &m, msgs, rows.Err()
}

// SaveChat upserts the conversation and atomically replaces its message list.
// A zero meta.ID gets a freshly generated one.
func (s *ChatStore) SaveChat(meta *ChatMeta, msgs []ChatMessage) error {
	if meta.ID == "" {
		meta.ID = newChatID()
	}
	now := nowSec()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO chats (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
		meta.ID, meta.Title, now, now)
	if err != nil {
		return err
	}

	if _, err = tx.Exec(`DELETE FROM messages WHERE chat_id = ?`, meta.ID); err != nil {
		return err
	}
	for seq, msg := range msgs {
		if _, err := tx.Exec(
			`INSERT INTO messages (chat_id, seq, role, content) VALUES (?, ?, ?, ?)`,
			meta.ID, seq, msg.Role, msg.Content); err != nil {
			return err
		}
	}
	meta.UpdatedAt = secToMs(now)
	return tx.Commit()
}

// DeleteChat removes a conversation and all of its messages.
func (s *ChatStore) DeleteChat(id string) error {
	res, err := s.db.Exec(`DELETE FROM chats WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return os.ErrNotExist
	}
	return nil
}

// ---------- HTTP handlers (registered in RunWebUI) ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func handleListChats(store *ChatStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		chats, err := store.ListChats()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, chats)
	}
}

func handleGetChat(store *ChatStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta, msgs, err := store.GetChat(r.PathValue("id"))
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "chat not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, struct {
			ID       string        `json:"id"`
			Title    string        `json:"title"`
			Messages []ChatMessage `json:"messages"`
		}{ID: meta.ID, Title: meta.Title, Messages: msgs})
	}
}

// handleSaveChat accepts {id?, title?, messages:[{role,content}]} and upserts
// the whole conversation, returning its id.
func handleSaveChat(store *ChatStore) http.HandlerFunc {
	type saveRequest struct {
		ID       string        `json:"id"`
		Title    string        `json:"title"`
		Messages []ChatMessage `json:"messages"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req saveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		msgs := make([]ChatMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			if m.Role != "" || m.Content != "" {
				msgs = append(msgs, ChatMessage{Role: m.Role, Content: m.Content})
			}
		}
		if len(msgs) == 0 {
			http.Error(w, "messages are required", http.StatusBadRequest)
			return
		}
		meta := &ChatMeta{ID: strings.TrimSpace(req.ID), Title: strings.TrimSpace(req.Title)}
		if err := store.SaveChat(meta, msgs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"id": meta.ID})
	}
}

func handleDeleteChat(store *ChatStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := store.DeleteChat(r.PathValue("id"))
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "chat not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}
