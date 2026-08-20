package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	thaumaste "github.com/thelemail/thaumaste/internal"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/service"
)

func newTenantCmd() *cobra.Command {
	tenant := &cobra.Command{
		Use:   "tenant",
		Short: "Manage the domains this server answers for",
	}
	tenant.AddCommand(
		newTenantCreateCmd(),
		newTenantListCmd(),
		newTenantSuspendCmd(),
		newTenantResumeCmd(),
		newTenantDeleteCmd(),
		newTenantRotateKeyCmd(),
	)
	return tenant
}

func newTenantCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <server_name> [host...]",
		Short: "Add a domain, generating its signing key",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withTenants(c.Context(), func(ctx context.Context, tenants service.Tenants) error {
				t, err := tenants.Create(ctx, entity.NewTenant{
					ServerName:       args[0],
					Hosts:            args[1:],
					RegistrationMode: entity.RegistrationClosed,
				})
				if err != nil {
					return err
				}
				keys, err := tenants.Keys(ctx, t.Scope())
				if err != nil {
					return err
				}
				printTenant(c.OutOrStdout(), t)
				for _, k := range keys {
					_, _ = fmt.Fprintf(c.OutOrStdout(), "key      %s\n", k.KeyID)
				}
				return nil
			})
		},
	}
}

func newTenantListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the domains this server answers for",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return withTenants(c.Context(), func(ctx context.Context, tenants service.Tenants) error {
				all, err := tenants.List(ctx)
				if err != nil {
					return err
				}
				for _, t := range all {
					hosts, err := tenants.Hosts(ctx, t.Scope())
					if err != nil {
						return err
					}
					_, _ = fmt.Fprintf(c.OutOrStdout(), "%-40s %-10s %s\n", t.ServerName, t.State, hosts)
				}
				return nil
			})
		},
	}
}

func newTenantSuspendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suspend <server_name>",
		Short: "Stop serving the client API for a domain, keeping its keys published",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withTenant(c, args[0], func(ctx context.Context, tenants service.Tenants, t entity.Tenant) error {
				updated, err := tenants.Suspend(ctx, t.ID)
				if err != nil {
					return err
				}
				printTenant(c.OutOrStdout(), updated)
				return nil
			})
		},
	}
}

func newTenantResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <server_name>",
		Short: "Serve the client API for a suspended domain again",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withTenant(c, args[0], func(ctx context.Context, tenants service.Tenants, t entity.Tenant) error {
				updated, err := tenants.Resume(ctx, t.ID)
				if err != nil {
					return err
				}
				printTenant(c.OutOrStdout(), updated)
				return nil
			})
		},
	}
}

func newTenantDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <server_name>",
		Short: "Remove a domain and everything belonging to it",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withTenant(c, args[0], func(ctx context.Context, tenants service.Tenants, t entity.Tenant) error {
				if err := tenants.Delete(ctx, t.ID); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(c.OutOrStdout(), "deleted %s\n", t.ServerName)
				return nil
			})
		},
	}
}

func newTenantRotateKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-key <server_name>",
		Short: "Issue a new signing key, retaining the old one for verification",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return withTenant(c, args[0], func(ctx context.Context, tenants service.Tenants, t entity.Tenant) error {
				k, err := tenants.RotateKey(ctx, t.Scope())
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(c.OutOrStdout(), "key      %s\n", k.KeyID)
				return nil
			})
		},
	}
}

func withTenants(ctx context.Context, fn func(context.Context, service.Tenants) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	tenants, cleanup, err := thaumaste.InitializeTenants(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(ctx, tenants)
}

func withTenant(c *cobra.Command, serverName string, fn func(context.Context, service.Tenants, entity.Tenant) error) error {
	return withTenants(c.Context(), func(ctx context.Context, tenants service.Tenants) error {
		t, err := tenants.ByServerName(ctx, serverName)
		if err != nil {
			if errors.Is(err, entity.ErrTenantNotFound) {
				return fmt.Errorf("no such server: %s", serverName)
			}
			return err
		}
		return fn(ctx, tenants, t)
	})
}

func printTenant(w io.Writer, t entity.Tenant) {
	_, _ = fmt.Fprintf(w, "server   %s\n", t.ServerName)
	_, _ = fmt.Fprintf(w, "id       %s\n", t.ID)
	_, _ = fmt.Fprintf(w, "state    %s\n", t.State)
}
