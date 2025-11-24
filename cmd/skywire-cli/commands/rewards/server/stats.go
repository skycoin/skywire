// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/logo.go
package clirewardsserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
)

var (
	tempJSONPath  = filepath.Join(os.TempDir(), "log-collection.json")
	tempStatsPath = filepath.Join(os.TempDir(), "stats")
	cacheInterval = 300 * time.Second // refresh cache every 5 minutes seconds
)

// Node represents a single node's flattened information.
type node struct {
	PK        string `json:"pk"`
	Time      string `json:"time"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	StartedAt string `json:"started_at"`
}

type nodesResponse struct {
	Nodes []node `json:"nodes"`
}

func generateAndCacheJSON() error {
	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}

	var nodes []node
	for i := range pks {
		healthPath := wd + "/log_backups/" + pks[i] + "/health.json"
		nodeInfoPath := wd + "/log_backups/" + pks[i] + "/node-info.json"

		fileInfo, err := os.Stat(healthPath)
		if err != nil {
			continue
		}
		_, err = os.Stat(nodeInfoPath)
		if err != nil {
			continue
		}

		modTime := fileInfo.ModTime().Format(time.RFC3339)

		healthData, err := script.File(healthPath).Bytes()
		if err != nil {
			continue
		}

		var temp struct {
			BuildInfo struct {
				Version string `json:"version"`
				Commit  string `json:"commit"`
				Date    string `json:"date"`
			} `json:"build_info"`
			StartedAt string `json:"started_at"`
		}

		if err := json.Unmarshal(healthData, &temp); err != nil {
			continue
		}

		nodeInfoSlc, err := script.File(nodeInfoPath).JQ(".skywire_version").Replace(`"`, "").Replace("\n", "").Slice()
		if err != nil || len(nodeInfoSlc) == 0 {
			continue
		}

		nodes = append(nodes, node{
			PK:        pks[i],
			Time:      modTime,
			Version:   nodeInfoSlc[0],
			Commit:    temp.BuildInfo.Commit,
			Date:      temp.BuildInfo.Date,
			StartedAt: temp.StartedAt,
		})
	}

	data := nodesResponse{Nodes: nodes}
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(data); err != nil {
		return err
	}

	return os.WriteFile(tempJSONPath, buf.Bytes(), 0600)
}

func generateAndCacheStats() (err error) {
	_, err = script.Exec("mkdir -p " + tempStatsPath).String()
	if err != nil {
		return err
	}

	pks, err := script.ListFiles(wd + "/log_backups").Basename().Slice()
	if err != nil {
		return err
	}
	var surveyarches string
	var surveycpus string
	var surveyOSNames string
	var totalBytes int64
	var totalramBytes int64
	for i := range pks {
		ni := wd + "/log_backups/" + pks[i] + "/node-info.json"
		surveycpu, err := script.File(ni).JQ(".zcalusic_sysinfo.cpu.model").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveycpus += surveycpu

		surveyarch, err := script.File(ni).JQ(".go_arch").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveyarches += surveyarch

		surveyOSName, err := script.File(ni).JQ(".zcalusic_sysinfo.os.name").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		surveyOSNames += surveyOSName

		surveytbs, err := script.File(ni).JQ(".ghw_blockinfo.total_size_bytes").Reject("null").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		if surveytbs != "\n" && surveytbs != "" {
			byteValue, err := strconv.ParseInt(strings.TrimRight(surveytbs, "\n"), 10, 64)
			if err != nil {
				continue
			}
			totalBytes += byteValue
		}

		surveymem, err := script.File(ni).JQ(".ghw_memoryinfo.total_usable_bytes").Reject("null").Replace(`"`, "").String()
		if err != nil {
			continue
		}
		if surveymem != "\n" && surveymem != "" {
			byteValue, err := strconv.ParseInt(strings.TrimRight(surveymem, "\n"), 10, 64)
			if err != nil {
				continue
			}
			totalramBytes += byteValue
		}
	}

	cpustats, err := script.Echo(surveycpus).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey CPU statistics:\n%s\n", cpustats)).WriteFile(tempStatsPath + "/cpu.txt")
	if err != nil {
		return err
	}

	archstats, err := script.Echo(surveyarches).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey architecture statistics:\n%s\n", archstats)).WriteFile(tempStatsPath + "/arch.txt")
	if err != nil {
		return err
	}

	namestats, err := script.Echo(surveyOSNames).Freq().String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("Survey OS name statistics:\n%s\n", namestats)).WriteFile(tempStatsPath + "/os.txt")
	if err != nil {
		return err
	}

	formattedTotal, err := script.Echo(fmt.Sprintf("%d", totalBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
	if err != nil {
		return err
	}

	_, err = script.Echo(fmt.Sprintf("Survey total byte size (cumulative): %s\n", formattedTotal)).WriteFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("T").AppendFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_blockinfo.total_size_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").AppendFile(tempStatsPath + "/mem.txt")
	if err != nil {
		return err
	}

	ramTotal, err := script.Echo(fmt.Sprintf("%d", totalramBytes)).ExecForEach("numfmt --to=iec {{.}}").String()
	if err != nil {
		return err
	}
	_, err = script.Echo(fmt.Sprintf("<u>Survey total RAM byte size (cumulative):</u> %s\n", ramTotal)).WriteFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).Reject("G").AppendFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	_, err = script.Exec(`bash -c 'jq '.ghw_memoryinfo.total_usable_bytes' ` + wd + `/log_backups/*/node-info.json | grep -v null | sort -n | numfmt --to=iec | sort -h | uniq -c'`).AppendFile(tempStatsPath + "/ram.txt")
	if err != nil {
		return err
	}

	return err
}
