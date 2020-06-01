package updater

import (
	"errors"
	"strconv"
	"strings"
)

const (
	semverLen = 3
)

var (
	// ErrMalformedVersion is returned when version is malformed.
	ErrMalformedVersion = errors.New("version malformed")
)

// Version represents binary version in semantic versioning format.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Additional string
}

// Cmp compares two versions: v and v2.
// v > v2  => 1
// v == v2 => 0
// v < v2  => -1
func (v *Version) Cmp(v2 *Version) int {
	if v.Major > v2.Major {
		return 1
	}

	if v.Major < v2.Major {
		return -1
	}

	if v.Minor > v2.Minor {
		return 1
	}

	if v.Minor < v2.Minor {
		return -1
	}

	if v.Patch > v2.Patch {
		return 1
	}

	if v.Patch < v2.Patch {
		return -1
	}

	return 0
}

// String converts Version to string.
func (v *Version) String() string {
	version := "v" + strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Additional != "" {
		version += "-" + v.Additional
	}

	return version
}

// VersionFromString parses a Version from a string.
func VersionFromString(s string) (*Version, error) {
	s = strings.TrimPrefix(s, "v")

	var version Version

	strs := strings.SplitN(s, "-", 2)
	if len(strs) > 1 {
		version.Additional = strs[1]
	}

	strs = strings.Split(strs[0], ".")
	if len(strs) != semverLen {
		return nil, ErrMalformedVersion
	}

	v, err := strconv.Atoi(strs[0])
	if err != nil {
		return nil, ErrMalformedVersion
	}

	version.Major = v

	v, err = strconv.Atoi(strs[1])
	if err != nil {
		return nil, ErrMalformedVersion
	}

	version.Minor = v

	v, err = strconv.Atoi(strs[2])
	if err != nil {
		return nil, ErrMalformedVersion
	}

	version.Patch = v

	return &version, nil
}
