package migration_acceptance_tests

import (
	"regexp"
	"testing"

	"github.com/stripe/pg-schema-diff/pkg/diff"
)

// excludeNameRegexAcceptanceTestCases covers the --exclude-name-regex / WithExcludeNameRegexes flag,
// the partman use case it was introduced for: runtime-rotated child tables that live in the database
// but never in the source DDL. Without the filter pg-schema-diff would emit DROPs for every child on
// every plan run.
var excludeNameRegexAcceptanceTestCases = []acceptanceTestCase{
	{
		name: "child-pattern excludes runtime-rotated children, parent stays managed",
		oldSchemaDDL: []string{`
            CREATE TABLE webhook_events (
                id UUID DEFAULT gen_random_uuid(),
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                payload TEXT NOT NULL,
                PRIMARY KEY (id, created_at)
            ) PARTITION BY RANGE (created_at);
            -- Simulate partman creating a daily child outside the source DDL.
            CREATE TABLE webhook_events_p20260101 PARTITION OF webhook_events
                FOR VALUES FROM ('2026-01-01') TO ('2026-01-02');
        `},
		newSchemaDDL: []string{`
            CREATE TABLE webhook_events (
                id UUID DEFAULT gen_random_uuid(),
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                payload TEXT NOT NULL,
                PRIMARY KEY (id, created_at)
            ) PARTITION BY RANGE (created_at);
        `},
		planOpts: []diff.PlanOpt{
			diff.WithExcludeNameRegexes(regexp.MustCompile(`_p[0-9]+(_|$)`)),
		},
		// The child is excluded, so it stays.
		expectedDBSchemaDDL: []string{`
            CREATE TABLE webhook_events (
                id UUID DEFAULT gen_random_uuid(),
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                payload TEXT NOT NULL,
                PRIMARY KEY (id, created_at)
            ) PARTITION BY RANGE (created_at);
            CREATE TABLE webhook_events_p20260101 PARTITION OF webhook_events
                FOR VALUES FROM ('2026-01-01') TO ('2026-01-02');
        `},
		expectEmptyPlan: true,
	},
	{
		name: "without filter, a runtime child fails the plan",
		oldSchemaDDL: []string{`
            CREATE TABLE webhook_events (
                id UUID DEFAULT gen_random_uuid(),
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                payload TEXT NOT NULL,
                PRIMARY KEY (id, created_at)
            ) PARTITION BY RANGE (created_at);
            CREATE TABLE webhook_events_p20260101 PARTITION OF webhook_events
                FOR VALUES FROM ('2026-01-01') TO ('2026-01-02');
        `},
		newSchemaDDL: []string{`
            CREATE TABLE webhook_events (
                id UUID DEFAULT gen_random_uuid(),
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                payload TEXT NOT NULL,
                PRIMARY KEY (id, created_at)
            ) PARTITION BY RANGE (created_at);
        `},
		// Without the filter the child reads as a partition to drop, which the differ refuses on its own.
		expectedPlanErrorIs:       diff.ErrNotImplemented,
		expectedPlanErrorContains: "deleting partitions without dropping parent table",
	},
	{
		name: "filter does not affect non-matching tables",
		oldSchemaDDL: []string{`
            CREATE TABLE users (
                id UUID PRIMARY KEY DEFAULT gen_random_uuid()
            );
        `},
		newSchemaDDL: []string{`
            CREATE TABLE users (
                id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                email TEXT
            );
        `},
		planOpts: []diff.PlanOpt{
			diff.WithExcludeNameRegexes(regexp.MustCompile(`_p[0-9]+(_|$)`)),
		},
		expectEmptyPlan: false,
	},
	{
		name: "schema-qualified exclusion",
		oldSchemaDDL: []string{`
            CREATE SCHEMA partitioned;
            CREATE TABLE partitioned.archived_events (id UUID PRIMARY KEY);
            CREATE TABLE public.live_events (id UUID PRIMARY KEY);
        `},
		newSchemaDDL: []string{`
            CREATE SCHEMA partitioned;
            CREATE TABLE public.live_events (id UUID PRIMARY KEY);
        `},
		planOpts: []diff.PlanOpt{
			diff.WithExcludeNameRegexes(regexp.MustCompile(`^partitioned\.`)),
		},
		// The excluded table is left alone, so it stays.
		expectedDBSchemaDDL: []string{`
            CREATE SCHEMA partitioned;
            CREATE TABLE partitioned.archived_events (id UUID PRIMARY KEY);
            CREATE TABLE public.live_events (id UUID PRIMARY KEY);
        `},
		expectEmptyPlan: true,
	},
}

func TestExcludeNameRegexTestCases(t *testing.T) {
	runTestCases(t, excludeNameRegexAcceptanceTestCases)
}
