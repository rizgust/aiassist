package postgres

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/Nuanu-com/aiassist/core"
)

// Conversation implements core.ConversationStore.
type Conversation struct{ DB DB }

func (s *Conversation) Append(ctx context.Context, callerID string, role core.TurnRole, content string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO ai_conversation (caller_id, role, content) VALUES ($1, $2, $3)`,
		callerID, string(role), content)
	return err
}

func (s *Conversation) Recent(ctx context.Context, callerID string, limit int) ([]core.Turn, error) {
	rows, err := s.DB.Query(ctx, `SELECT role, content, created_at FROM ai_conversation
		WHERE caller_id = $1 ORDER BY id DESC LIMIT $2`, callerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Turn
	for rows.Next() {
		var t core.Turn
		var role string
		if err := rows.Scan(&role, &t.Content, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Role = core.TurnRole(role)
		out = append(out, t)
	}
	slices.Reverse(out)
	return out, rows.Err()
}

// GlobalMemory implements core.GlobalMemoryStore.
type GlobalMemory struct{ DB DB }

func (s *GlobalMemory) List(ctx context.Context) ([]core.MemoryEntry, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, content, saved_by, created_at FROM ai_global_memory ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.MemoryEntry
	for rows.Next() {
		var e core.MemoryEntry
		if err := rows.Scan(&e.ID, &e.Content, &e.SavedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *GlobalMemory) Save(ctx context.Context, content, savedBy string) (core.MemoryEntry, error) {
	e := core.MemoryEntry{Content: content, SavedBy: savedBy}
	err := s.DB.QueryRow(ctx, `INSERT INTO ai_global_memory (content, saved_by) VALUES ($1, $2)
		RETURNING id, created_at`, content, savedBy).Scan(&e.ID, &e.CreatedAt)
	return e, err
}

// UserMemory implements core.UserMemoryStore.
type UserMemory struct{ DB DB }

func (s *UserMemory) Get(ctx context.Context, callerID string) (string, bool, error) {
	var content string
	err := s.DB.QueryRow(ctx, `SELECT content FROM ai_user_memory WHERE caller_id = $1`, callerID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content, true, nil
}

func (s *UserMemory) Upsert(ctx context.Context, callerID, content string) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO ai_user_memory (caller_id, content) VALUES ($1, $2)
		ON CONFLICT (caller_id) DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()`, callerID, content)
	return err
}

var (
	_ core.ConversationStore = (*Conversation)(nil)
	_ core.GlobalMemoryStore = (*GlobalMemory)(nil)
	_ core.UserMemoryStore   = (*UserMemory)(nil)
)
