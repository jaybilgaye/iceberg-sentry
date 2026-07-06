// Package exitcode defines the standard process exit codes emitted by the
// iceberg-sentry CLI. Exit codes are part of the public CI/CD contract.
package exitcode

const (
	OK             = 0
	Warning        = 1
	Critical       = 2
	UntaggedPII    = 3
	ConfigError    = 4
	ConnectionFail = 5
)
