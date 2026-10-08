package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/spf13/cobra"
	"github.com/stripe/pg-schema-diff/pkg/diff"
	"github.com/stripe/pg-schema-diff/pkg/log"
)

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
	skipConfirmPrompt := cmd.Flags().Bool("skip-confirm-prompt", false, "Skips prompt asking for user to confirm before applying")
	statementHookFile := cmd.Flags().String("statement-hook-file", "",
		"SQL file to run after every statement of the plan: in the statement's own transaction, or straight after it"+
			" for a statement that cannot run in a transaction block (CREATE or DROP INDEX CONCURRENTLY)")
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

		var statementHook string
		if *statementHookFile != "" {
			hook, err := os.ReadFile(*statementHookFile)
			if err != nil {
				return fmt.Errorf("reading statement hook file: %w", err)
			}
			statementHook = string(hook)
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

		if err := failIfHazardsNotAllowed(plan, *allowedHazardsTypesStrs); err != nil {
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

		if err := runPlan(cmd.Context(), cmd, connConfig, plan, statementHook); err != nil {
			return err
		}
		cmdPrintln(cmd, "Schema applied successfully")
		return nil
	}

	return cmd
}

func failIfHazardsNotAllowed(plan diff.Plan, allowedHazardsTypesStrs []string) error {
	isAllowedByHazardType := make(map[diff.MigrationHazardType]bool)
	for _, val := range allowedHazardsTypesStrs {
		isAllowedByHazardType[strings.ToUpper(val)] = true
	}
	var disallowedHazardMsgs []string
	for i, stmt := range plan.Statements {
		var disallowedTypes []diff.MigrationHazardType
		for _, hzd := range stmt.Hazards {
			if !isAllowedByHazardType[hzd.Type] {
				disallowedTypes = append(disallowedTypes, hzd.Type)
			}
		}
		if len(disallowedTypes) > 0 {
			disallowedHazardMsgs = append(disallowedHazardMsgs,
				fmt.Sprintf("- Statement %d: %s", getDisplayableStmtIdx(i), strings.Join(disallowedTypes, ", ")),
			)
		}

	}
	if len(disallowedHazardMsgs) > 0 {
		return fmt.Errorf("prohited hazards found\n"+
			"These hazards must be allowed via the allow-hazards flag, e.g., --allow-hazards %s\n"+
			"Prohibited hazards in the following statements:\n%s",
			strings.Join(getHazardTypes(plan), ","),
			strings.Join(disallowedHazardMsgs, "\n"))
	}
	return nil
}

// outsideTransactionDDL matches the statements Postgres refuses inside a transaction block.
var outsideTransactionDDL = regexp.MustCompile(`(?i)^\s*(CREATE\s+(UNIQUE\s+)?INDEX|DROP\s+INDEX)\s+CONCURRENTLY\b`)

func runPlan(ctx context.Context, cmd *cobra.Command, connConfig *pgx.ConnConfig, plan diff.Plan, statementHook string) error {
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
		if statementHook != "" && !outsideTransactionDDL.MatchString(stmt.DDL) {
			if err := runStatementWithHook(ctx, conn, stmt, statementHook); err != nil {
				return err
			}
			cmdPrintf(cmd, "Finished executing statement. Duration: %s\n", time.Since(start))
			continue
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION statement_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
			return fmt.Errorf("setting statement timeout: %w", err)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("SET SESSION lock_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
			return fmt.Errorf("setting lock timeout: %w", err)
		}
		if _, err := conn.ExecContext(ctx, stmt.ToSQL()); err != nil {
			return fmt.Errorf("executing migration statement. the database maybe be in a dirty state: %s: %w", stmt.DDL, err)
		}
		if statementHook != "" {
			if _, err := conn.ExecContext(ctx, statementHook); err != nil {
				return fmt.Errorf("running the statement hook after %s: %w", stmt.DDL, err)
			}
		}
		cmdPrintf(cmd, "Finished executing statement. Duration: %s\n", time.Since(start))
	}
	cmdPrintln(cmd, header("Complete"))

	return nil
}

// runStatementWithHook runs a statement and the hook in one transaction, so that the hook sees the statement's
// effect and both commit or neither does. The timeouts are the transaction's own.
func runStatementWithHook(ctx context.Context, conn *sql.Conn, stmt diff.Statement, statementHook string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning the statement's transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
		return fmt.Errorf("setting statement timeout: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", stmt.Timeout.Milliseconds())); err != nil {
		return fmt.Errorf("setting lock timeout: %w", err)
	}
	if _, err := tx.ExecContext(ctx, stmt.ToSQL()); err != nil {
		return fmt.Errorf("executing migration statement. the database maybe be in a dirty state: %s: %w", stmt.DDL, err)
	}
	if _, err := tx.ExecContext(ctx, statementHook); err != nil {
		return fmt.Errorf("running the statement hook after %s: %w", stmt.DDL, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration statement: %s: %w", stmt.DDL, err)
	}
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
