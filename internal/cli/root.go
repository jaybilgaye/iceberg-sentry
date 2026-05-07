// Package cli wires the iceberg-sentry CLI commands together.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the root command with the supplied args and returns the
// process exit code plus any unexpected error encountered. Exit codes follow
// the spec in internal/exitcode.
func Execute(args []string, version string) (int, error) {
	root := newRootCmd(version)
	root.SetArgs(args)
	root.SilenceErrors = true
	root.SilenceUsage = true

	exitCode := 0
	root.PersistentPostRunE = func(cmd *cobra.Command, _ []string) error {
		if v, ok := cmd.Context().Value(exitCodeKey{}).(*int); ok && v != nil {
			exitCode = *v
		}
		return nil
	}

	if err := root.Execute(); err != nil {
		if ce, ok := err.(*codedError); ok {
			return ce.code, ce.err
		}
		return 0, err
	}
	return exitCode, nil
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "iceberg-sentry",
		Short:         "Iceberg-native lakehouse reliability linter",
		Long:          "iceberg-sentry audits Apache Iceberg tables for physical, metadata, performance, and cost-efficiency issues without requiring a running compute engine.",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(
		newAuditCmd(),
		newVersionCmd(version),
	)
	return root
}

// codedError lets a command short-circuit Execute with a specific exit code.
type codedError struct {
	code int
	err  error
}

func (c *codedError) Error() string {
	if c.err == nil {
		return ""
	}
	return c.err.Error()
}

func (c *codedError) Unwrap() error { return c.err }

type exitCodeKey struct{}
