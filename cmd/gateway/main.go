// Command gateway runs the LLM gateway. main does wiring only; run owns ordering,
// logging and exit codes.
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	context.AfterFunc(ctx, stop) // a second signal gets Go's default behaviour and kills the process
	code := run(ctx, deps{args: os.Args[1:], lookupEnv: os.LookupEnv, stdout: os.Stdout, stdin: os.Stdin, listen: net.Listen})
	stop()
	os.Exit(code)
}
