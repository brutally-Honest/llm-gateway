package main

import (
	"errors"
	"flag"
	"io"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// runHooks is `gateway hooks print`: it writes the Claude Code settings snippet for
// listen_addr to stdout. It writes no file and binds nothing.
func runHooks(d deps) int {
	boot := logging.Bootstrap(d.stdout)
	if len(d.args) < 2 || d.args[1] != "print" {
		boot.Error("invalid flags", zap.String("reason", "usage: hooks print [-config <path>]"))
		return exitConfig
	}
	flags := flag.NewFlagSet("hooks print", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "path to the config file (default ./config.yaml if present)")
	switch err := flags.Parse(d.args[2:]); {
	case errors.Is(err, flag.ErrHelp):
		boot.Info("usage", zap.Strings("forms", []string{"hooks print [-config <path>]"}))
		return exitOK
	case err != nil:
		boot.Error("invalid flags")
		return exitConfig
	case flags.NArg() > 0:
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", flags.NArg()))
		return exitConfig
	}
	adapters, _ := registered()
	cfg, _, ok := loadConfig(boot, *configPath, d.lookupEnv, adapters)
	if !ok {
		return exitConfig
	}
	if _, err := d.stdout.Write(claudecode.HookSnippet(cfg.ListenAddr)); err != nil {
		return exitRuntime
	}
	return exitOK
}
