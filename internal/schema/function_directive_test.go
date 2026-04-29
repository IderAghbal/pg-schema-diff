package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHasFunctionTrackabilityDirective(t *testing.T) {
	for _, tc := range []struct {
		name    string
		funcDef string
		want    bool
	}{
		{
			name: "no directive",
			funcDef: `CREATE OR REPLACE FUNCTION public.fn() RETURNS TRIGGER AS $$
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: false,
		},
		{
			name: "no-untrackable-deps directive",
			funcDef: `CREATE OR REPLACE FUNCTION public.fn() RETURNS TRIGGER AS $$
-- pg-schema-diff: no-untrackable-deps
BEGIN
    UPDATE some_table SET x = 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "leading whitespace before -- accepted",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
    -- pg-schema-diff: no-untrackable-deps
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "trailing whitespace after directive accepted",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- pg-schema-diff: no-untrackable-deps
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "must match prefix exactly — pg-schema-diffx ignored",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- pg-schema-diffx: no-untrackable-deps
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: false,
		},
		{
			name: "case-insensitive prefix",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- PG-SCHEMA-DIFF: no-untrackable-deps
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "case-insensitive body",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- pg-schema-diff: NO-UNTRACKABLE-DEPS
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "case-insensitive both prefix and body",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- Pg-Schema-Diff: No-Untrackable-Deps
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "interleaved with other comments",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- This function does X
-- pg-schema-diff: no-untrackable-deps
-- More commentary
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "unknown directive still flips trackability (forward-compat)",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
-- pg-schema-diff: future-directive=foo
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: true,
		},
		{
			name: "comment without -- prefix ignored",
			funcDef: `CREATE OR REPLACE FUNCTION fn() RETURNS TRIGGER AS $$
/* pg-schema-diff: no-untrackable-deps */
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;`,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hasFunctionTrackabilityDirective(tc.funcDef))
		})
	}
}
