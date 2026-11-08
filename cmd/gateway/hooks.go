package main

import (
	"errors"
	"flag"
	"io"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

// runHooks is `gateway hooks print`: it writes the Claude Code settings document for
// listen_addr to stdout: the hooks, and with network effects on the env and sandbox
// blocks (spec 014 AC12). It writes no file and binds nothing.
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
	if _, err := d.stdout.Write(claudecode.SettingsDoc(cfg.ListenAddr, netSettings(cfg))); err != nil {
		return exitRuntime
	}
	return exitOK
}

// netSettings is the network part of the printed settings; nil with effects or the
// network sensor off, which prints the hooks alone.
func netSettings(cfg config.Config) *claudecode.NetSettings {
	n := cfg.Effects.Network
	if !cfg.Effects.Enabled || !n.Enabled {
		return nil
	}
	return &claudecode.NetSettings{
		BaseURL:        "http://" + cfg.ListenAddr + anthropic.Adapter{}.Prefix(),
		HTTPProxyAddr:  n.HTTPProxyAddr,
		HTTPProxyPort:  config.ProxyPort(n.HTTPProxyAddr),
		SocksProxyPort: config.ProxyPort(n.SocksProxyAddr),
		Sandbox:        n.Sandbox,
	}
}
