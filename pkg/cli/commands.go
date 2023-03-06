package cli

import (
	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "wolfictl",
		DisableAutoGenTag: true,
		SilenceUsage:      true,
		Short:             "A CLI helper for developing Wolfi",
	}

	cmd.AddCommand(
		Advisory(),
		Bump(),
		Gh(),
		Apk(),
		Index(),
		GenerateIndex(),
		cmdPod(),
		cmdSVG(),
		cmdText(),
		Check(),
		Lint(),
		Update(),
		VEX(),
	)

	return cmd
}
