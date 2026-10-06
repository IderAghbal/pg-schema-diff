package tempdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/stretchr/testify/suite"
	"github.com/stripe/pg-schema-diff/internal/pgengine"
	internalschema "github.com/stripe/pg-schema-diff/internal/schema"

	"github.com/stripe/pg-schema-diff/pkg/log"
)

type onInstanceTempDbFactorySuite struct {
	suite.Suite

	engine *pgengine.Engine
}

func (suite *onInstanceTempDbFactorySuite) SetupSuite() {
	engine, err := pgengine.StartEngine()
	suite.Require().NoError(err)
	suite.engine = engine
}

func (suite *onInstanceTempDbFactorySuite) TearDownSuite() {
	suite.engine.Close()
}

func (suite *onInstanceTempDbFactorySuite) mustBuildFactory(opt ...OnInstanceFactoryOpt) Factory {
	factory, err := NewOnInstanceFactory(context.Background(), func(ctx context.Context, dbName string) (*sql.DB, error) {
		return suite.getConnPoolForDb(dbName)
	}, opt...)
	suite.Require().NoError(err)
	return factory
}

func (suite *onInstanceTempDbFactorySuite) getConnPoolForDb(dbName string) (*sql.DB, error) {
	return sql.Open("pgx", suite.engine.GetPostgresDatabaseConnOpts().With("dbname", dbName).ToDSN())
}

func (suite *onInstanceTempDbFactorySuite) mustRunSQL(conn *sql.Conn) {
	_, err := conn.ExecContext(context.Background(), `
		CREATE TABLE foobar(
		  id INT PRIMARY KEY,
		  message TEXT
		);
		CREATE INDEX message_idx ON foobar(message);
 	`)
	suite.Require().NoError(err)

	_, err = conn.ExecContext(context.Background(), `
		INSERT INTO foobar VALUES (1, 'some message'), (2, 'some other message'), (3, 'a final message');
	`)
	suite.Require().NoError(err)

	res, err := conn.QueryContext(context.Background(), `
		SELECT id, message FROM foobar;
	`)
	suite.Require().NoError(err)

	var rows [][]any
	for res.Next() {
		var id int
		var message string
		suite.Require().NoError(res.Scan(&id, &message))
		rows = append(rows, []any{
			id, message,
		})
	}
	suite.ElementsMatch([][]any{
		{1, "some message"},
		{2, "some other message"},
		{3, "a final message"},
	}, rows)

	// Drop the table we just created
	_, err = conn.ExecContext(context.Background(), "DROP TABLE foobar")
	suite.Require().NoError(err)
}

func (suite *onInstanceTempDbFactorySuite) TestNew_ConnectsToWrongDatabase() {
	db, err := suite.engine.CreateDatabaseWithName("not-postgres")
	suite.Require().NoError(err)
	defer db.DropDB()

	_, err = NewOnInstanceFactory(context.Background(), func(ctx context.Context, dbName string) (*sql.DB, error) {
		return suite.getConnPoolForDb("not-postgres")
	})
	suite.ErrorContains(err, "connection pool is on")
}

func (suite *onInstanceTempDbFactorySuite) TestNew_ErrorsOnNonSimpleDbPrefix() {
	_, err := NewOnInstanceFactory(context.Background(), func(ctx context.Context, dbName string) (*sql.DB, error) {
		suite.Fail("shouldn't be reached")
		return nil, nil
	}, WithDbPrefix("non-simple identifier"))
	suite.ErrorContains(err, "must be a simple Postgres identifier")
}

