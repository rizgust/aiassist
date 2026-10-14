// Package channel defines output surfaces (Telegram, MCP, HTTP API). A
// channel turns inbound messages into core.Engine.Ask calls, building the
// core.Caller and, if it can push files, attaching a core.Deliverer.
package channel

import (
	"context"

	"github.com/rizgust/aiassist/core"
)

// Channel is one output surface.
type Channel interface {
	Name() string
	// Run serves until ctx is cancelled.
	Run(ctx context.Context, engine *core.Engine) error
}
