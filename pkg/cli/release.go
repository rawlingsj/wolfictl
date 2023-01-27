package cli

import (
	"github.com/spf13/cobra"
	"github.com/wolfi-dev/wolfictl/pkg/gh"
)

func Release() *cobra.Command {

	gitOpts := gh.New()

	cmd := &cobra.Command{
		Use:               "release",
		DisableAutoGenTag: true,
		SilenceUsage:      true,
		Short:             "performs a GitHub release using git tags to calculate the release version",
		Args:              cobra.RangeArgs(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {

			return gitOpts.Release(args[0])

		},
	}

	return cmd
}
