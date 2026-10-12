package main

import (
	"database/sql"
	"fmt"

	"github.com/spf13/cobra"
)

const maxConnectionsFlagName = "max-connections"

// maxConnections is the --max-connections the running command was given: the most connections any one pool the
// command opens may hold. Zero leaves every pool its own default. It is reset each time the root command is built.
var maxConnections int

// addMaxConnectionsFlag registers --max-connections on the root command, so every command that connects honors it.
func addMaxConnectionsFlag(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().IntVar(&maxConnections, maxConnectionsFlagName, 0,
		"The most connections any one connection pool may hold: the pool to each database the command reads or "+
			"migrates, to the temporary database's server and to each temporary database. Planning holds a "+
			"connection to the temporary database's server while it asks for another, so the least is 2. Unset, "+
			"each pool keeps its default.")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed(maxConnectionsFlagName) && maxConnections < 2 {
			return fmt.Errorf("--%s must be at least 2: planning holds a connection of a pool while it asks the "+
				"same pool for another", maxConnectionsFlagName)
		}
		return nil
	}
}

// capConnections holds a pool to --max-connections, when it is set.
func capConnections(db *sql.DB) {
	if maxConnections > 0 {
		db.SetMaxOpenConns(maxConnections)
	}
}

// maxOpenConnections is n, or --max-connections when that is lower.
func maxOpenConnections(n int) int {
	if maxConnections > 0 && maxConnections < n {
		return maxConnections
	}
	return n
}
