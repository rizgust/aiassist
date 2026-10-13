package core

import (
	"context"
	"sync"
)

// File is a rendered attachment.
type File struct {
	Name string
	MIME string
	Data []byte
}

// Output is something a tool pushes to the caller mid-answer (a PDF, an
// image), separate from the final text answer.
type Output struct {
	Text    string
	File    *File
	Caption string
}

// Deliverer pushes Output to a caller over the channel that is serving
// them. Channels attach one per request with WithDeliverer.
type Deliverer interface {
	Deliver(ctx context.Context, to Caller, out Output) error
}

// DelivererFunc adapts a function to Deliverer.
type DelivererFunc func(ctx context.Context, to Caller, out Output) error

func (f DelivererFunc) Deliver(ctx context.Context, to Caller, out Output) error {
	return f(ctx, to, out)
}

type delivererKey struct{}

// WithDeliverer attaches d to ctx.
func WithDeliverer(ctx context.Context, d Deliverer) context.Context {
	return context.WithValue(ctx, delivererKey{}, d)
}

// DelivererFrom returns the channel's Deliverer, if the channel supports
// pushing output (MCP, for example, does not).
func DelivererFrom(ctx context.Context) (Deliverer, bool) {
	d, ok := ctx.Value(delivererKey{}).(Deliverer)
	return d, ok
}

// deliveryState dedups pushes within one Ask: a single assistant turn can
// contain several identical tool_use blocks, and each must not trigger its
// own upload.
type deliveryState struct {
	mu   sync.Mutex
	sent map[string]bool
}

type deliveryStateKey struct{}

// ContextWithDeliveryState starts a fresh dedup scope. Engine.Ask does this
// per question; channels that execute tools directly (outside Ask) call it
// per request.
func ContextWithDeliveryState(ctx context.Context) context.Context {
	return context.WithValue(ctx, deliveryStateKey{}, &deliveryState{sent: map[string]bool{}})
}

// ClaimDelivery returns true the first time key is claimed within the
// current Ask, false afterwards. Outside an Ask it always returns true.
func ClaimDelivery(ctx context.Context, key string) bool {
	s, ok := ctx.Value(deliveryStateKey{}).(*deliveryState)
	if !ok {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[key] {
		return false
	}
	s.sent[key] = true
	return true
}

// ReleaseDelivery un-claims key, e.g. after a failed send so a retry within
// the same Ask may try again.
func ReleaseDelivery(ctx context.Context, key string) {
	if s, ok := ctx.Value(deliveryStateKey{}).(*deliveryState); ok {
		s.mu.Lock()
		delete(s.sent, key)
		s.mu.Unlock()
	}
}
