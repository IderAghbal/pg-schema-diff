package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// Postgres keeps a table's storage parameters in the order they were given, and a SET moves the parameters it sets
// to the end. The differ compares them as a set and writes them sorted, so a case whose plan creates or sets them
// declares them sorted: pg_dump would otherwise print the same parameters of the migrated and the directly created
// table in two orders.
var tableStorageParameterAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "No-op",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo TEXT
            ) WITH (fillfactor = 90, autovacuum_enabled = false, toast.autovacuum_enabled = false);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo TEXT
            ) WITH (fillfactor = 90, autovacuum_enabled = false, toast.autovacuum_enabled = false);
			`,
		},
		expectEmptyPlan: true,
	},
	{
		name:         "Create a table with storage parameters",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT PRIMARY KEY,
                foo TEXT
            ) WITH (autovacuum_vacuum_scale_factor = 0.05, fillfactor = 90, toast.autovacuum_enabled = false);
			`,
		},
	},
	{
		name: "Set a storage parameter",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            );
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (fillfactor = 90);
			`,
		},
		expectedPlanDDL: []string{
			`ALTER TABLE "public"."foobar" SET (fillfactor = '90')`,
		},
	},
	{
		name: "Change a storage parameter",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (autovacuum_enabled = false, fillfactor = 80);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (autovacuum_enabled = false, fillfactor = 90);
			`,
		},
		expectedPlanDDL: []string{
			`ALTER TABLE "public"."foobar" SET (fillfactor = '90')`,
		},
	},
	{
		name: "Reset a storage parameter",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (fillfactor = 90, autovacuum_enabled = false);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (autovacuum_enabled = false);
			`,
		},
		expectedPlanDDL: []string{
			`ALTER TABLE "public"."foobar" RESET (fillfactor)`,
		},
	},
	{
		name: "Set and reset storage parameters at once, the TOAST table's among them",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo TEXT
            ) WITH (fillfactor = 90, toast.autovacuum_enabled = false);
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo TEXT
            ) WITH (autovacuum_enabled = false, toast.autovacuum_vacuum_scale_factor = 0.1);
			`,
		},
		expectedPlanDDL: []string{
			`ALTER TABLE "public"."foobar" SET (autovacuum_enabled = 'false', toast.autovacuum_vacuum_scale_factor = '0.1')`,
			`ALTER TABLE "public"."foobar" RESET (fillfactor, toast.autovacuum_enabled)`,
		},
	},
	{
		name: "Set user_catalog_table",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            );
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT
            ) WITH (user_catalog_table = true);
			`,
		},
		expectedHazardTypes: []diff.MigrationHazardType{
			diff.MigrationHazardTypeAcquiresAccessExclusiveLock,
		},
	},
	{
		name:         "Create partitions with storage parameters of their own",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo VARCHAR(255)
            ) PARTITION BY LIST (foo);
            CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('foo_1') WITH (fillfactor = 90);
            CREATE TABLE foobar_2 PARTITION OF foobar FOR VALUES IN ('foo_2');
			`,
		},
	},
	{
		name: "Alter a partition's storage parameters",
		oldSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo VARCHAR(255)
            ) PARTITION BY LIST (foo);
            CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('foo_1') WITH (fillfactor = 90);
            CREATE TABLE foobar_2 PARTITION OF foobar FOR VALUES IN ('foo_2');
			`,
		},
		newSchemaDDL: []string{
			`
            CREATE TABLE foobar(
                id INT,
                foo VARCHAR(255)
            ) PARTITION BY LIST (foo);
            CREATE TABLE foobar_1 PARTITION OF foobar FOR VALUES IN ('foo_1');
            CREATE TABLE foobar_2 PARTITION OF foobar FOR VALUES IN ('foo_2') WITH (fillfactor = 80);
			`,
		},
		expectedPlanDDL: []string{
			`ALTER TABLE "public"."foobar_1" RESET (fillfactor)`,
			`ALTER TABLE "public"."foobar_2" SET (fillfactor = '80')`,
		},
	},
}

func TestTableStorageParameterTestCases(t *testing.T) {
	runTestCases(t, tableStorageParameterAcceptanceTestCases)
}
