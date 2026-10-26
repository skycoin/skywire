// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/0magnet/sh/v3/syntax"
)

// A job is a background shell running as a goroutine rather than a process,
// so the kill builtin cancels it with any signal which terminates a process.
// Nothing is ever stopped, so fg waits for a job and bg only reports on it.

// signalNames are the signals accepted by the kill builtin,
// whose numbers are fixed by POSIX and the same on every platform.
var signalNames = [...]string{1: "HUP", 2: "INT", 3: "QUIT", 6: "ABRT", 9: "KILL", 14: "ALRM", 15: "TERM"}

// killSignal is the cause with which the kill builtin cancels a job.
type killSignal uint8

func (s killSignal) Error() string { return "SIG" + signalNames[s] }

func parseSignal(s string) (uint8, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		n = slices.Index(signalNames[:], strings.TrimPrefix(strings.ToUpper(s), "SIG"))
	}
	if n == 0 { // only "0" itself, as a bare "SIG" matches the empty name
		return 0, err == nil
	}
	return uint8(n), n > 0 && n < len(signalNames) && signalNames[n] != ""
}

func isDone(done chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// waitDone waits for a job to finish, unless the context is cancelled first,
// as a job may outlive it; see [BackgroundContext]. In that case, it sets exit
// as fatal and returns false.
func waitDone(ctx context.Context, done chan struct{}, exit *exitStatus) bool {
	select {
	case <-done:
		return true
	case <-ctx.Done():
		exit.fatal(ctx.Err())
		return false
	}
}

func jobText(bg *bgProc) string {
	var sb strings.Builder
	syntax.NewPrinter(syntax.SingleLine(true)).Print(&sb, bg.stmt)
	return sb.String()
}

// jobList returns the jobs not yet disowned nor reported as finished.
func (r *Runner) jobList() []*bgProc {
	var jobs []*bgProc
	for i := range r.bgProcs {
		if bg := &r.bgProcs[i]; !bg.procSubst && !bg.hidden {
			jobs = append(jobs, bg)
		}
	}
	return jobs
}

// findJob finds a job by a spec such as %1, %+, %-, %prefix, or %?substring,
// or by what $! expanded to.
func (r *Runner) findJob(spec string) (*bgProc, error) {
	rest, ok := strings.CutPrefix(spec, "%")
	if !ok {
		// TODO: allow signalling other processes behind an option; see #171.
		if bg, _ := r.lookupBgProc(spec); bg != nil && !bg.procSubst {
			return bg, nil
		}
		return nil, fmt.Errorf("%s: no such job", spec)
	}
	jobs := r.jobList()
	var match *bgProc
	switch n, err := strconv.Atoi(rest); {
	case rest == "" || rest == "%" || rest == "+":
		if len(jobs) > 0 {
			match = jobs[len(jobs)-1]
		}
	case rest == "-":
		if len(jobs) > 1 {
			match = jobs[len(jobs)-2]
		}
	case err == nil:
		if i := slices.IndexFunc(jobs, func(bg *bgProc) bool { return bg.num == n }); i >= 0 {
			match = jobs[i]
		}
	default:
		matches := strings.HasPrefix
		if sub, ok := strings.CutPrefix(rest, "?"); ok {
			matches, rest = strings.Contains, sub
		}
		for _, bg := range jobs {
			if matches(jobText(bg), rest) {
				if match != nil {
					return nil, fmt.Errorf("%s: ambiguous job spec", rest)
				}
				match = bg
			}
		}
	}
	if match == nil {
		return nil, fmt.Errorf("%s: no such job", spec)
	}
	return match, nil
}

// findJobs is like [Runner.findJob] for many specs,
// reporting the ones not found as errors of the named builtin.
func (r *Runner) findJobs(name string, specs []string) (jobs []*bgProc, exit exitStatus) {
	for _, spec := range specs {
		if bg, err := r.findJob(spec); err != nil {
			r.errf("%s: %v\n", name, err)
			exit.code = 1
		} else {
			jobs = append(jobs, bg)
		}
	}
	return jobs, exit
}

// jobMark is the + or - which marks the current and previous jobs.
func jobMark(all []*bgProc, bg *bgProc) string {
	switch n := len(all); {
	case bg == all[n-1]:
		return "+"
	case n > 1 && bg == all[n-2]:
		return "-"
	}
	return " "
}

func (r *Runner) jobsBuiltin(args []string) exitStatus {
	form := "" // like Bash, the last of -l, -n and -p wins
	runningOnly, stoppedOnly := false, false
	fp := flagParser{remaining: args}
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-l", "-n", "-p":
			form = flag
		case "-r":
			runningOnly = true
		case "-s":
			stoppedOnly = true // nothing is ever stopped
		default:
			r.errf("jobs: invalid option %q\n", flag)
			return exitStatus{code: 2}
		}
	}
	all := r.jobList()
	jobs, exit := all, exitStatus{}
	if specs := fp.args(); len(specs) > 0 {
		jobs, exit = r.findJobs("jobs", specs)
	}
	for _, bg := range jobs {
		done := isDone(bg.done)
		if stoppedOnly || (runningOnly && done) {
			continue
		}
		if form == "-p" {
			r.outf("%s\n", r.bgProcID(bg))
			continue // like Bash, this does not count as reporting the job
		}
		if form == "-n" && !done && bg.notified {
			continue
		}
		status, suffix := "Running", " &"
		if done {
			suffix = ""
			switch code := bg.exit.code; {
			case code == 0:
				status = "Done"
			case code > 128 && int(code-128) < len(signalNames) && signalNames[code-128] != "":
				status = "Terminated"
			default:
				status = fmt.Sprintf("Exit %d", code)
			}
		}
		prefix := fmt.Sprintf("[%d]%s  ", bg.num, jobMark(all, bg))
		if form == "-l" {
			prefix = fmt.Sprintf("[%d]%s %-6s", bg.num, jobMark(all, bg), r.bgProcID(bg))
		}
		r.outf("%s%-27s%s%s\n", prefix, status, jobText(bg), suffix)
		bg.notified = true
		if done {
			bg.hidden = true // like Bash, only report finished jobs once
		}
	}
	return exit
}

