package schema

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

type (
	fakeNameFilterMock struct {
		expectedInput SchemaQualifiedName
		returnValue   bool
	}
	fakeNameFilter struct {
		t    *testing.T
		mock fakeNameFilterMock
	}
)

func newFakeNameFilter(t *testing.T, mock fakeNameFilterMock) fakeNameFilter {
	return fakeNameFilter{
		t:    t,
		mock: mock,
	}
}

func (f fakeNameFilter) filter(input SchemaQualifiedName) bool {
	assert.Equal(f.t, f.mock.expectedInput, input)
	return f.mock.returnValue
}

func TestOrNameFilters(t *testing.T) {
	someName1 := SchemaQualifiedName{
		SchemaName:  "some_schema",
		EscapedName: "some_name",
	}
	for _, tc := range []struct {
		name        string
		input       SchemaQualifiedName
		filters     []fakeNameFilterMock
		expectedOut bool
	}{
		{
			name:        "empty",
			input:       someName1,
			expectedOut: false,
		},
		{
			name:  "one filter (true)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{
					expectedInput: someName1,
					returnValue:   true,
				},
			},
			expectedOut: true,
		},
		{
			name:  "one filter (false)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: false},
			},
			expectedOut: false,
		},
		{
			name:  "two filters (false, true)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: false},
				{expectedInput: someName1, returnValue: true},
			},
			expectedOut: true,
		},
		{
			name:  "two filters (false, false)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: false},
				{expectedInput: someName1, returnValue: false},
			},
			expectedOut: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var filters []nameFilter
			for _, filter := range tc.filters {
				filters = append(filters, newFakeNameFilter(t, filter).filter)
			}
			assert.Equal(t, tc.expectedOut, orNameFilter(filters...)(tc.input))
		})
	}
}

func TestNotNameRegexFilter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		pattern     string
		input       SchemaQualifiedName
		expectedOut bool // true means "kept by filter" (NOT excluded)
	}{
		{
			name:        "match excludes object",
			pattern:     `_p[0-9]+(_|$)`,
			input:       SchemaQualifiedName{SchemaName: "partitioned", EscapedName: "webhook_events_p20260427"},
			expectedOut: false,
		},
		{
			name:        "match excludes object's auto-named pkey",
			pattern:     `_p[0-9]+(_|$)`,
			input:       SchemaQualifiedName{SchemaName: "partitioned", EscapedName: "webhook_events_p20260427_pkey"},
			expectedOut: false,
		},
		{
			name:        "non-match keeps parent table",
			pattern:     `_p[0-9]+(_|$)`,
			input:       SchemaQualifiedName{SchemaName: "partitioned", EscapedName: "webhook_events"},
			expectedOut: true,
		},
		{
			name:        "schema-qualified match",
			pattern:     `^partitioned\.`,
			input:       SchemaQualifiedName{SchemaName: "partitioned", EscapedName: "webhook_events"},
			expectedOut: false,
		},
		{
			name:        "different schema kept",
			pattern:     `^partitioned\.`,
			input:       SchemaQualifiedName{SchemaName: "public", EscapedName: "users"},
			expectedOut: true,
		},
		{
			name:        "quoted identifier matched against unescaped form",
			pattern:     `^public\.MixedCaseTable$`,
			input:       SchemaQualifiedName{SchemaName: "public", EscapedName: `"MixedCaseTable"`},
			expectedOut: false,
		},
		{
			name:        "doubled quote inside identifier unescaped",
			pattern:     `^public\.weird"name$`,
			input:       SchemaQualifiedName{SchemaName: "public", EscapedName: `"weird""name"`},
			expectedOut: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)
			assert.Equal(t, tc.expectedOut, notNameRegexFilter(re)(tc.input))
		})
	}
}

func TestUnescapeIdentifier(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"plain_name", "plain_name"},
		{`"PlainQuoted"`, "PlainQuoted"},
		{`"with""embedded"`, `with"embedded`},
		{`""`, ""},
		{`"`, `"`}, // single quote is not a wrap pair, returned as-is
	} {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, unescapeIdentifier(tc.in))
		})
	}
}

func TestAndNameFilters(t *testing.T) {
	someName1 := SchemaQualifiedName{
		SchemaName:  "some_schema",
		EscapedName: "some_name",
	}
	for _, tc := range []struct {
		name        string
		input       SchemaQualifiedName
		filters     []fakeNameFilterMock
		expectedOut bool
	}{
		{
			name:        "empty",
			input:       someName1,
			expectedOut: false,
		},
		{
			name:  "one filter (true)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{
					expectedInput: someName1,
					returnValue:   true,
				},
			},
			expectedOut: true,
		},
		{
			name:  "one filter (false)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: false},
			},
			expectedOut: false,
		},
		{
			name:  "two filters (false, true)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: false},
				{expectedInput: someName1, returnValue: true},
			},
			expectedOut: false,
		},
		{
			name:  "two filters (true, true)",
			input: someName1,
			filters: []fakeNameFilterMock{
				{expectedInput: someName1, returnValue: true},
				{expectedInput: someName1, returnValue: true},
			},
			expectedOut: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var filters []nameFilter
			for _, filter := range tc.filters {
				filters = append(filters, newFakeNameFilter(t, filter).filter)
			}
			assert.Equal(t, tc.expectedOut, andNameFilter(filters...)(tc.input))
		})
	}
}
