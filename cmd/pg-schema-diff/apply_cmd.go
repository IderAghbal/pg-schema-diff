package main

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/spf13/cobra"
	"github.com/stripe/pg-schema-diff/pkg/diff"
	"github.com/stripe/pg-schema-diff/pkg/log"
)

const (
	patternAllowHazardOnKey = "pattern"
	hazardAllowHazardOnKey  = "hazard"
)

// scopedHazardAllowance is one parsed --allow-hazard-on entry: a hazard type that is allowed only on
// statements whose DDL matches the regex.
//
// The pattern is matched against the full DDL string. Multi-line DDL (e.g., CREATE FUNCTION bodies)
// is treated as a single haystack — `^` and `$` anchor the entire DDL by default, not individual
// lines. Authors who need line-level anchors should prefix the pattern with `(?m)` to enable
// multi-line mode (Go RE2 flag).
type scopedHazardAllowance struct {
	pattern *regexp.Regexp
	hazard  diff.MigrationHazardType
}

func buildApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Migrate your database to the match the inputted schema (apply the schema to the database)",
	}

	connFlags := createConnectionFlags(cmd, "from-", " The database to migrate")
	toSchemaFlags := createSchemaSourceFlags(cmd, "to-")
	planOptsFlags := createPlanOptionsFlags(cmd)
	allowedHazardsTypesStrs := cmd.Flags().StringSlice("allow-hazards", nil,
		"Specify the hazards that are allowed. Order does not matter, and duplicates are ignored. If the"+
			" migration plan contains unwanted hazards (hazards not in this list), then the migration will fail to run"+
			" (example: --allow-hazards DELETES_DATA,INDEX_BUILD)")
	scopedHazardStrs := cmd.Flags().StringArray("allow-hazard-on", nil,
		fmt.Sprintf(
			"Allow a specific hazard only on statements whose DDL matches a regex. Repeatable. Format: "+
				"'%s=\"<regex>\" %s=<HAZARD_TYPE>'. Example: -allow-hazard-on '%s=\"CREATE OR REPLACE FUNCTION public.compute_slug_full_path\" %s=HAS_UNTRACKABLE_DEPENDENCIES'. "+
				"Use this when --allow-hazards would be too broad — a project-wide HAS_UNTRACKABLE_DEPENDENCIES waiver "+
				"silences every plpgsql function, while a per-pattern waiver scopes the allowance to one DDL. "+
				"The regex is matched against the full DDL string (which may span multiple lines): "+
				"'^' and '$' anchor the entire DDL by default; prefix with '(?m)' if you need line-level anchors "+
				"(e.g., '(?m)^CREATE TABLE'). Go RE2 syntax.",
			patternAllowHazardOnKey, hazardAllowHazardOnKey,
			patternAllowHazardOnKey, hazardAllowHazardOnKey,
		))
	skipConfirmPrompt := cmd.Flags().Bool("skip-confirm-prompt", false, "Skips prompt asking for user to confirm before applying")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		logger := log.SimpleLogger()

		connConfig, err := parseConnectionFlags(connFlags)
		if err != nil {
			return err
		}
		fromSchema := dsnSchemaSource(connConfig)

		toSchema, err := parseSchemaSource(*toSchemaFlags)
		if err != nil {
			return err
		}

		planOptions, err := parsePlanOptions(*planOptsFlags)
		if err != nil {
			return err
		}

		cmd.SilenceUsage = true

		plan, err := generatePlan(cmd.Context(), generatePlanParameters{
			fromSchema:       fromSchema,
			toSchema:         toSchema,
			tempDbConnConfig: connConfig,
			planOptions:      planOptions,
			logger:           logger,
		})
		if err != nil {
			return err
		} else if len(plan.Statements) == 0 {
			cmdPrintln(cmd, "Schema matches expected. No plan generated")
			return nil
		}

		cmdPrintln(cmd, header("Review plan"))
		cmdPrint(cmd, planToPrettyS(plan), "\n\n")

		scopedAllowances, err := parseScopedHazardAllowances(*scopedHazardStrs)
		if err != nil {
			return err
		}

		if err := failIfHazardsNotAllowed(plan, *allowedHazardsTypesStrs, scopedAllowances); err != nil {
			return err
		}

		if !*skipConfirmPrompt {
			if err := mustContinuePrompt(
				fmt.Sprintf(
					"Apply migration with the following hazards: %s?",
					strings.Join(*allowedHazardsTypesStrs, ", "),
				),
			); err != nil {
				return err
			}
		}

		if err := runPlan(cmd.Context(), cmd, connConfig, plan); err != nil {
			return err
		}
		cmdPrintln(cmd, "Schema applied successfully")
		return nil
	}

	return cmd
}

