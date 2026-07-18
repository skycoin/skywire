// Package cliversion cmd/skywire-cli/commands/version/root.go c4-vis-cli
//
// `skywire cli version` reports the local build (version/commit/date) and,
// unless --local is given, resolves the latest available version and the
// rolling-release branch tip the SAME way the Go toolchain does when you run
// `go install github.com/skycoin/skywire/...@latest` from outside the source:
// via the Go module proxy protocol.
//
// Resolution is authoritative through `go list -m -json <module>@<query>` (which
// honors GOPROXY + direct fallback, branch refs, tags, case-escaping — exactly
// Go's own logic) when the `go` toolchain is on PATH. With no `go` binary it
// falls back to a direct GOPROXY `@latest` HTTP query for the latest release
// (proxy.golang.org serves released tags but not arbitrary branch refs).
package cliversion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/buildinfo"
)

const defaultModule = "github.com/skycoin/skywire"

var (
	localOnly bool
	branch    string
	modulePat string
	proxyBase string
	timeout   time.Duration
)

func init() {
	RootCmd.Flags().BoolVarP(&localOnly, "local", "l", false, "only print the local build, skip the network check")
	RootCmd.Flags().StringVarP(&branch, "branch", "b", "develop", "rolling-release branch to resolve the latest commit of")
	RootCmd.Flags().StringVarP(&modulePat, "module", "m", moduleFromBuild(), "Go module path to resolve")
	RootCmd.Flags().StringVar(&proxyBase, "proxy", "", "GOPROXY base URL override (default: $GOPROXY, else proxy.golang.org)")
	RootCmd.Flags().DurationVar(&timeout, "check-timeout", 20*time.Second, "timeout for the latest-version resolution")
}

// RootCmd is `skywire cli version`.
var RootCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the build version and check the latest available release/commit",
	Long: `Show the local build (version/commit/date) and, unless --local, resolve the
latest available release and the rolling-release branch tip the way the Go
toolchain resolves '@latest' — via the Go module proxy.`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		local := buildinfo.Get()
		out := &report{Local: local}

		if !localOnly {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			out.resolve(ctx, modulePat, branch)
		}

		internal.PrintOutput(cmd.Flags(), out, out.human())
	},
}

// report is the full version report (also the --json shape).
type report struct {
	Local   *buildinfo.Info `json:"local"`
	Module  string          `json:"module,omitempty"`
	Release *modVersion     `json:"latest_release,omitempty"`
	Branch  *modVersion     `json:"branch_tip,omitempty"`
	Status  string          `json:"status,omitempty"`
	Errors  []string        `json:"errors,omitempty"`
}

// modVersion is a resolved module version.
type modVersion struct {
	Query   string `json:"query"`            // "latest" or the branch name
	Version string `json:"version"`          // semantic version or pseudo-version
	Commit  string `json:"commit,omitempty"` // full VCS hash
	Ref     string `json:"ref,omitempty"`    // e.g. refs/tags/v1.3.64
	Time    string `json:"time,omitempty"`   // RFC3339
	Via     string `json:"resolved_via"`     // "go list" or "goproxy"
}

func (r *report) resolve(ctx context.Context, module, br string) {
	r.Module = module

	if rel, err := resolveVersion(ctx, module, "latest"); err == nil {
		r.Release = rel
	} else {
		r.Errors = append(r.Errors, fmt.Sprintf("latest release: %v", err))
	}

	tip, err := resolveVersion(ctx, module, br)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("%s tip: %v", br, err))
		return
	}
	r.Branch = tip
	r.Status = compareStatus(buildinfo.Commit(), tip.Commit, br)
}

// resolveVersion resolves module@query the canonical way: prefer the `go`
// toolchain (authoritative, honors GOPROXY+direct, handles branch refs); fall
// back to a direct GOPROXY HTTP query when `go` is unavailable.
func resolveVersion(ctx context.Context, module, query string) (*modVersion, error) {
	if _, err := exec.LookPath("go"); err == nil {
		if mv, err := resolveViaGoList(ctx, module, query); err == nil {
			return mv, nil
		} else if query == "latest" {
			// only fall back to the proxy for @latest; branch refs aren't
			// served by proxy.golang.org, so surface the go-list error.
			if mv, perr := resolveViaProxy(ctx, module, query); perr == nil {
				return mv, nil
			}
			return nil, err
		} else {
			return nil, err
		}
	}
	return resolveViaProxy(ctx, module, query)
}

