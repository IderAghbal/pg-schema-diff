package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
)

func TestMaxConnectionsRefusesFewerThanTwo(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		wantErr bool
	}{
		{args: []string{"version"}},
		{args: []string{"version", "--max-connections", "2"}},
		{args: []string{"version", "--max-connections", "1"}, wantErr: true},
		{args: []string{"version", "--max-connections", "0"}, wantErr: true},
		{args: []string{"version", "--max-connections", "-3"}, wantErr: true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			rootCmd := buildRootCmd()
			rootCmd.SetArgs(tc.args)
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			err := rootCmd.Execute()
			if tc.wantErr {
				assert.ErrorContains(t, err, "--max-connections must be at least 2")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMaxOpenConnections(t *testing.T) {
	buildRootCmd()
	assert.Equal(t, 5, maxOpenConnections(5), "unset, a pool keeps its default")
	maxConnections = 2
	t.Cleanup(func() { maxConnections = 0 })
	assert.Equal(t, 2, maxOpenConnections(5))
	maxConnections = 8
	assert.Equal(t, 5, maxOpenConnections(5), "the flag only ever lowers a pool's default")
}

// An apply as a role whose connection limit sits below what the pools open by default fails, and passes once
// --max-connections holds each pool to two connections: the target's, the temporary database server's and the
// temporary database's.
func (suite *cmdTestSuite) TestApplyCmdHoldsToMaxConnections() {
	admin, err := sql.Open("pgx", suite.pgEngine.GetPostgresDatabaseDSN())
	suite.Require().NoError(err)

	role := "capped_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	_, err = admin.Exec(fmt.Sprintf("CREATE ROLE %s LOGIN CREATEDB CONNECTION LIMIT 6", role))
	suite.Require().NoError(err)
	_, err = admin.Exec(fmt.Sprintf("CREATE DATABASE %s OWNER %s", role, role))
	suite.Require().NoError(err)
	suite.T().Cleanup(func() {
		// The refused apply can leave a temporary database behind, which the role owns too.
		rows, err := admin.Query("SELECT datname FROM pg_database WHERE datdba = (SELECT oid FROM pg_roles WHERE rolname = $1)", role)
		require.NoError(suite.T(), err)
		var owned []string
		for rows.Next() {
			var name string
			require.NoError(suite.T(), rows.Scan(&name))
			owned = append(owned, name)
		}
		require.NoError(suite.T(), rows.Err())
		for _, name := range owned {
			_, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name))
			require.NoError(suite.T(), err)
		}
		_, err = admin.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", role))
		require.NoError(suite.T(), err)
		require.NoError(suite.T(), admin.Close())
	})
	dsn := suite.pgEngine.GetPostgresDatabaseConnOpts().
		With(pgengine.ConnectionOptionUser, role).
		With(pgengine.ConnectionOptionDatabase, role).
		ToDSN()

	var ddl []string
	for i := range 6 {
		ddl = append(ddl,
			fmt.Sprintf("CREATE TABLE t%d (id BIGINT PRIMARY KEY, name TEXT NOT NULL CHECK (name <> ''), at TIMESTAMPTZ);", i),
			fmt.Sprintf("CREATE INDEX t%d_name ON t%d (name);", i, i),
			fmt.Sprintf("CREATE FUNCTION f%d() RETURNS INT LANGUAGE SQL AS 'SELECT %d';", i, i),
			fmt.Sprintf("CREATE VIEW v%d AS SELECT id, name FROM t%d;", i, i),
		)
	}
	schemaDir := tempSchemaDir(suite.T(), []string{strings.Join(ddl, "\n")})
	apply := []string{"apply", "--skip-confirm-prompt", "--from-dsn", dsn, "--to-dir", schemaDir, "--allow-hazards", "INDEX_BUILD"}

	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:              apply,
		expectErrContains: []string{"too many connections for role"},
	})
	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:           append(apply, "--max-connections", "2"),
		outputContains: []string{"Schema applied successfully"},
	})
	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:           append(apply, "--max-connections", "2"),
		outputContains: []string{"Schema matches expected"},
	})
}
