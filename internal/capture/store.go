// Package capture is the capture sink: a bounded queue of finished exchanges, drained
// by worker goroutines that store them through a Store. It knows no provider and no
// client; it reads only core's exchanges and canonical events.
package capture

import (
	"context"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// Store is what the workers write through. SaveExchange stores the raw exchange and
// both bodies; SaveParse stores the canonical events and their content, and records
// the exchange's parse status. Each call honours ctx, which the sink cancels when
// shutdown runs out of time.
type Store interface {
	SaveExchange(ctx context.Context, ex *core.Exchange) error
	SaveParse(ctx context.Context, requestID, principalID string,
		status core.ParseStatus, events []core.StoredEvent, contents []core.Content) error
	Close() error
}