func failIfHazardsNotAllowed(plan diff.Plan, allowedHazardsTypesStrs []string, scopedAllowances []scopedHazardAllowance) error {
	isAllowedByHazardType := make(map[diff.MigrationHazardType]bool)
	for _, val := range allowedHazardsTypesStrs {
		isAllowedByHazardType[strings.ToUpper(val)] = true
	}
	var disallowedHazardMsgs []string
	for i, stmt := range plan.Statements {
		var disallowedTypes []diff.MigrationHazardType
		for _, hzd := range stmt.Hazards {
			if isAllowedByHazardType[hzd.Type] {
				continue
			}
			if isScopedAllowance(stmt, hzd.Type, scopedAllowances) {
				continue
			}
			disallowedTypes = append(disallowedTypes, hzd.Type)
		}
		if len(disallowedTypes) > 0 {
			disallowedHazardMsgs = append(disallowedHazardMsgs,
				fmt.Sprintf("- Statement %d: %s", getDisplayableStmtIdx(i), strings.Join(disallowedTypes, ", ")),
			)
		}

	}
	if len(disallowedHazardMsgs) > 0 {
		return fmt.Errorf("prohited hazards found\n"+
			"These hazards must be allowed via the allow-hazards or allow-hazard-on flag, e.g., --allow-hazards %s\n"+
			"Prohibited hazards in the following statements:\n%s",
			strings.Join(getHazardTypes(plan), ","),
			strings.Join(disallowedHazardMsgs, "\n"))
	}
	return nil
}

// isScopedAllowance reports whether the (statement, hazard) pair is covered by any --allow-hazard-on entry.
// A scoped allowance applies when its hazard type matches and its regex matches the statement DDL.
func isScopedAllowance(stmt diff.Statement, hazardType diff.MigrationHazardType, allowances []scopedHazardAllowance) bool {
	for _, a := range allowances {
		if a.hazard != hazardType {
			continue
		}
		if a.pattern.MatchString(stmt.DDL) {
			return true
		}
	}
	return false
}

// parseScopedHazardAllowances parses each --allow-hazard-on entry (logfmt: 'pattern="<regex>" hazard=<TYPE>').
// Hazard names are normalized to upper-case to match diff.MigrationHazardType.
func parseScopedHazardAllowances(raw []string) ([]scopedHazardAllowance, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]scopedHazardAllowance, 0, len(raw))
	for _, val := range raw {
		fm, err := logFmtToMap(val)
		if err != nil {
			return nil, fmt.Errorf("parsing --allow-hazard-on %q: %w", val, err)
		}
		patternStr, err := mustGetAndDeleteKey(fm, patternAllowHazardOnKey)
		if err != nil {
			return nil, fmt.Errorf("parsing --allow-hazard-on %q: %w", val, err)
		}
		hazardStr, err := mustGetAndDeleteKey(fm, hazardAllowHazardOnKey)
		if err != nil {
			return nil, fmt.Errorf("parsing --allow-hazard-on %q: %w", val, err)
		}
		if len(fm) > 0 {
			return nil, fmt.Errorf("parsing --allow-hazard-on %q: unknown keys %s", val, slices.Sorted(maps.Keys(fm)))
		}
		re, err := regexp.Compile(patternStr)
		if err != nil {
			return nil, fmt.Errorf("parsing --allow-hazard-on %q: pattern %q: %w", val, patternStr, err)
		}
		out = append(out, scopedHazardAllowance{
			pattern: re,
			hazard:  diff.MigrationHazardType(strings.ToUpper(hazardStr)),
		})
	}
	return out, nil
}

func runPlan(ctx context.Context, cmd *cobra.Command, connConfig *pgx.ConnConfig, plan diff.Plan) error {
	connPool, err := openDbWithPgxConfig(connConfig)
	if err != nil {
		return err
	}
	defer connPool.Close()

	conn, err := connPool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Due to the way *sql.Db works, when a statement_timeout is set for the session, it will NOT reset
	// by default when it's returned to the pool.
	//
	// We can't set the timeout at the TRANSACTION-level (for each transaction) because `ADD INDEX CONCURRENTLY`
	// must be executed within its own transaction block. Postgres will error if you try to set a TRANSACTION-level
	// timeout for it. SESSION-level statement_timeouts are respected by `ADD INDEX CONCURRENTLY`
	for i, stmt := range plan.Statements {
		cmdPrintln(cmd, header(fmt.Sprintf("Executing statement %d", getDisplayableStmtIdx(i))))
		cmdPrintf(cmd, "%s\n\n", statementToPrettyS(stmt))
		start := time.Now()
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION statement_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
			return fmt.Errorf("setting statement timeout: %w", err)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION lock_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
			return fmt.Errorf("setting lock timeout: %w", err)
		}
		if _, err := conn.ExecContext(ctx, stmt.ToSQL()); err != nil {
			return fmt.Errorf("executing migration statement. the database maybe be in a dirty state: %s: %w", stmt.DDL, err)
		}
		cmdPrintf(cmd, "Finished executing statement. Duration: %s\n", time.Since(start))
	}
	cmdPrintln(cmd, header("Complete"))

	return nil
}

func getHazardTypes(plan diff.Plan) []diff.MigrationHazardType {
	seenHazardTypes := make(map[diff.MigrationHazardType]bool)
	var hazardTypes []diff.MigrationHazardType
	for _, stmt := range plan.Statements {
		for _, hazard := range stmt.Hazards {
			if !seenHazardTypes[hazard.Type] {
				seenHazardTypes[hazard.Type] = true
				hazardTypes = append(hazardTypes, hazard.Type)
			}
		}
	}
	sort.Slice(hazardTypes, func(i, j int) bool {
		return hazardTypes[i] < hazardTypes[j]
	})
	return hazardTypes
}

func getDisplayableStmtIdx(i int) int {
	return i + 1
}
