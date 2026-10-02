package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/yokonao/cc-prguard/internal/prguard"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// Claude Code treats exit 2 as a blocking error, so failures exit with 1.
func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "cc-prguard",
		Short:        "Claude Code PreToolUse hook that checks gh pr create against per-repository rules",
		Version:      version,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return prguard.Run(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}