func (suite *onInstanceTempDbFactorySuite) TestCreate_CreateAndDropFlow() {
	const (
		dbPrefix       = "some_prefix"
		metadataSchema = "some metadata schema"
		metadataTable  = "some metadata table"
		rootDbName     = "some_root_db"
	)

	rootDb, err := suite.engine.CreateDatabaseWithName(rootDbName)
	suite.Require().NoError(err)
	defer func(rootDb *pgengine.DB) {
		suite.Require().NoError(rootDb.DropDB())
	}(rootDb)

	factory := suite.mustBuildFactory(
		WithDbPrefix(dbPrefix),
		WithMetadataSchema(metadataSchema),
		WithMetadataTable(metadataTable),
		WithLogger(log.SimpleLogger()),
		WithRootDatabase(rootDbName),
	)
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	tempDb, err := factory.Create(context.Background())
	suite.Require().NoError(err)
	// Don't defer dropping. we want to run assertions after it drops. if dropping fails,
	// it shouldn't be a problem because names shouldn't conflict
	afterTimeOfCreation := time.Now()

	conn1, err := tempDb.ConnPool.Conn(context.Background())
	suite.Require().NoError(err)

	var dbName string
	suite.Require().NoError(conn1.QueryRowContext(context.Background(), "SELECT current_database()").Scan(&dbName))
	suite.True(strings.HasPrefix(dbName, dbPrefix))
	suite.Len(dbName, len(dbPrefix)+36) // should be length of prefix + length of uuid

	// Make sure SQL can run on the connection
	suite.mustRunSQL(conn1)

	// Check the metadata entry exists
	var createdAt time.Time
	metadataQuery := fmt.Sprintf(`
		SELECT * FROM "%s"."%s"
	`, metadataSchema, metadataTable)
	suite.Require().NoError(conn1.QueryRowContext(context.Background(), metadataQuery).Scan(&createdAt))
	suite.True(createdAt.Before(afterTimeOfCreation))

	// Get another connection from the pool and make sure it's also set to the correct db while
	// the other connection is still open
	conn2, err := tempDb.ConnPool.Conn(context.Background())
	suite.Require().NoError(err)
	var dbNameFromConn2 string
	suite.Require().NoError(conn2.QueryRowContext(context.Background(), "SELECT current_database()").Scan(&dbNameFromConn2))
	suite.Equal(dbName, dbNameFromConn2)

	suite.Require().NoError(conn1.Close())
	suite.Require().NoError(conn2.Close())

	// Get the schema without the exclude options. It should not be empty because of the metadata schema.
	schema, err := internalschema.GetSchema(context.Background(), tempDb.ConnPool)
	suite.Require().NoError(err)
	suite.NotEmpty(schema)

	// Get the schema with the exclude options (it should be empty)
	schema, err = internalschema.GetSchema(context.Background(), tempDb.ConnPool, tempDb.ExcludeMetadataOptions...)
	suite.Require().NoError(err)
	suite.Equal(internalschema.Schema{
		NamedSchemas: []internalschema.NamedSchema{{
			Name: "public",
		}},
	}, schema)

	// Drop database
	suite.Require().NoError(tempDb.Close(context.Background()))

	// Expect an error when attempting to query the database, since it should be dropped.
	// when a db pool is opened, it has no connections.
	// a query is needed in order to find if the database still exists.
	conn, err := suite.getConnPoolForDb(dbName)
	suite.Require().NoError(err)
	suite.Require().ErrorContains(conn.QueryRowContext(context.Background(), metadataQuery).Scan(&createdAt), "SQLSTATE 3D000")
	suite.True(createdAt.Before(afterTimeOfCreation))
}

// A session left on the temporary database made DROP DATABASE wait for it and fail, which a drop under a statement
// timeout shorter than that wait reported as "canceling statement due to statement timeout" and left the database
// behind.
func (suite *onInstanceTempDbFactorySuite) TestClose_DropsADatabaseASessionIsStillOn() {
	ctx := context.Background()
	factory := suite.mustBuildFactory(WithDropTimeout(DefaultStatementTimeout))
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	tempDb, err := factory.Create(ctx)
	suite.Require().NoError(err)
	var dbName string
	suite.Require().NoError(tempDb.ConnPool.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbName))

	holderPool, err := suite.getConnPoolForDb(dbName)
	suite.Require().NoError(err)
	defer holderPool.Close()
	holder, err := holderPool.Conn(ctx)
	suite.Require().NoError(err)
	defer holder.Close()
	_, err = holder.ExecContext(ctx, "SET SESSION statement_timeout = 100")
	suite.Require().NoError(err)
	_, err = holder.ExecContext(ctx, "BEGIN")
	suite.Require().NoError(err)
	_, err = holder.ExecContext(ctx, "SELECT 1")
	suite.Require().NoError(err)

	suite.Require().NoError(tempDb.Close(ctx))

	suite.Equal(0, suite.countDatabasesNamed(dbName))
	_, err = holder.ExecContext(ctx, "SELECT 1")
	suite.Error(err, "the session left on the dropped database should have been terminated")
}

