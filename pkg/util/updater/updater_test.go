package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_getChecksum(t *testing.T) {
	tests := []struct {
		name      string
		checksums string
		filename  string
		want      string
		wantErr   error
	}{
		{
			name: "No Error 1",
			checksums: `
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92470 skywire-visor-v0.1.0-linux-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92471 skywire-visor-v0.1.0-linux-386
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92472 skywire-visor-v0.1.0-darwin-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92473 skywire-visor-v0.1.0-windows-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92474 skywire-visor-v0.1.0-linux-arm64
`,
			filename: "skywire-visor-v0.1.0-darwin-amd64",
			want:     "2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92472",
			wantErr:  nil,
		},
		{
			name: "No Error 2",
			checksums: `
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92470     	   skywire-visor-v0.1.0-linux-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92471     	   skywire-visor-v0.1.0-linux-386
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92472     	   skywire-visor-v0.1.0-darwin-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92473     	   skywire-visor-v0.1.0-windows-amd64
2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92474     	   skywire-visor-v0.1.0-linux-arm64
`,
			filename: "skywire-visor-v0.1.0-darwin-amd64",
			want:     "2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92472",
			wantErr:  nil,
		},
		{
			name:      "ErrMalformedChecksumFile 1",
			checksums: "skywire-visor-v0.1.0-darwin-amd64",
			filename:  "skywire-visor-v0.1.0-darwin-amd64",
			want:      "",
			wantErr:   ErrMalformedChecksumFile,
		},
		{
			name:      "ErrMalformedChecksumFile 2",
			checksums: " skywire-visor-v0.1.0-darwin-amd64",
			filename:  " skywire-visor-v0.1.0-darwin-amd64",
			want:      "",
			wantErr:   ErrMalformedChecksumFile,
		},
		{
			name:      "ErrMalformedChecksumFile 3",
			checksums: "  \t skywire-visor-v0.1.0-darwin-amd64",
			filename:  "  \t skywire-visor-v0.1.0-darwin-amd64",
			want:      "",
			wantErr:   ErrMalformedChecksumFile,
		},
		{
			name:      "ErrNoChecksumFound",
			checksums: `2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92470 skywire-visor-v0.1.0-linux-amd64`,
			filename:  "skywire-visor-v0.1.0-darwin-amd64",
			want:      "",
			wantErr:   ErrNoChecksumFound,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := getChecksum(tc.checksums, tc.filename)
			assert.Equal(t, tc.wantErr, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func Test_isChecksumValid(t *testing.T) {
	const (
		randText1 = "2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92470"
		randText2 = "2f505da2a905889aa978597814f91dbe32ee46fffe44657bd7af56a942d92472"
	)

	path := filepath.Join(os.TempDir(), randText1)

	defer func() {
		require.NoError(t, os.Remove(path))
	}()

	require.NoError(t, ioutil.WriteFile(path, []byte(randText2), permRWX))

	hasher := sha256.New()
	_, err := hasher.Write([]byte(randText2))
	require.NoError(t, err)

	sum := hex.EncodeToString(hasher.Sum(nil))

	valid, err := isChecksumValid(path, sum)
	require.NoError(t, err)
	require.True(t, valid)

	sum = randText1
	valid, err = isChecksumValid(path, sum)
	require.NoError(t, err)
	require.False(t, valid)
}

func Test_fileURL(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		filename string
		want     string
	}{
		{
			name:     "Case 1",
			version:  "v1.2.3",
			filename: "skywire-visor-v1.2.3-linux-amd64",
			want: "https://github.com/SkycoinProject/skywire-mainnet/releases/download/v1.2.3/" +
				"skywire-visor-v1.2.3-linux-amd64",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, fileURL(tc.version, tc.filename))
		})
	}
}

func Test_binaryFilename(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		version string
		os      string
		arch    string
		want    string
	}{
		{
			name:    "Case 1",
			file:    "skywire-visor",
			version: "v1.2.3",
			os:      "linux",
			arch:    "amd64",
			want:    "skywire-visor-v1.2.3-linux-amd64.tar.gz",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, archiveFilename(tc.file, tc.version, tc.os, tc.arch))
		})
	}
}
