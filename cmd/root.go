package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/health"
	"github.com/thelemail/thaumaste/internal/runtime"
)

var (
	cfgOnce sync.Once
	cfgVal  config.Config
	cfgErr  error
)

func loadConfig() (config.Config, error) {
	cfgOnce.Do(func() {
		cfgVal, cfgErr = config.Load()
		if cfgErr != nil {
			cfgErr = fmt.Errorf("load config: %w", cfgErr)
			return
		}
		setupLogger(cfgVal.Logger)
	})
	return cfgVal, cfgErr
}

func setupLogger(cfg config.Logger) {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func runtimeOpts(cfg config.Config, ready health.ReadyFunc) runtime.Options {
	return runtime.Options{
		HealthAddr: cfg.Health.Addr,
		DrainDelay: cfg.Health.DrainDelay,
		Ready:      ready,
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "thaumaste",
		Short:         "Thaumaste — a multi-tenant Matrix homeserver",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newServeCmd(), newMigrateCmd(), newTenantCmd(), newKeysCmd(), newVersionCmd())
	return root
}

func Execute(ctx context.Context) error {
	return newRootCmd().ExecuteContext(ctx)
}
