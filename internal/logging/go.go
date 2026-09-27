package logging

import (
	"errors"

	"go.uber.org/zap"
)

// ErrPanicked is what Go delivers when fn panicked instead of returning.
var ErrPanicked = errors.New("goroutine panicked")

// Go runs fn on a new goroutine and delivers its error on the returned channel, which
// has room for that one value, so the goroutine never blocks on a reader that has
// gone. A panic in fn is recovered and logged through LogPanic (type and stack, never
// the value) as one JSON line naming the goroutine, and the channel then delivers
// ErrPanicked, so a caller waiting on it is never left waiting forever. This is the
// one place in the gateway allowed a go statement.
func Go(log *zap.Logger, name string, fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() {
		defer func() {
			// Since Go 1.21 panic(nil) recovers as *runtime.PanicNilError, so a nil
			// here means fn returned normally.
			if v := recover(); v != nil {
				LogPanic(log, "goroutine panicked", v, zap.String("goroutine", name))
				ch <- ErrPanicked
			}
		}()
		ch <- fn()
	}()
	return ch
}
