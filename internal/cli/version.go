package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the iceberg-sentry version",
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "iceberg-sentry %s\n", info.Version)
			fmt.Fprintf(w, "  commit:     %s\n", info.Commit)
			fmt.Fprintf(w, "  built:      %s\n", info.BuildDate)
			fmt.Fprintf(w, "  go:         %s\n", runtime.Version())
			fmt.Fprintf(w, "  platform:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
