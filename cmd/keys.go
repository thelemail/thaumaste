package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thelemail/thaumaste/internal/pkg/keyseal"
	"github.com/thelemail/thaumaste/internal/service"
)

func newKeysCmd() *cobra.Command {
	keys := &cobra.Command{
		Use:   "keys",
		Short: "Manage the key that protects every domain's signing key",
	}
	keys.AddCommand(newKeysResealCmd())
	return keys
}

func newKeysResealCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reseal",
		Short: "Re-encrypt every stored signing key under THAUMASTE_SIGNING_NEXT_MASTER_KEY",
		Long: "Re-encrypt every stored signing key under THAUMASTE_SIGNING_NEXT_MASTER_KEY.\n\n" +
			"Run this with the server stopped, then move the new value into\n" +
			"THAUMASTE_SIGNING_MASTER_KEY and start it again. Nothing is written unless every\n" +
			"key opens under the current master key first.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.Signing.NextMasterKey == "" {
				return fmt.Errorf("set THAUMASTE_SIGNING_NEXT_MASTER_KEY to the new master key")
			}
			if cfg.Signing.NextMasterKey == cfg.Signing.MasterKey {
				return fmt.Errorf("the next master key is the one already in use")
			}
			next, err := keyseal.NewFromEncoded(cfg.Signing.NextMasterKey)
			if err != nil {
				return err
			}
			return withTenants(c.Context(), func(ctx context.Context, tenants service.Tenants) error {
				n, err := tenants.ResealKeys(ctx, next)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(c.OutOrStdout(), "resealed %d keys\n", n)
				_, _ = fmt.Fprintln(c.OutOrStdout(), "move the new value into THAUMASTE_SIGNING_MASTER_KEY before starting the server")
				return nil
			})
		},
	}
}
