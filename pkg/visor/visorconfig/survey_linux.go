//go:build linux && !android

// Package visorconfig pkg/visor/visorconfig/survey_linux.go c3-vis-core
package visorconfig

import (
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jaypipes/ghw"
	"github.com/zcalusic/sysinfo"
)

// SystemSurvey returns system survey
func SystemSurvey() (Survey, error) {
	var si sysinfo.SysInfo
	si.GetSysInfo()
	disks, err := ghw.Block(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	product, err := ghw.Product(ghw.WithDisableWarnings())
	if err != nil {
		return Survey{}, err
	}
	memory, err := ghw.Memory(ghw.WithDisableWarnings())
	if err != nil && !strings.Contains(err.Error(), "Could not determine total usable bytes of memory") {
		return Survey{}, err
	}

	s := Survey{
		Timestamp:      time.Now(),
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		SYSINFO:        si,
		UUID:           uuid.New(),
		Disks:          disks,
		Product:        product,
		Memory:         memory,
		SkywireVersion: Version(),
	}
	return s, nil
}
