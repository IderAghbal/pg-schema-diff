package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/pgdump"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
)

func (suite *cmdTestSuite) TestApplyCmd() {
	// Non-comprehensive set of tests for the plan command. Not totally comprehensive to avoid needing to avoid
	// hindering developer velocity when updating the command.
	type testCase struct {
		name string
		// fromDbArg is an optional argument to override the default "--from-dsn" arg.
		fromDbArg func(db *pgengine.DB) []string
		args      []string
		// dynamicArgs is function that can be used to build args that are dynamic, i.e.,
		// saving schemas to a randomly generated temporary directory.
		dynamicArgs []dArgGenerator

		outputContains []string
		// expectedSchema is the schema that is expected to be in the database after the migration.
		// If nil, the expected schema will be the fromDDL.
		expectedSchemaDDL []string
		// expectErrContains is a list of substrings that are expected to be contained in the error returned by
		// cmd.RunE. This is DISTINCT from stdErr.
		expectErrContains []string
	}
	for _, tc := range []testCase{
		{
			name:        "to dir",
			dynamicArgs: []dArgGenerator{tempSchemaDirDArg("to-dir", []string{"CREATE TABLE foobar();"})},

			expectedSchemaDDL: []string{"CREATE TABLE foobar();"},
		},
		{
			name:        "to dsn",
			dynamicArgs: []dArgGenerator{tempDsnDArg(suite.pgEngine, "to-dsn", []string{"CREATE TABLE foobar();"})},

			expectedSchemaDDL: []string{"CREATE TABLE foobar();"},
		},
		{
			name: "from empty dsn",
			fromDbArg: func(db *pgengine.DB) []string {
				tempSetPqEnvVarsForDb(suite.T(), db)
				return []string{"--from-empty-dsn"}
			},
			dynamicArgs: []dArgGenerator{tempSchemaDirDArg("to-dir", []string{"CREATE TABLE foobar();"})},

			expectedSchemaDDL: []string{"CREATE TABLE foobar();"},
		},
		{
			name:              "no to schema provided",
			expectErrContains: []string{"must be set"},
		},
		{
			name:              "two to schemas provided",
			args:              []string{"--to-dir", "some-other-dir", "--to-dsn", "some-dsn"},
			expectErrContains: []string{"only one of"},
		},
	} {
		suite.Run(tc.name, func() {
			fromDb := tempDbWithSchema(suite.T(), suite.pgEngine, nil)
			if tc.fromDbArg == nil {
				tc.fromDbArg = func(db *pgengine.DB) []string {
					return []string{"--from-dsn", db.GetDSN()}
				}
			}
			args := append([]string{
				"apply",
				"--skip-confirm-prompt",
			}, tc.fromDbArg(fromDb)...)
			args = append(args, tc.args...)
			suite.runCmdWithAssertions(runCmdWithAssertionsParams{
				args:              args,
				dynamicArgs:       tc.dynamicArgs,
				outputContains:    tc.outputContains,
				expectErrContains: tc.expectErrContains,
			})
			// The migration should have been successful. Assert it was.
			expectedDb := tempDbWithSchema(suite.T(), suite.pgEngine, tc.expectedSchemaDDL)
			expectedDbDump, err := pgdump.GetDump(expectedDb, pgdump.WithSchemaOnly(), pgdump.WithRestrictKey(pgdump.FixedRestrictKey))
			suite.Require().NoError(err)
			fromDbDump, err := pgdump.GetDump(fromDb, pgdump.WithSchemaOnly(), pgdump.WithRestrictKey(pgdump.FixedRestrictKey))
			suite.Require().NoError(err)

			suite.Equal(expectedDbDump, fromDbDump)
		})
	}
}

