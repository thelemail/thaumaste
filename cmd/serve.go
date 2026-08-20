package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	thaumaste "github.com/thelemail/thaumaste/internal"
	"github.com/thelemail/thaumaste/internal/runtime"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the client-server API",
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.Postgres.MigrateOnStart {
				if err := migrateUp(c.Context(), cfg.Postgres); err != nil {
					return fmt.Errorf("apply migrations: %w", err)
				}
			}
			rt, cleanup, err := thaumaste.InitializeServe(c.Context(), cfg)
			if err != nil {
				return err
			}
			defer cleanup()
			return runtime.Run(c.Context(), cfg.Server.ShutdownTimeout, runtimeOpts(cfg, rt.Ready), rt)
		},
	}
}
