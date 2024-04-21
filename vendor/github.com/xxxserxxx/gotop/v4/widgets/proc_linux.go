package widgets

import (
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

func getProcs() ([]Proc, error) {
	output, err := exec.Command("ps", "-axo", "pid:10,comm:50,pcpu:5,pmem:5,args").Output()
	if err != nil {
		return nil, fmt.Errorf(tr.Value("widget.proc.err.ps", err.Error()))
	}

	// converts to []string, removing trailing newline and header
	linesOfProcStrings := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")[1:]

	procs := []Proc{}
	for _, line := range linesOfProcStrings {
		pid, err := strconv.Atoi(strings.TrimSpace(line[0:10]))
		if err != nil {
			log.Println(tr.Value("widget.proc.err.pidconv", err.Error(), line))
		}
		cpu, err := strconv.ParseFloat(strings.TrimSpace(line[63:68]), 64)
		if err != nil {
			log.Println(tr.Value("widget.proc.err.cpuconv", err.Error(), line))
		}
		mem, err := strconv.ParseFloat(strings.TrimSpace(line[69:74]), 64)
		if err != nil {
			log.Println(tr.Value("widget.proc.err.memconv", err.Error(), line))
		}
		proc := Proc{
			Pid:         pid,
			CommandName: strings.TrimSpace(line[11:61]),
			FullCommand: line[74:],
			CPU:         cpu,
			Mem:         mem,
		}
		procs = append(procs, proc)
	}

	return procs, nil
}
