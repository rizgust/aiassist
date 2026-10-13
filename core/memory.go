package core

import (
	"context"
	"time"
)

// TurnRole is who wrote a conversation turn.
type TurnRole string

const (
	TurnUser      TurnRole = "user"
	TurnAssistant TurnRole = "assistant"
)

// Turn is one stored conversation message.
type Turn struct {
	Role      TurnRole
	Content   string
	CreatedAt time.Time
}

// ConversationStore keeps per-caller history.
type ConversationStore interface {
	Append(ctx context.Context, callerID string, role TurnRole, content string) error
	// Recent returns up to limit most recent turns, oldest first.
	Recent(ctx context.Context, callerID string, limit int) ([]Turn, error)
}

// MemoryEntry is one durable cross-caller fact.
type MemoryEntry struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	SavedBy   string    `json:"saved_by"`
	CreatedAt time.Time `json:"created_at"`
}

// GlobalMemoryStore keeps durable facts injected for every caller.
type GlobalMemoryStore interface {
	List(ctx context.Context) ([]MemoryEntry, error)
	Save(ctx context.Context, content, savedBy string) (MemoryEntry, error)
}

// UserMemoryStore keeps one evolving profile per caller.
type UserMemoryStore interface {
	Get(ctx context.Context, callerID string) (content string, found bool, err error)
	Upsert(ctx context.Context, callerID, content string) error
}