func (r *Runner) disownBuiltin(args []string) exitStatus {
	all, runningOnly, keep := false, false, false
	fp := flagParser{remaining: args}
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-a":
			all = true
		case "-r":
			runningOnly = true
		case "-h":
			keep = true // nothing sends SIGHUP but BackgroundContext, which ends every job
		default:
			r.errf("disown: invalid option %q\n", flag)
			return exitStatus{code: 2}
		}
	}
	jobs, exit := r.jobList(), exitStatus{}
	if !all && (!runningOnly || len(fp.args()) > 0) {
		specs := fp.args()
		if len(specs) == 0 {
			specs = []string{"%+"}
		}
		jobs, exit = r.findJobs("disown", specs)
	}
	for _, bg := range jobs {
		if !keep && !(runningOnly && isDone(bg.done)) {
			bg.hidden = true
		}
	}
	return exit
}

func (r *Runner) killBuiltin(args []string) exitStatus {
	sig := uint8(15) // TERM
	if len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' {
		opt := args[0][1:]
		args = args[1:]
		switch opt {
		case "-":
			opt = "TERM"
		case "l", "L":
			if len(args) == 0 {
				for n, name := range signalNames {
					if name != "" {
						r.outf("%d) SIG%s\n", n, name)
					}
				}
			}
			for _, arg := range args {
				n, ok := parseSignal(arg)
				if !ok || n == 0 {
					r.errf("kill: %s: invalid signal specification\n", arg)
					return exitStatus{code: 1}
				}
				if arg == strconv.Itoa(int(n)) {
					r.outf("%s\n", signalNames[n])
				} else {
					r.outf("%d\n", n)
				}
			}
			return exitStatus{}
		case "s", "n":
			if len(args) == 0 {
				r.errf("kill: -%s: option requires an argument\n", opt)
				return exitStatus{code: 2}
			}
			opt, args = args[0], args[1:]
		}
		var ok bool
		if sig, ok = parseSignal(opt); !ok {
			r.errf("kill: %s: invalid signal specification\n", opt)
			return exitStatus{code: 1}
		}
	}
	if len(args) == 0 {
		r.errf("kill: usage: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ... or kill -l [sigspec]\n")
		return exitStatus{code: 2}
	}
	jobs, exit := r.findJobs("kill", args)
	for _, bg := range jobs {
		switch {
		case !isDone(bg.done):
			if sig != 0 {
				bg.cancel(killSignal(sig))
			}
		case sig == 0:
			r.errf("kill: (%s) - No such process\n", r.bgProcID(bg))
			exit.code = 1
		}
	}
	return exit
}

// fgBuiltin waits for a job and returns its status, as there is no terminal
// to give it.
func (r *Runner) fgBuiltin(ctx context.Context, args []string) exitStatus {
	if len(args) == 0 {
		if len(r.jobList()) == 0 {
			r.errf("fg: current: no such job\n")
			return exitStatus{code: 1}
		}
		args = []string{"%+"}
	}
	bg, err := r.findJob(args[0])
	if err != nil {
		r.errf("fg: %v\n", err)
		return exitStatus{code: 1}
	}
	r.outf("%s\n", jobText(bg))
	var exit exitStatus
	if !waitDone(ctx, bg.done, &exit) {
		return exit
	}
	bg.hidden = true
	return *bg.exit
}

// bgBuiltin reports on jobs which already run in the background.
func (r *Runner) bgBuiltin(args []string) exitStatus {
	all := r.jobList()
	if len(args) == 0 {
		if len(all) == 0 {
			r.errf("bg: current: no such job\n")
			return exitStatus{code: 1}
		}
		args = []string{"%+"}
	}
	jobs, exit := r.findJobs("bg", args)
	for _, bg := range jobs {
		if isDone(bg.done) {
			r.errf("bg: job %d has terminated\n", bg.num)
			exit.code = 1
			continue
		}
		r.outf("[%d]%s %s &\n", bg.num, jobMark(all, bg), jobText(bg))
	}
	return exit
}
