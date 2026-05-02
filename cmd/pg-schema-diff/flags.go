package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-logfmt/logfmt"
	"github.com/jackc/pgx/v4"
	"github.com/spf13/cobra"
)

// compileExcludeNameRegexes compiles the raw `--exclude-name-regex` strings
// into regexp.Regexp values. Used by both `plan` and `dump` commands so the
// failure mode (invalid regex) is reported with the same wording from either
// path.
func compileExcludeNameRegexes(raw []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(raw))
	for _, r := range raw {
		re, err := regexp.Compile(r)
		if err != nil {
			return nil, fmt.Errorf("compiling --exclude-name-regex %q: %w", r, err)
		}
		out = append(out, re)
	}
	return out, nil
}

type connectionFlags struct {
	// dsn is the connection string for the database.
	dsn         string
	dsnFlagName string

	// isEmptyDsnUsingPq indicates to connect via DSN using the pq environment variables and defaults.
	isEmptyDsnUsingPq         bool
	isEmptyDsnUsingPqFlagName string
}

func createConnectionFlags(cmd *cobra.Command, prefix string, additionalHelp string) *connectionFlags {
	var c connectionFlags

	c.dsnFlagName = prefix + "dsn"
	dsnFlagHelp := "Connection string for the database (DB password can be specified through PGPASSWORD environment variable)."
	if additionalHelp != "" {
		dsnFlagHelp += " " + additionalHelp
	}
	cmd.Flags().StringVar(&c.dsn, c.dsnFlagName, "", dsnFlagHelp)

	c.isEmptyDsnUsingPqFlagName = prefix + "empty-dsn"
	isEmptyDsnUsingPqFlagHelp := "Connect with an empty DSN using the pq environment variables and defaults."
	if additionalHelp != "" {
		isEmptyDsnUsingPqFlagHelp += " " + additionalHelp
	}
	cmd.Flags().BoolVar(&c.isEmptyDsnUsingPq, c.isEmptyDsnUsingPqFlagName, false, isEmptyDsnUsingPqFlagHelp)

	return &c
}

func (c *connectionFlags) IsSet() bool {
	return c.dsn != "" || c.isEmptyDsnUsingPq
}

func parseConnectionFlags(flags *connectionFlags) (*pgx.ConnConfig, error) {
	if !flags.isEmptyDsnUsingPq && flags.dsn == "" {
		return nil, fmt.Errorf("must specify either --%s or --%s", flags.dsnFlagName, flags.isEmptyDsnUsingPqFlagName)
	}
	connConfig, err := pgx.ParseConfig(flags.dsn)
	if err != nil {
		return nil, fmt.Errorf("could not parse connection string %q: %w", flags.dsn, err)
	}
	return connConfig, nil
}

// logFmtToMap parses all LogFmt key/value pairs from the provided string into a
// map.
//
// All records are scanned. If a duplicate key is found, an error is returned.
func logFmtToMap(logFmt string) (map[string]string, error) {
	logMap := make(map[string]string)
	decoder := logfmt.NewDecoder(strings.NewReader(logFmt))
	for decoder.ScanRecord() {
		for decoder.ScanKeyval() {
			if _, ok := logMap[string(decoder.Key())]; ok {
				return nil, fmt.Errorf("duplicate key %q in logfmt", string(decoder.Key()))
			}
			logMap[string(decoder.Key())] = string(decoder.Value())
		}
	}
	if decoder.Err() != nil {
		return nil, decoder.Err()
	}
	return logMap, nil
}
