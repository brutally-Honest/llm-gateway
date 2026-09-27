package logging

import (
	"fmt"
	"runtime/debug"

	"go.uber.org/zap"
)

// LogPanic writes one error line for a recovered panic: the value's type (%T) and the
// stack, never the value, which can hold request data. debug.Stack prints arguments
// as words and pointers, so a string argument shows an address, not its bytes. Call it
// from the deferred function that recovered, so the stack still shows the panic.
func LogPanic(log *zap.Logger, msg string, v any, fields ...zap.Field) {
	fields = append(fields,
		zap.String("panic_type", fmt.Sprintf("%T", v)),
		zap.String("stack", string(debug.Stack())),
	)
	log.Error(msg, fields...)
}
