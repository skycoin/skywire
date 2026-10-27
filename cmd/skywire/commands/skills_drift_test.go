// Package commands cmd/skywire/commands/skills_drift_test.go
package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/flags"
)

var (
	skillSpan   = regexp.MustCompile("`([^`\n]+)`")
	skillQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	skillSplit  = regexp.MustCompile(`\s*(\|\||&&|\||;|>|<\(|\$\(|\))\s*`)
	skillWord   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// TestSkillsMatchCommandTree checks the agent skills in .claude/skills
// against the real cobra tree. Every `skywire ...` command in backticks or
// in a code block must resolve to a command, and every flag written with it
// must exist on that command.
func TestSkillsMatchCommandTree(t *testing.T) {
	files, err := filepath.Glob("../../../.claude/skills/*/SKILL.md")
	if err != nil || len(files) == 0 {
		t.Skip("no skills found")
	}
	flags.InstallHelpTree(RootCmd)

	var problems []string
	for _, f := range files {
		skill := filepath.Base(filepath.Dir(f))
		data, err := os.ReadFile(f) //nolint:gosec
		if err != nil {
			t.Fatal(err)
		}
		for _, seg := range skillSegments(string(data)) {
			problems = append(problems, checkSkillSegment(skill, seg)...)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// skillSegments returns every command-looking piece of the skill text,
// one per command invocation.
func skillSegments(text string) []string {
	var spans []string
	inCode := false
	var cont string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			cont = ""
			continue
		}
		if inCode {
			l := strings.TrimSpace(line)
			if strings.HasSuffix(l, "\\") {
				cont += strings.TrimSuffix(l, "\\") + " "
				continue
			}
			spans = append(spans, cont+l)
			cont = ""
			continue
		}
		for _, m := range skillSpan.FindAllStringSubmatch(line, -1) {
			spans = append(spans, m[1])
		}
	}
	var out []string
	for _, s := range spans {
		s = skillQuoted.ReplaceAllString(s, "ARG")
		for _, part := range skillSplit.Split(s, -1) {
			part = strings.TrimPrefix(strings.TrimSpace(part), "$ ")
			w := strings.Fields(part)
			if len(w) > 0 && filepath.Base(w[0]) == "skywire" {
				w[0] = "skywire"
				out = append(out, strings.Join(w, " "))
			} else if len(w) > 1 && isCLIGroup(w[0]) {
				out = append(out, "~skywire cli "+strings.Join(w, " "))
			}
		}
	}
	return out
}

func checkSkillSegment(skill, seg string) []string {
	implicit := strings.HasPrefix(seg, "~")
	seg = strings.TrimPrefix(seg, "~")
	words := strings.Fields(seg)
	cmd := RootCmd
	var problems []string
	i := 1
	for ; i < len(words); i++ {
		w := words[i]
		if !skillWord.MatchString(w) {
			break
		}
		next := child(cmd, w)
		if next == nil {
			if implicit {
				return nil // prose that merely starts with a command word
			}
			if cmd.HasSubCommands() && cmd.Args == nil {
				problems = append(problems, "skill "+skill+": `"+seg+"` names no command, "+
					"`"+cmd.CommandPath()+"` has no subcommand \""+w+"\"")
			}
			break
		}
		cmd = next
	}
	for ; i < len(words); i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "-" || w == "--" {
			continue
		}
		name := strings.TrimLeft(w, "-")
		if k := strings.IndexAny(name, "=</"); k >= 0 {
			name = name[:k]
		}
		if name == "" || name == "h" || name == "help" || strings.ContainsAny(name, "[]|.") {
			continue
		}
		if !skillHasFlag(cmd, strings.HasPrefix(w, "--"), name) {
			problems = append(problems, "skill "+skill+": `"+seg+"` uses flag \""+w+
				"\" which `"+cmd.CommandPath()+"` does not have")
		}
	}
	return problems
}

func skillHasFlag(cmd *cobra.Command, long bool, name string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		pf := c.PersistentFlags()
		fs := pf
		if c == cmd {
			fs = c.Flags()
		}
		if long {
			if fs.Lookup(name) != nil || pf.Lookup(name) != nil {
				return true
			}
		} else {
			r := string([]rune(name)[0])
			if fs.ShorthandLookup(r) != nil || pf.ShorthandLookup(r) != nil {
				return true
			}
		}
	}
	return false
}

var skillWildcard = regexp.MustCompile(`--[a-z0-9-]+\*`)

var skillLongFlag = regexp.MustCompile(`(?:^|[\s(\x60])--([a-z][a-z0-9-]+)`)

// TestSkillFlagsExistSomewhere is a coarser check than the one above. A flag
// written in a span or code block, even without its command, must exist on
// at least one command, unless it belongs to a tool the skills mention.
func TestSkillFlagsExistSomewhere(t *testing.T) {
	files, _ := filepath.Glob("../../../.claude/skills/*/SKILL.md")
	if len(files) == 0 {
		t.Skip("no skills found")
	}
	flags.InstallHelpTree(RootCmd)
	known := map[string]bool{"help": true, "max-time": true, "remote-debugging-port": true}
	walk(RootCmd, func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { known[f.Name] = true })
	})
	for _, f := range files {
		skill := filepath.Base(filepath.Dir(f))
		data, err := os.ReadFile(f) //nolint:gosec
		if err != nil {
			t.Fatal(err)
		}
		inCode := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inCode = !inCode
				continue
			}
			var texts []string
			if inCode {
				texts = []string{line}
			} else {
				for _, m := range skillSpan.FindAllStringSubmatch(line, -1) {
					texts = append(texts, m[1])
				}
			}
			for _, txt := range texts {
				if skillForeignTool.MatchString(txt) {
					continue
				}
				txt = skillWildcard.ReplaceAllString(txt, "")
				for _, m := range skillLongFlag.FindAllStringSubmatch(txt, -1) {
					if !known[m[1]] {
						t.Errorf("skill %s: flag --%s in `%s` exists on no skywire command", skill, m[1], strings.TrimSpace(txt))
					}
				}
			}
		}
	}
}

// skillForeignTool matches spans that run another program, whose flags are
// not ours to check.
var skillForeignTool = regexp.MustCompile(`^\s*(\$ )?(curl|go|git|gh|make|tinygo|jq|systemctl|journalctl|ss|perf|gdb|nm|tail|grep|sudo|systemd-run|ssh|fusermount|dig|iperf3?|nc|ping|chmod|pacman|apt|docker)\b`)

// isCLIGroup reports whether name is a command directly under `skywire cli`.
// The skills often drop the `skywire cli` prefix once it is obvious.
func isCLIGroup(name string) bool {
	cli := child(RootCmd, "cli")
	return cli != nil && child(cli, name) != nil
}
