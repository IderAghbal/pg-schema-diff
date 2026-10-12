package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
)

// An apply as a role that may not create databases fails to plan on its own server, and plans on the server
// --temp-db-dsn names: only there could its temporary databases have been created.
func (suite *cmdTestSuite) TestApplyCmdPlansOnTheTempDbServer() {
	admin, err := sql.Open("pgx", suite.pgEngine.GetPostgresDatabaseDSN())
	suite.Require().NoError(err)

	role := "migrator_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	_, err = admin.Exec(fmt.Sprintf("CREATE ROLE %s LOGIN NOCREATEDB", role))
	suite.Require().NoError(err)
	_, err = admin.Exec(fmt.Sprintf("CREATE DATABASE %s OWNER %s", role, role))
	suite.Require().NoError(err)
	suite.T().Cleanup(func() {
		_, err := admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", role))
		require.NoError(suite.T(), err)
		_, err = admin.Exec(fmt.Sprintf("DROP ROLE IF EXISTS %s", role))
		require.NoError(suite.T(), err)
		require.NoError(suite.T(), admin.Close())
	})
	dsn := suite.pgEngine.GetPostgresDatabaseConnOpts().
		With(pgengine.ConnectionOptionUser, role).
		With(pgengine.ConnectionOptionDatabase, role).
		ToDSN()
	apply := []string{"apply", "--skip-confirm-prompt", "--from-dsn", dsn,
		"--to-dir", tempSchemaDir(suite.T(), []string{"CREATE TABLE foobar(id INT PRIMARY KEY);"})}

	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:              apply,
		expectErrContains: []string{"permission denied to create database"},
	})
	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:           append(apply, "--temp-db-dsn", suite.pgEngine.GetPostgresDatabaseDSN()),
		outputContains: []string{"Schema applied successfully"},
	})
	suite.runCmdWithAssertions(runCmdWithAssertionsParams{
		args:           append(apply, "--temp-db-dsn", suite.pgEngine.GetPostgresDatabaseDSN()),
		outputContains: []string{"Schema matches expected"},
	})
}
