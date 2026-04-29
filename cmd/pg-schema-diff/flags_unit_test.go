package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileExcludeNameRegexes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       []string
		wantLen   int
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "empty",
			raw:     nil,
			wantLen: 0,
		},
		{
			name:    "single valid regex",
			raw:     []string{`_p[0-9]+(_|$)`},
			wantLen: 1,
		},
		{
			name:    "multiple valid regexes accumulate",
			raw:     []string{`_p[0-9]+`, `^archive_`, `^public\.foo$`},
			wantLen: 3,
		},
		{
			name:      "single invalid regex",
			raw:       []string{`[unclosed`},
			wantErr:   true,
			errSubstr: "[unclosed",
		},
		{
			name:      "first valid, second invalid stops at second",
			raw:       []string{`^foo$`, `[unclosed`},
			wantErr:   true,
			errSubstr: "[unclosed",
		},
		{
			name:      "error message names the flag for actionable failure",
			raw:       []string{`*invalid`},
			wantErr:   true,
			errSubstr: "--exclude-name-regex",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := compileExcludeNameRegexes(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantLen, len(got))
			// Each compiled regex should round-trip the original string.
			for i, re := range got {
				assert.Equal(t, tc.raw[i], re.String())
			}
		})
	}
}
