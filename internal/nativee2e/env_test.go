//go:build client_e2e
// +build client_e2e

// Package nativee2e — internal/nativee2e/env_test.go: a NATIVE (no-Docker)
// client-side e2e harness that runs a small skywire deployment + two visors as
// host processes on 127.0.0.1, so client behaviour (visor, hypervisor,
// skysocks-client, vpn-client) can be tested on macOS and Windows — where the
// Docker-based internal/integration suite can't run.
//
// It is gated behind the `client_e2e` build tag (like the Docker suite's
// `!no_ci`) so it never runs in the normal unit lanes. TestMain builds the
// skywire binary + the app binaries, writes the embedded testdata configs into a
// temp workdir, starts `skywire svc run` (dmsg-disc/server, tpd, ar, rf, sn, tps
// — all in-memory stores, no redis) and two visors, waits for both to connect to
// dmsg, then runs the tests and tears everything down.
//
// Nothing here needs Docker or redis: the dmsg-discovery uses its in-memory mock
// store (test_mode + no redis URL) and the visors are private (is_public=false)
// so they boot with no STUN/public-IP. See docs in the config files under
// testdata/.
package nativee2e

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/*.json
var testdataFS embed.FS

// Ports/identities are fixed by the checked-in testdata configs.
const (
	rpcA        = "localhost:3435" // client visor (also runs the hypervisor on :8000)
	rpcB        = "localhost:3436" // server visor (skysocks + vpn-server)
	hypervisorA = "http://127.0.0.1:8000"
	dmsgDiscURL = "http://127.0.0.1:9090"
	socksAddr   = "127.0.0.1:1080" // skysocks-client SOCKS5 listener (default)
	// egressTarget is an in-network HTTP service reachable from the SERVER visor's
	// egress (localhost) — the transport-discovery health endpoint. A proxied GET
	// that returns this proves traffic crossed the skywire route and egressed at B.
	egressTarget = "http://127.0.0.1:9094/health"
)

// env is the shared harness state, set up in TestMain.
var env struct {
	work  string // temp working directory (cwd for every process)
	bin   string // path to the built skywire binary
	procs []*exec.Cmd
}

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "nativee2e setup failed: %v\n", err)
		teardown()
		os.Exit(1)
	}
	code := m.Run()
	teardown()
	os.Exit(code)
}

// setup builds the binaries, writes configs, and starts the deployment + visors.
func setup() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	env.work, err = os.MkdirTemp("", "skywire-nativee2e-")
	if err != nil {
		return err
	}
	fmt.Printf("nativee2e workdir: %s\n", env.work)

	// 1. Write embedded configs into the workdir.
	if err := writeConfigs(); err != nil {
		return fmt.Errorf("write configs: %w", err)
	}

	// 2. Provide the skywire binary + the app binaries the visor launcher spawns.
	// SKYWIRE_NATIVEE2E_BIN, when set, is a dir of already-built binaries — used
	// as-is (fast local iteration / a prior CI build step). Otherwise build them
	// into <workdir>/bin.
	binDir := filepath.Join(env.work, "bin")
	if pre := os.Getenv("SKYWIRE_NATIVEE2E_BIN"); pre != "" {
		binDir = pre
		if err := rewriteBinPath(binDir); err != nil {
			return err
		}
		fmt.Printf("using prebuilt binaries: %s\n", binDir)
	} else {
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return err
		}
		fmt.Println("building skywire + apps (this takes a moment)...")
		if err := goBuild(root, filepath.Join(binDir, exe("skywire")), "./cmd/skywire"); err != nil {
			return fmt.Errorf("build skywire: %w", err)
		}
		for _, app := range []string{"skychat", "skysocks", "skysocks-client", "vpn-server", "vpn-client"} {
			if err := goBuild(root, filepath.Join(binDir, exe(app)), "./cmd/apps/"+app); err != nil {
				return fmt.Errorf("build %s: %w", app, err)
			}
		}
	}
	env.bin = filepath.Join(binDir, exe("skywire"))

	// 3. Start the deployment, then the two visors.
	if err := startProc("svc", env.bin, "svc", "run", "--config", "services.json"); err != nil {
		return err
	}
	if err := waitDmsgDisc(60 * time.Second); err != nil {
		return fmt.Errorf("deployment not ready: %w", err)
	}
	if err := startProc("visorA", env.bin, "visor", "-c", "visorA.json"); err != nil {
		return err
	}
	if err := startProc("visorB", env.bin, "visor", "-c", "visorB.json"); err != nil {
		return err
	}
	for name, rpc := range map[string]string{"visorA": rpcA, "visorB": rpcB} {
		if err := waitVisor(rpc, 180*time.Second); err != nil {
			dumpLog(name)
			dumpLog("svc")
			return fmt.Errorf("visor %s not ready: %w", rpc, err)
		}
	}
	// Warm-up: the dmsg backbone + route-finder/setup-node need a little time to
	// settle on a freshly-started single-server loopback deployment before route
	// setup (skysocks/vpn) is reliable — same cold-start the docker e2e absorbs
	// with staged healthchecks. A fixed pause here keeps the route-dependent tests
	// from racing the still-churning network.
	fmt.Println("nativee2e: visors ready; warming up the network (90s)...")
	time.Sleep(90 * time.Second)
	fmt.Println("nativee2e: deployment + 2 visors ready")
	return nil
}

// dumpLogCausePatterns are the log substrings that identify a process's real
// failure — a fatal visor-module init OR a deployment listener/serve failure
// (the dmsg-server data-plane binding :8080, QUIC/WS/health listeners, panics,
// port clashes). These otherwise get pushed off the tail by retry churn.
var dumpLogCausePatterns = []string{
	"Module init failed", "initializing module", "failed to start",
	"a fatal error occurred", "data-plane server stopped", "http health server stopped",
	"QUIC server stopped", "WebSocket serving stopped", "failed to bind",
	"address already in use", "bind:", "panic:", "Serving dmsg",
}

// dumpLog prints, for post-mortem on failure: (1) the lines matching a known
// failure pattern (root cause), (2) the log HEAD (startup — where a service binds
// its listeners, e.g. the dmsg-server on :8080), and (3) the log tail.
func dumpLog(name string) {
	b, err := os.ReadFile(filepath.Join(env.work, name+".log"))
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	var causes []string
	for _, l := range lines {
		for _, p := range dumpLogCausePatterns {
			if strings.Contains(l, p) {
				causes = append(causes, l)
				break
			}
		}
	}
	head := lines
	if len(head) > 40 {
		head = head[:40]
	}
	from := 0
	if len(lines) > 60 {
		from = len(lines) - 60
	}
	fmt.Fprintf(os.Stderr,
		"\n===== %s.log — ROOT CAUSE =====\n%s\n===== %s.log (head) =====\n%s\n===== %s.log (tail) =====\n%s\n=========================\n",
		name, strings.Join(causes, "\n"), name, strings.Join(head, "\n"), name, strings.Join(lines[from:], "\n"))
}

func teardown() {
	// Interrupt (so the visor stops its child apps cleanly), then hard-kill.
	for _, p := range env.procs {
		if p.Process != nil {
			_ = p.Process.Signal(os.Interrupt)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for _, p := range env.procs {
		if p.Process == nil {
			continue
		}
		done := make(chan struct{})
		go func() { _ = p.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Until(deadline)):
			_ = p.Process.Kill()
		}
	}
	if env.work != "" {
		_ = os.RemoveAll(env.work)
	}
}

// --- process + build helpers -------------------------------------------------

// startProc launches a skywire subprocess with cwd=workdir (so the relative
// paths in the configs resolve) and SKYDEPLOY pointing at the native deployment.
// stdout/stderr go to <name>.log in the workdir for post-mortem.
func startProc(name, bin string, args ...string) error {
	logf, err := os.Create(filepath.Join(env.work, name+".log"))
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = env.work
	cmd.Env = append(os.Environ(), "SKYDEPLOY=services-config.json")
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	env.procs = append(env.procs, cmd)
	return nil
}

// rewriteBinPath points both visor configs' launcher.bin_path at an absolute
// prebuilt-binary dir (default configs use the relative "./bin"). The path is
// forward-slashed: a Windows absolute path (e.g. `D:\a\skywire\skywire/_nbin`)
// contains backslashes that are invalid JSON string escapes (`\a`, `\s`) and
// would make the visor fail to parse its config; Go on Windows accepts
// forward-slash paths, so this is safe on every OS.
func rewriteBinPath(binDir string) error {
	binDir = filepath.ToSlash(binDir)
	for _, name := range []string{"visorA.json", "visorB.json"} {
		p := filepath.Join(env.work, name)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := strings.Replace(string(b), `"bin_path": "./bin"`, `"bin_path": "`+binDir+`"`, 1)
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func goBuild(root, out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, b)
	}
	return nil
}

// cli runs `skywire cli <args...>` with a 30s cap and returns trimmed stdout.
func cli(args ...string) (string, error) { return cliT(30*time.Second, args...) }

// cliT is cli with an explicit timeout — for long ops like `proxy start` /
// `vpn start`, which poll route/TUN readiness up to their own --timeout.
func cliT(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, env.bin, append([]string{"cli"}, args...)...)
	cmd.Dir = env.work
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// --- readiness helpers -------------------------------------------------------

func waitDmsgDisc(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := httpGet(dmsgDiscURL + "/dmsg-discovery/entries"); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("dmsg-discovery not reachable on %s", dmsgDiscURL)
}

// waitVisor polls the visor RPC for its PK, then for at least one dmsg session.
func waitVisor(rpc string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := cli("visor", "--rpc", rpc, "pk")
		if err == nil && has66Hex(out) {
			// RPC up; now wait for a dmsg session.
			s, _ := cli("dmsg", "--rpc", rpc, "sessions")
			if strings.Contains(s, "Connected sessions:") && !strings.Contains(s, "Connected sessions: 0") {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("visor on %s never reached ready (RPC + dmsg session)", rpc)
}

// --- small utilities ---------------------------------------------------------

func writeConfigs() error {
	entries, err := testdataFS.ReadDir("testdata")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := testdataFS.ReadFile("testdata/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(env.work, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// repoRoot walks up from this test file's package until it finds go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func has66Hex(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 66 {
			hex := line[len(line)-66:]
			ok := true
			for _, c := range hex {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}