func (suite *cmdTestSuite) TestApplyCmdStatementHook() {
	hookFile := func(sql string) dArgGenerator {
		return func(t *testing.T) []string {
			t.Helper()
			path := filepath.Join(t.TempDir(), "hook.sql")
			require.NoError(t, os.WriteFile(path, []byte(sql), 0644))
			return []string{"--statement-hook-file", path}
		}
	}
	const logHook = `INSERT INTO hook_log (ran_in_transaction, columns) SELECT
		current_setting('transaction_isolation') IS NOT NULL AND txid_current_if_assigned() IS NOT NULL,
		(SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.folders'::regclass AND attnum > 0 AND NOT attisdropped);`
	fromDDL := []string{
		"CREATE TABLE hook_log (ran_in_transaction BOOLEAN NOT NULL, columns BIGINT NOT NULL);",
		"CREATE TABLE folders (id BIGINT PRIMARY KEY, name TEXT);",
	}

	suite.Run("the hook runs in each statement's transaction, after the statement", func() {
		fromDb := tempDbWithSchema(suite.T(), suite.pgEngine, fromDDL)
		suite.runCmdWithAssertions(runCmdWithAssertionsParams{
			args: []string{"apply", "--skip-confirm-prompt", "--from-dsn", fromDb.GetDSN()},
			dynamicArgs: []dArgGenerator{
				tempSchemaDirDArg("to-dir", append(fromDDL[:1:1],
					"CREATE TABLE folders (id BIGINT PRIMARY KEY, name TEXT, size BIGINT);")),
				hookFile(logHook),
			},
		})
		suite.Equal([][]any{{true, int64(3)}}, queryRows(suite.T(), fromDb, "SELECT ran_in_transaction, columns FROM hook_log"))
	})

	suite.Run("a hook that fails rolls its statement back", func() {
		fromDb := tempDbWithSchema(suite.T(), suite.pgEngine, fromDDL)
		suite.runCmdWithAssertions(runCmdWithAssertionsParams{
			args: []string{"apply", "--skip-confirm-prompt", "--from-dsn", fromDb.GetDSN()},
			dynamicArgs: []dArgGenerator{
				tempSchemaDirDArg("to-dir", append(fromDDL[:1:1],
					"CREATE TABLE folders (id BIGINT PRIMARY KEY, name TEXT, size BIGINT);")),
				hookFile("DO $$ BEGIN RAISE EXCEPTION 'hook refused'; END $$;"),
			},
			expectErrContains: []string{"hook refused"},
		})
		suite.Equal([][]any{{int64(2)}}, queryRows(suite.T(), fromDb,
			"SELECT count(*) FROM pg_attribute WHERE attrelid = 'public.folders'::regclass AND attnum > 0 AND NOT attisdropped"))
	})

	suite.Run("the hook runs straight after a statement that cannot run in a transaction", func() {
		fromDb := tempDbWithSchema(suite.T(), suite.pgEngine, fromDDL)
		suite.runCmdWithAssertions(runCmdWithAssertionsParams{
			args: []string{"apply", "--skip-confirm-prompt", "--allow-hazards", "INDEX_BUILD", "--from-dsn", fromDb.GetDSN()},
			dynamicArgs: []dArgGenerator{
				tempSchemaDirDArg("to-dir", append(fromDDL,
					"CREATE INDEX folders_name ON folders (name);")),
				hookFile(logHook),
			},
		})
		suite.Equal([][]any{{false, int64(2)}}, queryRows(suite.T(), fromDb, "SELECT ran_in_transaction, columns FROM hook_log"))
	})
}

func queryRows(t *testing.T, db *pgengine.DB, query string) [][]any {
	t.Helper()
	pool, err := sql.Open("pgx", db.GetDSN())
	require.NoError(t, err)
	defer pool.Close()
	rows, err := pool.Query(query)
	require.NoError(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var all [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range row {
			pointers[i] = &row[i]
		}
		require.NoError(t, rows.Scan(pointers...))
		all = append(all, row)
	}
	require.NoError(t, rows.Err())
	return all
}
