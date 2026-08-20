package cmd

import (
	"context"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "thaumaste",
		Short:         "Thaumaste — a multi-tenant Matrix homeserver",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newVersionCmd())
	return root
}

func Execute(ctx context.Context) error {
	return newRootCmd().ExecuteContext(ctx)
}
