package main

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stripe/pg-schema-diff/pkg/diff"
)

func TestParseScopedHazardAllowances(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       []string
		want      []scopedHazardAllowance
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty",
			raw:  nil,
			want: nil,
		},
		{
			name: "single allowance",
			raw:  []string{`pattern="CREATE OR REPLACE FUNCTION" hazard=HAS_UNTRACKABLE_DEPENDENCIES`},
			want: []scopedHazardAllowance{
				{
					pattern: regexp.MustCompile("CREATE OR REPLACE FUNCTION"),
					hazard:  diff.MigrationHazardType("HAS_UNTRACKABLE_DEPENDENCIES"),
				},
			},
		},
		{
			name: "lower-case hazard normalized to upper",
			raw:  []string{`pattern=".*" hazard=deletes_data`},
			want: []scopedHazardAllowance{
				{
					pattern: regexp.MustCompile(".*"),
					hazard:  diff.MigrationHazardType("DELETES_DATA"),
				},
			},
		},
		{
			name:      "missing pattern",
			raw:       []string{`hazard=DELETES_DATA`},
			wantErr:   true,
			errSubstr: "pattern",
		},
		{
			name:      "missing hazard",
			raw:       []string{`pattern=".*"`},
			wantErr:   true,
			errSubstr: "hazard",
		},
		{
			name:      "extra unknown key",
			raw:       []string{`pattern=".*" hazard=DELETES_DATA something=else`},
			wantErr:   true,
			errSubstr: "unknown keys",
		},
		{
			name:      "invalid regex",
			raw:       []string{`pattern="[unclosed" hazard=DELETES_DATA`},
			wantErr:   true,
			errSubstr: "pattern",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseScopedHazardAllowances(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, len(tc.want), len(got))
			for i := range tc.want {
				assert.Equal(t, tc.want[i].hazard, got[i].hazard)
				assert.Equal(t, tc.want[i].pattern.String(), got[i].pattern.String())
			}
		})
	}
}

func TestIsScopedAllowance(t *testing.T) {
	allowances := []scopedHazardAllowance{
		{
			pattern: regexp.MustCompile(`^CREATE OR REPLACE FUNCTION public\.compute_slug_full_path`),
			hazard:  diff.MigrationHazardType("HAS_UNTRACKABLE_DEPENDENCIES"),
		},
		{
			pattern: regexp.MustCompile(`^DROP TABLE`),
			hazard:  diff.MigrationHazardType("DELETES_DATA"),
		},
	}

	for _, tc := range []struct {
		name   string
		stmt   diff.Statement
		hazard diff.MigrationHazardType
		want   bool
	}{
		{
			name:   "matching pattern + hazard",
			stmt:   diff.Statement{DDL: "CREATE OR REPLACE FUNCTION public.compute_slug_full_path() RETURNS TRIGGER AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql"},
			hazard: "HAS_UNTRACKABLE_DEPENDENCIES",
			want:   true,
		},
		{
			name:   "matching pattern, different hazard",
			stmt:   diff.Statement{DDL: "CREATE OR REPLACE FUNCTION public.compute_slug_full_path() ..."},
			hazard: "DELETES_DATA",
			want:   false,
		},
		{
			name:   "different function — pattern doesn't match",
			stmt:   diff.Statement{DDL: "CREATE OR REPLACE FUNCTION public.unrelated_helper()"},
			hazard: "HAS_UNTRACKABLE_DEPENDENCIES",
			want:   false,
		},
		{
			name:   "second allowance covers DROP TABLE",
			stmt:   diff.Statement{DDL: "DROP TABLE old_users"},
			hazard: "DELETES_DATA",
			want:   true,
		},
		{
			name:   "empty allowances list",
			stmt:   diff.Statement{DDL: "anything"},
			hazard: "ANY",
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inputAllowances []scopedHazardAllowance
			if tc.name != "empty allowances list" {
				inputAllowances = allowances
			}
			assert.Equal(t, tc.want, isScopedAllowance(tc.stmt, tc.hazard, inputAllowances))
		})
	}
}

func TestIsScopedAllowance_MultiLineDDL(t *testing.T) {
	multiLineDDL := `CREATE OR REPLACE FUNCTION public.compute_slug_full_path()
RETURNS TRIGGER AS $$
BEGIN
    RETURN NEW;
END;
$$ LANGUAGE plpgsql`

	for _, tc := range []struct {
		name    string
		pattern string
		want    bool
	}{
		{
			name:    "single-line-mode anchors match the full string boundaries",
			pattern: `^CREATE OR REPLACE FUNCTION`,
			want:    true,
		},
		{
			name:    "single-line-mode does not match interior of the DDL with leading-line anchor",
			pattern: `^BEGIN`,
			want:    false,
		},
		{
			name:    "multi-line mode (?m) does match interior lines",
			pattern: `(?m)^BEGIN`,
			want:    true,
		},
		{
			name:    "embedded-pattern match without anchors works in either mode",
			pattern: `compute_slug_full_path`,
			want:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowances := []scopedHazardAllowance{{
				pattern: regexp.MustCompile(tc.pattern),
				hazard:  "TEST_HAZARD",
			}}
			got := isScopedAllowance(diff.Statement{DDL: multiLineDDL}, "TEST_HAZARD", allowances)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFailIfHazardsNotAllowed_ScopedSuppressesGlobal(t *testing.T) {
	plan := diff.Plan{
		Statements: []diff.Statement{
			{
				DDL: "CREATE OR REPLACE FUNCTION public.compute_slug_full_path() ...",
				Hazards: []diff.MigrationHazard{
					{Type: "HAS_UNTRACKABLE_DEPENDENCIES", Message: "..."},
				},
			},
			{
				DDL: "CREATE OR REPLACE FUNCTION public.unrelated_helper() ...",
				Hazards: []diff.MigrationHazard{
					{Type: "HAS_UNTRACKABLE_DEPENDENCIES", Message: "..."},
				},
			},
		},
	}

	// No allowances: both statements should fail.
	err := failIfHazardsNotAllowed(plan, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Statement 1")
	assert.Contains(t, err.Error(), "Statement 2")

	// Scoped allowance covers only the first statement → second still fails.
	scoped := []scopedHazardAllowance{
		{
			pattern: regexp.MustCompile(`^CREATE OR REPLACE FUNCTION public\.compute_slug_full_path`),
			hazard:  "HAS_UNTRACKABLE_DEPENDENCIES",
		},
	}
	err = failIfHazardsNotAllowed(plan, nil, scoped)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Statement 1")
	assert.Contains(t, err.Error(), "Statement 2")

	// Global allowance covers both → no error.
	err = failIfHazardsNotAllowed(plan, []string{"HAS_UNTRACKABLE_DEPENDENCIES"}, nil)
	assert.NoError(t, err)
}
