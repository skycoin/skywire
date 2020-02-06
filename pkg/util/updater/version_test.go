package updater

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nolint:funlen
func TestVersionFromString(t *testing.T) {
	tests := []struct {
		name string
		str  string
		want *Version
		err  error
	}{
		{
			name: "Case 1",
			str:  "0.1.0",
			want: &Version{
				Major:      0,
				Minor:      1,
				Patch:      0,
				Additional: "",
			},
			err: nil,
		},
		{
			name: "Case 2",
			str:  "1.2.3",
			want: &Version{
				Major:      1,
				Minor:      2,
				Patch:      3,
				Additional: "",
			},
			err: nil,
		},
		{
			name: "Case 3",
			str:  "3.2.1",
			want: &Version{
				Major:      3,
				Minor:      2,
				Patch:      1,
				Additional: "",
			},
			err: nil,
		},
		{
			name: "Case 4",
			str:  "3.2.1-test",
			want: &Version{
				Major:      3,
				Minor:      2,
				Patch:      1,
				Additional: "test",
			},
			err: nil,
		},
		{
			name: "Case 5",
			str:  "3.2.1-1-test",
			want: &Version{
				Major:      3,
				Minor:      2,
				Patch:      1,
				Additional: "1-test",
			},
			err: nil,
		},
		{
			name: "Case 6",
			str:  "v3.2.1-1-test",
			want: &Version{
				Major:      3,
				Minor:      2,
				Patch:      1,
				Additional: "1-test",
			},
			err: nil,
		},
		{
			name: "Malformed 1",
			str:  "3.2-1-test",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 2",
			str:  "",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 3",
			str:  "1",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 4",
			str:  "1.2",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 5",
			str:  "a.b.c",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 6",
			str:  "-1.1.1",
			want: nil,
			err:  ErrMalformedVersion,
		},
		{
			name: "Malformed 7",
			str:  "0.1.999999999999999999999999999",
			want: nil,
			err:  ErrMalformedVersion,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := VersionFromString(tc.str)
			require.Equal(t, tc.err, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// nolint:funlen
func TestVersion_Cmp(t *testing.T) {
	tests := []struct {
		name string
		v1   *Version
		v2   *Version
		want int
	}{
		{
			name: "Case 1",
			v1: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			want: 0,
		},
		{
			name: "Case 2",
			v1: &Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			want: 1,
		},
		{
			name: "Case 3",
			v1: &Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      1,
				Patch:      0,
				Additional: "",
			},
			want: 1,
		},
		{
			name: "Case 4",
			v1: &Version{
				Major:      1,
				Minor:      2,
				Patch:      5,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      8,
				Patch:      9,
				Additional: "123",
			},
			want: 1,
		},
		{
			name: "Case 5",
			v1: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      1,
				Minor:      0,
				Patch:      0,
				Additional: "123",
			},
			want: -1,
		},
		{
			name: "Case 6",
			v1: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      1,
				Patch:      0,
				Additional: "123",
			},
			want: -1,
		},
		{
			name: "Case 7",
			v1: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      0,
				Patch:      1,
				Additional: "123",
			},
			want: -1,
		},
		{
			name: "Case 8",
			v1: &Version{
				Major:      0,
				Minor:      1,
				Patch:      0,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      0,
				Patch:      1,
				Additional: "123",
			},
			want: 1,
		},
		{
			name: "Case 8",
			v1: &Version{
				Major:      0,
				Minor:      0,
				Patch:      1,
				Additional: "",
			},
			v2: &Version{
				Major:      0,
				Minor:      0,
				Patch:      0,
				Additional: "123",
			},
			want: 1,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.v1.Cmp(tc.v2))
		})
	}
}
