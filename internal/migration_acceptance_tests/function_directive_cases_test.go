package migration_acceptance_tests

import (
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// functionDirectiveAcceptanceTestCases covers the `-- pg-schema-diff: no-untrackable-deps`
// in-body directive. A plpgsql function with the directive is treated as trackable, so the
// HAS_UNTRACKABLE_DEPENDENCIES hazard is suppressed for that function.
var functionDirectiveAcceptanceTestCases = []acceptanceTestCase{
	{
		name:         "create plpgsql with directive — no untrackable hazard",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{`
            CREATE OR REPLACE FUNCTION public.bump(i integer) RETURNS integer AS $$
            -- pg-schema-diff: no-untrackable-deps
            BEGIN
                RETURN i + 1;
            END;
            $$ LANGUAGE plpgsql;
        `},
		// Without the directive this case would emit HAS_UNTRACKABLE_DEPENDENCIES; assert
		// the directive suppresses it by giving an empty expected hazard set.
		expectedHazardTypes: nil,
	},
	{
		name:         "create plpgsql without directive — hazard fires",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{`
            CREATE OR REPLACE FUNCTION public.bump(i integer) RETURNS integer AS $$
            BEGIN
                RETURN i + 1;
            END;
            $$ LANGUAGE plpgsql;
        `},
		expectedHazardTypes: []diff.MigrationHazardType{diff.MigrationHazardTypeHasUntrackableDependencies},
	},
	{
		name: "drop plpgsql with directive — no untrackable hazard",
		oldSchemaDDL: []string{`
            CREATE OR REPLACE FUNCTION public.bump(i integer) RETURNS integer AS $$
            -- pg-schema-diff: no-untrackable-deps
            BEGIN
                RETURN i + 1;
            END;
            $$ LANGUAGE plpgsql;
        `},
		newSchemaDDL:        nil,
		expectedHazardTypes: nil,
	},
	{
		name: "alter plpgsql adding the directive — hazard suppressed afterwards",
		oldSchemaDDL: []string{`
            CREATE OR REPLACE FUNCTION public.bump(i integer) RETURNS integer AS $$
            BEGIN
                RETURN i + 1;
            END;
            $$ LANGUAGE plpgsql;
        `},
		newSchemaDDL: []string{`
            CREATE OR REPLACE FUNCTION public.bump(i integer) RETURNS integer AS $$
            -- pg-schema-diff: no-untrackable-deps
            BEGIN
                RETURN i + 1;
            END;
            $$ LANGUAGE plpgsql;
        `},
		expectedHazardTypes: nil,
	},
	{
		name: "sql-language function unaffected — was always trackable",
		oldSchemaDDL: nil,
		newSchemaDDL: []string{`
            CREATE FUNCTION public.add(a integer, b integer) RETURNS integer
                LANGUAGE SQL
                IMMUTABLE
                RETURNS NULL ON NULL INPUT
                RETURN a + b;
        `},
		expectedHazardTypes: nil,
	},
}

func TestFunctionDirectiveTestCases(t *testing.T) {
	runTestCases(t, functionDirectiveAcceptanceTestCases)
}
