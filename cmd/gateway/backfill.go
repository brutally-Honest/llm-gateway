package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// backfillBatch is how many exchanges one transaction keys.
const backfillBatch = 500

// backfillCounts is what a backfill did.
type backfillCounts struct{ filled, noKey, skipped int }

// runBackfill is `gateway backfill-sessions`: it gives each stored exchange that has
// no session key the key its client's profile reads from the stored request headers.
func runBackfill(d deps) int {
	boot := logging.Bootstrap(d.stdout)
	flags := flag.NewFlagSet("backfill-sessions", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "path to the config file (default ./config.yaml if present)")
	switch err := flags.Parse(d.args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		boot.Info("usage", zap.Strings("forms", []string{"backfill-sessions [-config <path>]"}))
		return exitOK
	case err != nil:
		boot.Error("invalid flags")
		return exitConfig
	case flags.NArg() > 0:
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", flags.NArg()))
		return exitConfig
	}

	adapters, profiles := registered()
	cfg, _, ok := loadConfig(boot, *configPath, d.lookupEnv, adapters)
	if !ok {
		return exitConfig
	}
	log := logging.New(d.stdout, cfg.LogLevel)
	dir, ok := captureDir(cfg.Capture, d.lookupEnv, log)
	if !ok {
		return exitConfig
	}
	// store.Open creates what is missing; backfill must never create a store.
	if _, err := os.Stat(filepath.Join(dir, store.DBName)); err != nil {
		reason := "cannot read store"
		if errors.Is(err, os.ErrNotExist) {
			reason = "store not found"
		}
		log.Error("backfill-sessions failed", zap.String("path", dir), zap.String("reason", reason))
		return exitRuntime
	}
	st, err := store.Open(dir, log)
	if err != nil {
		log.Error("backfill-sessions failed", zap.String("path", dir),
			zap.String("reason", storeOpenReason(err)), zap.Error(err))
		return exitRuntime
	}
	defer func() { _ = st.Close() }()

	keyers := map[string]core.SessionKeyer{}
	for _, p := range profiles {
		if k, isKeyer := p.(core.SessionKeyer); isKeyer {
			keyers[p.Name()] = k
		}
	}
	c, err := backfill(context.Background(), st, keyers)
	if err != nil {
		log.Error("backfill-sessions failed", zap.String("path", dir),
			zap.String("reason", "cannot update store"), zap.Error(err))
		return exitRuntime
	}
	if _, err := fmt.Fprintf(d.stdout, "filled %d, left without key %d, skipped %d (no profile registered)\n",
		c.filled, c.noKey, c.skipped); err != nil {
		log.Error("backfill-sessions failed", zap.String("reason", "cannot write output"), zap.Error(err))
		return exitRuntime
	}
	return exitOK
}

// backfill walks the exchanges without a key in batches. Rows it cannot key stay
// NULL, so it pages by rowid rather than re-reading the first batch.
func backfill(ctx context.Context, st *store.Store, keyers map[string]core.SessionKeyer) (backfillCounts, error) {
	var c backfillCounts
	var after int64
	for {
		batch, err := st.UnkeyedBatch(ctx, after, backfillBatch)
		if err != nil {
			return c, err
		}
		if len(batch) == 0 {
			return c, nil
		}
		after = batch[len(batch)-1].Rowid
		var as []store.KeyAssignment
		for _, u := range batch {
			k, ok := keyers[u.Client]
			if !ok {
				c.skipped++
				continue
			}
			key, present := k.SessionKey(u.RequestHeaders)
			if !present || core.ValidSessionKey(key) != "" {
				c.noKey++
				continue
			}
			as = append(as, store.KeyAssignment{RequestID: u.RequestID, Key: key})
		}
		n, err := st.SetSessionKeys(ctx, as)
		if err != nil {
			return c, err
		}
		c.filled += n
	}
}