// A database already gone counts as dropped, so a retry after an attempt whose drop committed does not fail.
func (suite *onInstanceTempDbFactorySuite) TestClose_ADatabaseAlreadyGoneCountsAsDropped() {
	ctx := context.Background()
	factory := suite.mustBuildFactory()
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	tempDb, err := factory.Create(ctx)
	suite.Require().NoError(err)
	var dbName string
	suite.Require().NoError(tempDb.ConnPool.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbName))
	suite.Require().NoError(tempDb.ConnPool.Close())

	rootPool, err := suite.getConnPoolForDb("postgres")
	suite.Require().NoError(err)
	defer rootPool.Close()
	_, err = rootPool.ExecContext(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName))
	suite.Require().NoError(err)

	suite.Require().NoError(tempDb.Close(ctx))
	suite.Equal(0, suite.countDatabasesNamed(dbName))
}

// An attempt that runs out of time is retried rather than leaving the database behind. A transaction on another
// database holds a lock on the temporary one, which no session on it would release, until the first attempt has
// failed.
func (suite *onInstanceTempDbFactorySuite) TestClose_RetriesADropThatRanOutOfTime() {
	ctx := context.Background()
	retried := &retryLogger{retries: make(chan string, 8)}
	factory := suite.mustBuildFactory(WithDropTimeout(500*time.Millisecond), WithLogger(retried))
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	tempDb, err := factory.Create(ctx)
	suite.Require().NoError(err)
	var dbName string
	suite.Require().NoError(tempDb.ConnPool.QueryRowContext(ctx, "SELECT current_database()").Scan(&dbName))

	rootPool, err := suite.getConnPoolForDb("postgres")
	suite.Require().NoError(err)
	defer rootPool.Close()
	blocker, err := rootPool.BeginTx(ctx, nil)
	suite.Require().NoError(err)
	_, err = blocker.ExecContext(ctx, fmt.Sprintf("COMMENT ON DATABASE %s IS 'held'", dbName))
	suite.Require().NoError(err)
	released := make(chan error, 1)
	go func() {
		select {
		case <-retried.retries:
		case <-time.After(30 * time.Second):
		}
		released <- blocker.Commit()
	}()

	suite.Require().NoError(tempDb.Close(ctx))
	suite.Require().NoError(<-released)
	suite.Equal(0, suite.countDatabasesNamed(dbName))
}

type retryLogger struct {
	retries chan string
}

func (l *retryLogger) Errorf(msg string, args ...any) {}

func (l *retryLogger) Warnf(msg string, args ...any) {
	select {
	case l.retries <- fmt.Sprintf(msg, args...):
	default:
	}
}

func (suite *onInstanceTempDbFactorySuite) countDatabasesNamed(dbName string) int {
	rootPool, err := suite.getConnPoolForDb("postgres")
	suite.Require().NoError(err)
	defer rootPool.Close()
	var count int
	suite.Require().NoError(rootPool.QueryRowContext(context.Background(), "SELECT count(*) FROM pg_database WHERE datname = $1", dbName).Scan(&count))
	return count
}

func (suite *onInstanceTempDbFactorySuite) TestCreate_ConnectsToWrongDatabase() {
	factory, err := NewOnInstanceFactory(context.Background(), func(ctx context.Context, dbName string) (*sql.DB, error) {
		return suite.getConnPoolForDb("postgres")
	})
	suite.Require().NoError(err)
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	_, err = factory.Create(context.Background())
	suite.ErrorContains(err, "connection pool is on")
}

func (suite *onInstanceTempDbFactorySuite) TestDropTempDB_CannotDropNonTempDb() {
	factory := suite.mustBuildFactory()
	defer func(factory Factory) {
		suite.Require().NoError(factory.Close())
	}(factory)

	suite.ErrorContains(factory.(*onInstanceFactory).dropTempDatabase(context.Background(), "some_db"), "drop non-temporary database")
}

func TestOnInstanceFactorySuite(t *testing.T) {
	suite.Run(t, new(onInstanceTempDbFactorySuite))
}
