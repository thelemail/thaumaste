package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build revision",
		RunE: func(c *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(c.OutOrStdout(), buildVersion())
			return nil
		},
	}
}

func buildVersion() string {
	rev, suffix := "unknown", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					suffix = "+dirty"
				}
			}
		}
	}
	return fmt.Sprintf("thaumaste %s%s (%s)", rev, suffix, runtime.Version())
}