// resolveViaGoList shells out to `go list -m -json module@query`. Runs in module
// mode from a neutral directory so it works "from outside the source code".
func resolveViaGoList(ctx context.Context, module, query string) (*modVersion, error) {
	// module/query come from the operator's own flags (safe defaults), invoking
	// the read-only `go list` on their own machine — not an injection surface.
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", module+"@"+query) //nolint:gosec
	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	cmd.Dir = os.TempDir()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("go list: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go list: %w", err)
	}
	var m struct {
		Version string `json:"Version"`
		Time    string `json:"Time"`
		Origin  struct {
			Hash string `json:"Hash"`
			Ref  string `json:"Ref"`
		} `json:"Origin"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("parse go list: %w", err)
	}
	return &modVersion{
		Query:   query,
		Version: m.Version,
		Commit:  m.Origin.Hash,
		Ref:     m.Origin.Ref,
		Time:    m.Time,
		Via:     "go list",
	}, nil
}

// resolveViaProxy queries the Go module proxy directly (no go toolchain). Used
// for @latest; proxy.golang.org does not serve arbitrary branch refs.
func resolveViaProxy(ctx context.Context, module, query string) (*modVersion, error) {
	base := goProxyBase()
	if base == "" {
		return nil, fmt.Errorf("no usable GOPROXY (set $GOPROXY or use --proxy)")
	}
	esc := escapeModulePath(module)
	var url string
	if query == "latest" {
		url = base + "/" + esc + "/@latest"
	} else {
		url = base + "/" + esc + "/@v/" + query + ".info"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", base, resp.Status)
	}
	var m struct {
		Version string `json:"Version"`
		Time    string `json:"Time"`
		Origin  struct {
			Hash string `json:"Hash"`
			Ref  string `json:"Ref"`
		} `json:"Origin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &modVersion{
		Query:   query,
		Version: m.Version,
		Commit:  m.Origin.Hash,
		Ref:     m.Origin.Ref,
		Time:    m.Time,
		Via:     "goproxy",
	}, nil
}

// goProxyBase returns the first http(s) proxy from GOPROXY (or --proxy override),
// defaulting to proxy.golang.org. GOPROXY entries are separated by ',' or '|'.
func goProxyBase() string {
	if proxyBase != "" {
		return strings.TrimRight(proxyBase, "/")
	}
	gp := os.Getenv("GOPROXY")
	if gp == "" {
		return "https://proxy.golang.org"
	}
	for _, p := range strings.FieldsFunc(gp, func(r rune) bool { return r == ',' || r == '|' }) {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
			return strings.TrimRight(p, "/")
		}
	}
	// GOPROXY had only "direct"/"off" — fall back to the public proxy.
	return "https://proxy.golang.org"
}

// escapeModulePath applies the module proxy path escaping (uppercase -> !lower),
// matching golang.org/x/mod/module.EscapePath.
func escapeModulePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// compareStatus compares the local build commit to the resolved branch tip.
func compareStatus(localCommit, tipCommit, br string) string {
	lc := strings.ToLower(strings.TrimSpace(localCommit))
	tc := strings.ToLower(strings.TrimSpace(tipCommit))
	if lc == "" || lc == "unknown" || tc == "" {
		return "unknown (local build commit not embedded)"
	}
	// local commit is often the short 12-char form; tip is the full hash.
	if strings.HasPrefix(tc, lc) || strings.HasPrefix(lc, tc) {
		return fmt.Sprintf("up to date with %s", br)
	}
	return fmt.Sprintf("behind %s — update available", br)
}

// moduleFromBuild returns the module path embedded in the build, defaulting to
// the canonical skywire module.
func moduleFromBuild() string {
	if bi := buildinfo.DebugBuildInfo(); bi != nil && bi.Main.Path != "" {
		return bi.Main.Path
	}
	return defaultModule
}

func (r *report) human() string {
	var b strings.Builder
	li := r.Local
	fmt.Fprintf(&b, "skywire %s  (local build)\n", li.Version)
	fmt.Fprintf(&b, "  commit  %s\n", orUnknown(li.Commit))
	fmt.Fprintf(&b, "  built   %s\n", orUnknown(li.Date))
	fmt.Fprintf(&b, "  go      %s  %s/%s\n", li.Go, li.OS, li.Arch)

	if localOnly {
		return b.String()
	}

	fmt.Fprintf(&b, "\nlatest  %s\n", r.Module)
	if r.Release != nil {
		fmt.Fprintf(&b, "  release  %-34s %s  [%s]\n", r.Release.Version, fmtTime(r.Release.Time), r.Release.Via)
	}
	if r.Branch != nil {
		fmt.Fprintf(&b, "  %-7s  %-34s %s  [%s]\n", branch, r.Branch.Version, fmtTime(r.Branch.Time), r.Branch.Via)
		fmt.Fprintf(&b, "           commit %s\n", shortCommit(r.Branch.Commit))
	}
	if r.Status != "" {
		fmt.Fprintf(&b, "  status   %s\n", r.Status)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "  ! %s\n", e)
	}
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func fmtTime(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format("2006-01-02 15:04Z")
	}
	return s
}
