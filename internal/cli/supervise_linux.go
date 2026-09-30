package cli

import (
	"github.com/spf13/cobra"

	"github.com/yasyf/daemonkit"

	"github.com/yasyf/cc-review/internal/runtimeconfig"
)

func platformCmds() []*cobra.Command {
	return []*cobra.Command{newSuperviseCmd()}
}

func newSuperviseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "supervise",
		Short:  "Supervise the review daemon in the foreground",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := runtimeconfig.Spec()
			if err != nil {
				return err
			}
			return daemonkit.Supervise(cmd.Context(), spec.Label)
		},
	}
}
