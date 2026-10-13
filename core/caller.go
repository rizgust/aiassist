// Package core is the channel-neutral assistant engine: the tool-use loop,
// model routing with fallback, usage logging, and memory.
package core

import "context"

// Caller identifies who is asking, independent of channel. Channels build
// it (e.g. Telegram user id → ID); tools read it from context so the model
// can never spoof identity through tool input.
type Caller struct {
	ID         string // stable per channel, e.g. "tg:123456"
	Username   string
	Channel    string // "telegram", "mcp", "httpapi", ...
	Privileged bool   // master/whitelisted: may unlock privileged tools
	Attrs      map[string]string
}

type callerKey struct{}

// WithCaller attaches c to ctx.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the Caller set by Engine.Ask (or WithCaller).
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
