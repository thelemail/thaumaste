package cmd

import (
	"context"
	"fmt"

	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/thelemail/thaumaste/db"
	"github.com/thelemail/thaumaste/internal/config"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
)

func newMigrateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Apply, roll back or inspect database migrations",
	}
	c.AddCommand(newMigrateUpCmd(), newMigrateDownCmd(), newMigrateStatusCmd())
	return c
}

func newMigrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(c *cobra.Command, _ []string) error {
			return runMigrate(c.Context(), func(ctx context.Context, p *goose.Provider) error {
				_, err := p.Up(ctx)
				return err
			})
		},
	}
}

func newMigrateDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Roll back the most recent migration",
		RunE: func(c *cobra.Command, _ []string) error {
			return runMigrate(c.Context(), func(ctx context.Context, p *goose.Provider) error {
				_, err := p.Down(ctx)
				return err
			})
		},
	}
}

func newMigrateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show applied and pending migrations",
		RunE: func(c *cobra.Command, _ []string) error {
			return runMigrate(c.Context(), func(ctx context.Context, p *goose.Provider) error {
				sources, err := p.Status(ctx)
				if err != nil {
					return err
				}
				for _, s := range sources {
					state := "pending"
					if s.State == goose.StateApplied {
						state = "applied"
					}
					_, _ = fmt.Fprintf(c.OutOrStdout(), "%-8s %s\n", state, s.Source.Path)
				}
				return nil
			})
		},
	}
}

func runMigrate(ctx context.Context, op func(context.Context, *goose.Provider) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	pg, err := postgres.NewMigrator(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer func() { _ = pg.Close() }()

	p, err := db.Provider(pg.DB)
	if err != nil {
		return err
	}
	return op(ctx, p)
}

func migrateUp(ctx context.Context, cfg config.Postgres) error {
	pg, err := postgres.NewMigrator(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = pg.Close() }()

	p, err := db.Provider(pg.DB)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
