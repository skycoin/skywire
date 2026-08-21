// Package clilog cmd/skywire-cli/commands/log/log.go c4-vis-cli
package clilog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/net/proxy"

	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

func init() {
	logCmd.Flags().SortFlags = false //nolint:errcheck
	logCmd.Flags().BoolVarP(&logOnly, "log", "l", false, "fetch only transport logs")
	logCmd.Flags().BoolVarP(&surveyOnly, "survey", "v", false, "fetch only surveys")
	logCmd.Flags().StringVarP(&fetchFile, "file", "f", "", "fetch only a specific file from all online visors")
	logCmd.Flags().StringVarP(&fetchFrom, "pks", "k", "", "fetch only from specific public keys ; semicolon separated")
	logCmd.Flags().StringVarP(&writeDir, "dir", "d", "log_collecting", "save files to specified dir")
	logCmd.Flags().BoolVarP(&deleteOnErrors, "clean", "c", false, "delete files and folders on errors")
	logCmd.Flags().StringVar(&minv, "minv", "v1.3.19", "minimum visor version to fetch from (\"auto\" = the dynamic 14-day reward-eligibility floor from GitHub releases)")
	logCmd.Flags().StringVar(&incVer, "include-versions", "", "list of version that not satisfy our minimum version condition, but we want include them")
	logCmd.Flags().IntVarP(&duration, "duration", "n", 0, "number of days before today to fetch transport logs for")
	logCmd.Flags().BoolVar(&allVisors, "all", false, "consider all visors ; no version filtering")
	logCmd.Flags().IntVar(&batchSize, "batchSize", 50, "number of visor in each batch")
	logCmd.Flags().Int64Var(&maxFileSize, "maxfilesize", 1024, "maximum file size allowed to download during collecting logs, in KB")
	logCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDiscURL, "dmsg discovery url\n")
	logCmd.Flags().StringVarP(&utAddr, "ut", "u", utURL, "uptime tracker url (default: TPD-integrated /uptimes)\n")
	if os.Getenv("DMSGCURL_SK") != "" {
		sk.Set(os.Getenv("DMSGCURL_SK")) //nolint:errcheck,gosec
	}
	logCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	logCmd.Flags().BoolVar(&runCleanup, "cleanup", true, "run cleanup after collection (remove old/invalid files)")
	logCmd.Flags().StringVar(&backupDir, "backup-dir", "log_backups", "backup directory to also clean")
	logCmd.Flags().IntVar(&maxAgeDays, "max-age", 7, "maximum age in days for files before deletion")
	logCmd.Flags().StringVar(&pruneBelowVer, "prune-below-version", "", "during cleanup, also remove existing surveys whose skywire_version is below this (e.g. v1.3.43; \"auto\" = the dynamic 14-day reward floor)")
	logCmd.Flags().StringVar(&proxyAddr, "proxy", "", "fetch via a dmsgweb SOCKS5 resolving proxy (host:port, e.g. 127.0.0.1:4443)\ninstead of this command's own dmsg client — reuses the proxy's warm\ndmsg sessions (e.g. a running `skywire dmsg web`), avoiding cold first-contact timeouts")
	logCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Survey & transport-log collection",
	Long:  fmt.Sprintf("Fetch health, survey, and transport logging from visors which are online in the TPD-integrated uptime tracker\n%[1]s/uptimes?v=v3\n%[1]s/uptimes?v=v3&visors=<pk1>;<pk2>;<pk3>", deployment.Prod.TransportDiscovery),
	Run: func(cmd *cobra.Command, _ []string) {
		log := logging.MustGetLogger("log-collecting")
		fver, err := version.NewVersion("v1.3.17")
		if err != nil {
			log.Fatal("can't parse version for filtering fetches")
		}
		if logOnly && surveyOnly {
			log.Fatal("use of mutually exclusive flags --log and --survey")
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		go func() {
			<-ctx.Done()
			cancel()
			os.Exit(1)
		}()

		// Preparing directories
		if _, err := os.ReadDir(writeDir); err != nil {
			if err := os.Mkdir(writeDir, 0750); err != nil {
				log.Error("Unable to create directory " + writeDir)
				return
			}
		}

		if err := os.Chdir(writeDir); err != nil {
			log.Error("Unable to change directory to " + writeDir)
			return
		}

		// visorTransport is the RoundTripper used to reach the uptime tracker and every
		// per-visor endpoint. Two modes:
		//   default  — open our OWN dmsg client (pre-seeded from the embedded prod server
		//              entries) and dial visors directly as dmsg://<pk>:80/... . First
		//              contact to a never-dialed peer is a COLD session that can exceed the
		//              per-request timeout (hence the two-pass retry below).
		//   --proxy  — fetch through an external dmsgweb SOCKS5 resolving proxy (e.g. a
		//              running `skywire dmsg web` with a survey-whitelisted key) that
		//              already holds WARM dmsg sessions; visors are reached as
		//              http://<pk>.dmsg/... and the proxy resolves + dials them. No dmsg
		//              bootstrap here — this is what the live reward host uses (it already
		//              runs such a proxy), so cold-session first-contact timeouts vanish.
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		endpoint := utAddr + "/uptimes?v=v3"
		if fetchFrom != "" {
			endpoint = utAddr + "/uptimes?v=v3&visors=" + fetchFrom
		}

		// Both the uptime tracker and per-visor surveys are reached over dmsg.
		// --proxy routes everything through a warm dmsgweb SOCKS5 resolving proxy
		// (http://<pk>.dmsg/..., resolved + dialed by the proxy — which holds warm
		// sessions and, for a survey-whitelisted key, reaches deployment services
		// like the TPD-integrated uptime tracker too); default mode opens this
		// command's own dmsg client and dials dmsg://<pk>:80/... directly.
		var visorTransport http.RoundTripper
		if proxyAddr != "" {
			tr, perr := socks5Transport(proxyAddr)
			if perr != nil {
				log.WithError(perr).Errorf("Invalid --proxy %q", proxyAddr)
				return
			}
			visorTransport = tr
			log.Infof("Collecting via dmsgweb SOCKS5 proxy %s (http://<pk>.dmsg/...)", proxyAddr)
		} else {
			// Route the bootstrap's per-PK Entry() fallback over dmsg-HTTP by default;
			// plain-HTTP discovery is only taken with a non-prod --dmsg-disc URL.
			httpURL, dmsgURL := dmsgDiscArgs(dmsgDisc)
			bootstrap, berr := cmdutil.BootstrapDmsg(ctx, log, pk, sk, dmsg.Prod.DmsgServers, httpURL, dmsgURL, "")
			if berr != nil {
				log.WithError(berr).Error("Failed to bootstrap dmsg client.")
				return
			}
			defer bootstrap.Close()
			dmsgC := bootstrap.Client
			// Connect to any additional servers the dmsg-disc knows about beyond the
			// embedded prod set (custom/test servers not baked into the binary).
			for _, server := range getAllDMSGServers(cmd.Flags()) {
				dmsgC.EnsureAndObtainSession(ctx, server.PK) //nolint:errcheck,gosec
			}
			visorTransport = dmsghttp.MakeHTTPTransport(ctx, dmsgC)
		}

		// Shared clients over visorTransport: a 60s one for the UT fetch (a single
		// large payload) and a 10s one for per-visor fetches. The transport pools
		// connections across the concurrent fetch goroutines below.
		utClient := &http.Client{Transport: visorTransport, Timeout: 60 * time.Second}
		visorClient := &http.Client{Transport: visorTransport, Timeout: 10 * time.Second}

		uptimes, err := getUptimes(ctx, endpoint, utClient, log)
		if err != nil {
			log.WithError(err).Error("Unable to get data from uptime tracker.")
			return
		}
		//randomize the order of the survey collection - workaround for hanging
		rand.Shuffle(len(uptimes), func(i, j int) {
			uptimes[i], uptimes[j] = uptimes[j], uptimes[i]
		})

		// Resolve the dynamic reward-eligibility floor when requested (--minv auto /
		// --prune-below-version auto): the latest skywire release published >= 14 days
		// ago. Go port of getlogs.sh's _compute_minversion, so the collector enforces
		// the floor itself — the reward calc does NOT re-gate on version, so the
		// collect+prune floor is the sole version gate.
		if minv == "auto" || pruneBelowVer == "auto" {
			floor := minVersionFloor(log)
			if minv == "auto" {
				minv = floor
			}
			if pruneBelowVer == "auto" {
				pruneBelowVer = floor
			}
			log.Infof("Reward min-version floor: %s (auto, 14-day)", floor)
		}

		minimumVersion, _ := version.NewVersion(minv) //nolint:errcheck
		incVerList := strings.Split(incVer, ",")

		start := time.Now()
		var bulkFolders []string
		var bulkMu sync.Mutex // bulkFolders is appended from concurrent fetch goroutines

		// fetchVisorSurvey collects health + survey (+ optional tp logs) for one visor.
		// Returns true when the visor was UNREACHABLE (its /health fetch failed) — a
		// candidate for the retry pass: a cold dmsg session to a peer we've never dialed
		// can exceed the per-request timeout on first contact yet succeed once the
		// session is warm. quiet suppresses the per-visor "unreachable" line so the first
		// pass stays silent and the operator only sees the final state.
		//
		// Attempts collection from EVERY visor the uptime tracker saw today, not only
		// those currently flagged Online: a visor can meet the daily uptime bar yet read
		// Online=false at the instant we poll (transient transport/heartbeat churn), and
		// gating on that flag SILENTLY dropped it from collection — earned uptime, no
		// survey, no reward, nothing logged. The /health fetch is the reachability gate
		// (an offline visor fails it) AND supplies the authoritative version.
		fetchVisorSurvey := func(key string, quiet bool) bool {
			httpC := *visorClient // shared transport (own dmsg client, or the SOCKS5 proxy)

			deleteOnError := false
			if _, err := os.ReadDir(key); err != nil {
				if err := os.Mkdir(key, 0750); err != nil {
					log.Errorf("Unable to create directory for visor %s", key)
					return false
				}
				deleteOnError = true
			}
			if err := download(ctx, log, httpC, "health", "health.json", key, maxFileSize); err != nil {
				if !quiet {
					log.Debugf("visor %s unreachable (health fetch failed) — skipping", key)
				}
				if deleteOnErrors && deleteOnError {
					bulkMu.Lock()
					bulkFolders = append(bulkFolders, key)
					bulkMu.Unlock()
				}
				return true // retry candidate
			}
			// Authoritative version gate from the visor's OWN /health (build_info.version).
			// Survey collection IS the version-eligibility gate downstream — version is
			// not re-checked at calc time — so an ineligible-version visor must NOT have
			// its survey stored.
			healthVer := parseHealthVersion(key + "/health.json")
			includeV := contains(incVerList, healthVer)
			vver, verr := version.NewVersion(healthVer)
			eligible := true
			switch {
			case healthVer == "" && !includeV:
				log.Warnf("The version for visor %s is blank in /health — skipping survey", key)
				eligible = false
			case verr != nil && !includeV:
				log.Warnf("The version %q for visor %s (from /health) is not valid — skipping survey", healthVer, key)
				eligible = false
			case verr == nil && !allVisors && vver.LessThan(minimumVersion) && !includeV:
				log.Warnf("The version %s for visor %s does not satisfy our minimum version condition — skipping survey", healthVer, key)
				eligible = false
			}
			if !logOnly && eligible {
				// vver may be nil for unparseable versions included via
				// --include-versions; then use the current node-info API.
				if vver != nil && vver.LessThan(fver) {
					download(ctx, log, httpC, "node-info.json", "node-info.json", key, maxFileSize) //nolint:errcheck,gosec
				} else if !surveyUnchanged(ctx, log, &httpC, key) {
					// checksum unchanged → skip; falls back to a full download if the
					// visor doesn't support the checksum endpoint.
					download(ctx, log, httpC, "node-info", "node-info.json", key, maxFileSize) //nolint:errcheck,gosec
				}
			}
			if !surveyOnly {
				for i := 0; i <= duration; i++ {
					date := time.Now().AddDate(0, 0, -i).UTC().Format("2006-01-02")
					download(ctx, log, httpC, date+".csv", date+".csv", key, maxFileSize) //nolint:errcheck,gosec
				}
			}
			return false
		}

		// runPass fetches every pk concurrently in throttled batches (--batchSize per
		// batch, 15s between batches) and returns the pks that were unreachable.
		runPass := func(pks []string, quiet bool) []string {
			var passWG sync.WaitGroup
			var failedMu sync.Mutex
			var failed []string
			batch := batchSize
			for _, pk := range pks {
				passWG.Add(1)
				go func(key string) {
					defer passWG.Done()
					if fetchVisorSurvey(key, quiet) {
						failedMu.Lock()
						failed = append(failed, key)
						failedMu.Unlock()
					}
				}(pk)
				if batch > 0 {
					batch--
					if batch == 0 {
						time.Sleep(15 * time.Second)
						batch = batchSize
					}
				}
			}
			passWG.Wait()
			return failed
		}

		if fetchFile != "" {
			// A specific file was requested: fetch it from every seen-today visor, no
			// version gate and no retry pass (single explicit artifact).
			var fileWG sync.WaitGroup
			batch := batchSize
			for _, v := range uptimes {
				fileWG.Add(1)
				go func(key string) {
					defer fileWG.Done()
					httpC := *visorClient
					if _, err := os.ReadDir(key); err != nil {
						if err := os.Mkdir(key, 0750); err != nil {
							log.Errorf("Unable to create directory for visor %s", key)
							return
						}
					}
					_ = download(ctx, log, httpC, fetchFile, fetchFile, key, maxFileSize) //nolint:errcheck
				}(v.PubKey)
				if batch > 0 {
					batch--
					if batch == 0 {
						time.Sleep(15 * time.Second)
						batch = batchSize
					}
				}
			}
			fileWG.Wait()
		} else {
			pks := make([]string, 0, len(uptimes))
			for _, v := range uptimes {
				pks = append(pks, v.PubKey)
			}
			// Two-pass collection, mirroring the live fetch_surveys.sh: pass 1 warms the
			// dmsg sessions (silent), pass 2 retries only the visors that missed on a
			// cold session — so a first-contact timeout no longer looks like an offline
			// visor and silently drops its survey.
			failed := runPass(pks, true)
			if len(failed) > 0 {
				log.Infof("Retrying %d visor(s) that failed the first pass (dmsg sessions now warm)", len(failed))
				runPass(failed, false)
			}
		}

		for _, key := range bulkFolders {
			if err := os.RemoveAll(key); err != nil {
				log.Warnf("Unable to remove directory %s", key)
			}
		}
		log.Infof("Collection Duration: %s", time.Since(start))

		// Run cleanup if enabled
		if runCleanup {
			cleanupStart := time.Now()
			log.Info("Running cleanup...")

			// cwd is the collection dir here (Chdir(writeDir) above). Capture it before
			// stepping back out, so we can stage its contents into the backup dir.
			collectingDir, _ := os.Getwd() //nolint:errcheck

			// Clean the collection directory (current working directory)
			cleanupDirectory(log, ".", maxAgeDays, pruneBelowVer)

			// Also clean the backup directory if it exists
			// Need to go back to original directory first
			if err := os.Chdir(".."); err == nil {
				if _, err := os.Stat(backupDir); err == nil {
					cleanupDirectory(log, backupDir, maxAgeDays, pruneBelowVer)
				}
				// Stage freshly-collected (and now pruned) surveys into the backup dir
				// the reward calc reads — the Go replacement for getlogs.sh's
				// `rsync -r log_collecting/ log_backups`. Additive merge (dst-only files
				// are left intact); no separate uptime tracker / bash step needed.
				backupAbs := backupDir
				if !filepath.IsAbs(backupDir) {
					if wd, e := os.Getwd(); e == nil {
						backupAbs = filepath.Join(wd, backupDir)
					}
				}
				if collectingDir != "" {
					syncToBackup(log, collectingDir, backupAbs)
				}
			}

			log.Infof("Cleanup Duration: %s", time.Since(cleanupStart))
		}

		log.Infof("Total Process Duration: %s", time.Since(start))
	},
}

// parseHealthVersion reads a downloaded health.json and returns the visor's
// build_info.version. Empty string on any read/parse failure. This is the
// authoritative version for reward eligibility: it comes from the visor's own
// running binary (/health), not the uptime tracker's possibly-stale report.
func parseHealthVersion(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	var doc struct {
		BuildInfo struct {
			Version string `json:"version"`
		} `json:"build_info"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.BuildInfo.Version
}

// surveyUnchanged checks if the remote survey checksum matches the local file.
// Returns true if checksums match (survey unchanged), false otherwise.
// Falls back to false (re-download) on any error including 404.
func surveyUnchanged(ctx context.Context, log *logging.Logger, httpC *http.Client, pubkey string) bool {
	// Check if local file exists
	localPath := pubkey + "/node-info.json"
	localData, err := os.ReadFile(localPath) //nolint:gosec
	if err != nil || len(localData) == 0 {
		return false
	}

	// Compute local checksum
	localSum := sha256.Sum256(localData)
	localHex := hex.EncodeToString(localSum[:])

	// Fetch remote checksum
	target := visorURL(pubkey, "node-info/checksum")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		// 404 or any other error — visor doesn't support checksum endpoint, re-download
		return false
	}

	var result struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}

	if result.SHA256 == localHex {
		log.Debugf("Survey unchanged for visor %s (checksum match)", pubkey)
		return true
	}
	return false
}

// minVersionFallback is the reward-eligibility floor used when the GitHub releases
// API can't be reached. Keep it bumped to a recent known-good floor so the collector
// never runs without a minimum. Mirrors getlogs.sh's _minversion_fallback.
const minVersionFallback = "v1.3.53"

// minVersionFloor returns the reward-eligibility minimum version: the latest
// published skywire release whose published_at is at least 14 days old (per
// rewards/mainnet_rules.md — a released version becomes required 2 weeks after
// publication). Cached in $TMPDIR for 1h to respect GitHub's 60-req/hr
// unauthenticated rate limit; falls back to minVersionFallback on any failure.
// Go port of getlogs.sh's _compute_minversion so the collector enforces the floor
// without host-only bash. Uses the default (clearnet) HTTP client regardless of
// --proxy — GitHub is not a dmsg service.
func minVersionFloor(log *logging.Logger) string {
	cache := filepath.Join(os.TempDir(), "skywire_minversion.cache")
	if fi, err := os.Stat(cache); err == nil && time.Since(fi.ModTime()) < time.Hour {
		if b, err := os.ReadFile(cache); err == nil { //nolint:gosec
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	v := fetchMinVersionFromGitHub()
	if v == "" {
		log.Warnf("Could not compute reward min-version from GitHub releases; using fallback %s", minVersionFallback)
		return minVersionFallback
	}
	_ = os.WriteFile(cache, []byte(v+"\n"), 0644) //nolint:errcheck,gosec
	return v
}

func fetchMinVersionFromGitHub() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/skycoin/skywire/releases?per_page=100", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var releases []struct {
		TagName     string    `json:"tag_name"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return ""
	}
	cutoff := time.Now().Add(-14 * 24 * time.Hour)
	var best *version.Version
	var bestTag string
	for _, r := range releases {
		if r.Draft || r.Prerelease || r.PublishedAt.IsZero() || r.PublishedAt.After(cutoff) {
			continue
		}
		v, err := version.NewVersion(r.TagName)
		if err != nil {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestTag = r.TagName
		}
	}
	return bestTag
}

// visorURL builds the URL to fetch `path` from visor `pubkey`, per collector mode:
// through the SOCKS5 resolving proxy (http://<pk>.dmsg/<path>, resolved by the proxy)
// when --proxy is set, else directly over the built-in dmsg client
// (dmsg://<pk>:80/<path>).
func visorURL(pubkey, path string) string {
	if proxyAddr != "" {
		return "http://" + pubkey + ".dmsg/" + path
	}
	return fmt.Sprintf("dmsg://%s:80/%s", pubkey, path)
}

// socks5Transport builds an http.Transport that dials through the SOCKS5 proxy at
// addr, resolving hostnames REMOTELY (socks5h) so <pk>.dmsg names are resolved by the
// proxy (e.g. a running `skywire dmsg web`) rather than locally.
func socks5Transport(addr string) (*http.Transport, error) {
	d, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 dialer for %s does not support contexts", addr)
	}
	return &http.Transport{DialContext: cd.DialContext}, nil
}

// dmsgToProxyURL rewrites dmsg://<pk>:<port>/<path> to the http://<pk>.dmsg/<path>
// form the SOCKS5 resolving proxy understands (it maps <pk>.dmsg to dmsg port 80).
func dmsgToProxyURL(dmsgURL string) string {
	s := strings.TrimPrefix(dmsgURL, "dmsg://")
	host, rest := s, ""
	if i := strings.IndexByte(s, '/'); i >= 0 {
		host, rest = s[:i], s[i:]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return "http://" + host + ".dmsg" + rest
}

func download(ctx context.Context, log *logging.Logger, httpC http.Client, targetPath, fileName, pubkey string, maxSize int64) error {
	target := visorURL(pubkey, targetPath)
	//nolint:errcheck,gosec
	file, _ := os.Create(pubkey + "/" + fileName)
	//nolint:errcheck
	defer file.Close()

	if err := downloadDmsg(ctx, log, &httpC, file, target, maxSize); err != nil {
		log.WithError(err).Errorf("The %s for visor %s not available", fileName, pubkey)
		return err
	}
	return nil
}

// downloadDmsg downloads a file from the given URL into 'w'.
func downloadDmsg(ctx context.Context, log logrus.FieldLogger, httpC *http.Client, w io.Writer, urlStr string, maxSize int64) error {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		log.WithError(err).Fatal("Failed to formulate HTTP request.")
	}
	resp, err := httpC.Do(req) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return &httpError{Status: resp.StatusCode}
	}
	if maxSize > 0 && resp.ContentLength > maxSize*1024 {
		return fmt.Errorf("requested file size is more than allowed size: %d KB > %d KB", (resp.ContentLength / 1024), maxSize)
	}
	n, err := CancellableCopy(ctx, w, resp.Body, resp.ContentLength)
	if err != nil {
		return fmt.Errorf("download failed at %d/%dB: %w", n, resp.ContentLength, err)
	}
	return nil
}

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

// CancellableCopy will call the Reader and Writer interface multiple time, in order
// to copy by chunk (avoiding loading the whole file in memory).
func CancellableCopy(ctx context.Context, w io.Writer, body io.ReadCloser, length int64) (int64, error) {

	n, err := io.Copy(io.MultiWriter(w, &ProgressWriter{Total: length}), readerFunc(func(p []byte) (int, error) {

		// golang non-blocking channel: https://gobyexample.com/non-blocking-channel-operations
		select {

		// if context has been canceled
		case <-ctx.Done():
			// stop process and propagate "Download Canceled" error
			return 0, errors.New("Download Canceled")
		default:
			// otherwise just run default io.Reader implementation
			return body.Read(p)
		}
	}))
	return n, err
}

// ProgressWriter prints the progress of a download to stdout.
type ProgressWriter struct {
	// atomic requires 64-bit alignment for struct field access
	Current int64
	Total   int64
}

// Write implements io.Writer
func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)

	current := atomic.AddInt64(&pw.Current, int64(n))
	total := atomic.LoadInt64(&pw.Total)
	pc := fmt.Sprintf("%d%%", current*100/total)
	fmt.Printf("Downloading: %d/%dB (%s)", current, total, pc)
	if current != total {
		fmt.Print("\r")
	} else {
		fmt.Print("\n")
	}
	return n, nil
}

// getUptimes fetches the visor list from the uptime tracker. Prefers
// the dmsg form (http://<UT-PK>:<port>/...) over the passed dmsgHTTPC,
// since dmsgHTTPC is a pooled dmsghttp transport that already shares
// the caller's dmsg sessions. Falls back to plain HTTP iff the UT URL
// has no known dmsg twin in the deployment map (operator-overridden
// utAddr against a non-deployment UT).
//
// Pre-fix, this function spun up its own plain http.Client and never
// went over dmsg, even when a perfectly good dmsg.Client was already
// up in the same process.
func getUptimes(ctx context.Context, endpoint string, dmsgHTTPC *http.Client, log *logging.Logger) ([]VisorUptimeResponse, error) {
	var results []VisorUptimeResponse
	fetchURL := endpoint
	client := dmsgHTTPC
	switch {
	case strings.HasPrefix(endpoint, "dmsg://"):
		// utAddr is already a dmsg URL (the deployment's dmsg-only config, e.g. the
		// TPD-integrated uptime endpoint). --proxy reaches it as http://<pk>.dmsg/...;
		// otherwise the dmsghttp transport dials the dmsg:// URL directly.
		if proxyAddr != "" {
			fetchURL = dmsgToProxyURL(endpoint)
		}
	case clirpc.DmsgURLForHTTP(endpoint) != "":
		dmsgEquiv := clirpc.DmsgURLForHTTP(endpoint)
		if proxyAddr != "" {
			// Reach the UT through the SOCKS5 resolving proxy as http://<pk>.dmsg/...
			fetchURL = dmsgToProxyURL(dmsgEquiv)
		} else {
			fetchURL = dmsgEquiv
		}
	case proxyAddr == "":
		log.Debugf("No dmsg twin for %s; falling back to plain HTTP for UT fetch", endpoint)
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		log.Error("Error while building UT request. Error: ", err)
		return results, errors.New("Cannot get Uptime data")
	}
	response, err := client.Do(req)
	if err != nil {
		log.Error("Error while fetching data from uptime service. Error: ", err)
		return results, errors.New("Cannot get Uptime data")
	}
	defer response.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(response.Body)
	if err != nil {
		log.Error("Error while reading data from uptime service. Error: ", err)
		return results, errors.New("Cannot get Uptime data")
	}
	err = json.Unmarshal(body, &results)
	if err != nil {
		log.Errorf("Error while unmarshalling data from uptime service.\nBody:\n%v\nError:\n%v ", string(body), err)
		return results, errors.New("Cannot get Uptime data")
	}
	log.Debugf("Successfully called uptime service via %s and received %d entries", fetchURL, len(results))
	return results, nil
}

func getAllDMSGServers(cmdFlags *pflag.FlagSet) []dmsgServer {
	var results []dmsgServer

	body, err := clirpc.FetchServiceURL(cmdFlags, dmsgDisc+"/dmsg-discovery/all_servers")
	if err != nil {
		return results
	}
	err = json.Unmarshal(body, &results)
	if err != nil {
		return results
	}
	return results
}

type dmsgServer struct {
	PK cipher.PubKey `json:"static"`
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

type httpError struct {
	Status int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("http error: %d", e.Status)
}

// syncToBackup recursively copies every file under src into dst (creating dirs and
// overwriting existing files), mirroring `rsync -r src/ dst`: an additive merge that
// leaves dst-only files intact. This is the Go replacement for getlogs.sh's
// `rsync -r log_collecting/ log_backups`, staging freshly-collected surveys into the
// directory the reward calc reads — so `skywire cli log --cleanup` fully replaces the
// host's _getlogs + _cleanup bash without a separate sync step.
func syncToBackup(log *logging.Logger, src, dst string) {
	entries, err := os.ReadDir(src)
	if err != nil {
		log.WithError(err).Warnf("sync-to-backup: cannot read %s", src)
		return
	}
	if err := os.MkdirAll(dst, 0750); err != nil {
		log.WithError(err).Warnf("sync-to-backup: cannot create %s", dst)
		return
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			syncToBackup(log, s, d)
			continue
		}
		if err := copyFile(s, d); err != nil {
			log.WithError(err).Warnf("sync-to-backup: copy %s -> %s", s, d)
		}
	}
}

// copyFile copies src to dst, overwriting dst if it exists.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer in.Close()           //nolint:errcheck
	out, err := os.Create(dst) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close() //nolint:errcheck
		return err
	}
	return out.Close()
}

// cleanupDirectory performs all cleanup operations on a directory.
// If minVersion is non-empty, surveys (node-info.json) whose skywire_version
// is below it are also removed, regardless of age.
func cleanupDirectory(log *logging.Logger, dir string, maxAgeDays int, minVersion string) {
	log.Infof("Cleaning directory: %s", dir)

	// Get list of subdirectories (each is a visor public key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.WithError(err).Warnf("Failed to read directory %s", dir)
		return
	}

	var minVer *version.Version
	if minVersion != "" {
		mv, err := version.NewVersion(minVersion)
		if err != nil {
			log.WithError(err).Warnf("Invalid --prune-below-version %q; skipping version prune", minVersion)
		} else {
			minVer = mv
		}
	}

	var removedFiles, removedDirs int
	maxAge := time.Duration(maxAgeDays) * 24 * time.Hour

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		visorDir := dir + "/" + entry.Name()

		// Process files in this visor directory
		files, err := os.ReadDir(visorDir)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			filePath := visorDir + "/" + file.Name()
			info, err := file.Info()
			if err != nil {
				continue
			}

			shouldRemove := false
			reason := ""

			// Check file age for JSON files
			if strings.HasSuffix(file.Name(), ".json") {
				if time.Since(info.ModTime()) > maxAge {
					shouldRemove = true
					reason = "old file"
				} else if info.Size() == 0 {
					shouldRemove = true
					reason = "empty file"
				} else if !isValidJSON(filePath) {
					shouldRemove = true
					reason = "invalid JSON"
				}
			}

			// Min-version prune: surveys for visors below the current min-version
			// are not eligible for rewards, so keeping them wastes disk and
			// confuses downstream calculations.
			if !shouldRemove && minVer != nil && file.Name() == "node-info.json" {
				if surveyBelowVersion(filePath, minVer) {
					shouldRemove = true
					reason = "skywire_version below " + minVersion
				}
			}

			// Check for HTTP 404 error files (typically 18-19 bytes)
			if info.Size() == 18 || info.Size() == 19 {
				shouldRemove = true
				reason = "HTTP 404 error response"
			}

			// Check for empty files
			if info.Size() == 0 {
				shouldRemove = true
				reason = "empty file"
			}

			// Check CSV files for error content
			if strings.HasSuffix(file.Name(), ".csv") {
				if containsErrorContent(filePath) {
					shouldRemove = true
					reason = "contains error content"
				} else {
					// Strip CSV header if present
					stripCSVHeader(filePath, log)
				}
			}

			if shouldRemove {
				if err := os.Remove(filePath); err == nil {
					log.Debugf("Removed %s (%s)", filePath, reason)
					removedFiles++
				}
			}
		}

		// Remove empty directories
		remaining, _ := os.ReadDir(visorDir) //nolint:errcheck
		if len(remaining) == 0 {
			if err := os.Remove(visorDir); err == nil {
				log.Debugf("Removed empty directory: %s", visorDir)
				removedDirs++
			}
		}
	}

	log.Infof("Cleanup complete for %s: removed %d files and %d empty directories", dir, removedFiles, removedDirs)
}

// isValidJSON checks if a file contains valid JSON
func isValidJSON(filePath string) bool {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return false
	}
	var js json.RawMessage
	return json.Unmarshal(data, &js) == nil
}

// surveyBelowVersion reports whether the survey at filePath has a
// skywire_version that parses cleanly and is strictly less than minVer.
// Returns false on any parse failure so we don't delete on bad data.
func surveyBelowVersion(filePath string, minVer *version.Version) bool {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return false
	}
	var s struct {
		SkywireVersion string `json:"skywire_version"`
	}
	if err := json.Unmarshal(data, &s); err != nil || s.SkywireVersion == "" {
		return false
	}
	// Strip any "-something" or "+something" build suffix
	v := s.SkywireVersion
	if idx := strings.IndexAny(v, "-+ "); idx > 0 {
		v = v[:idx]
	}
	parsed, err := version.NewVersion(v)
	if err != nil {
		return false
	}
	return parsed.LessThan(minVer)
}

// containsErrorContent checks if a file contains HTTP error messages
func containsErrorContent(filePath string) bool {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "404 page not found") ||
		strings.Contains(content, "Not Found")
}

// stripCSVHeader removes the header line if it matches the old transport log format
func stripCSVHeader(filePath string, log *logging.Logger) {
	data, err := os.ReadFile(filePath) //nolint:gosec
	if err != nil {
		return
	}

	content := string(data)
	if strings.HasPrefix(content, "tp_id,recv,sent,time_stamp") {
		// Find the first newline and remove everything before it
		idx := strings.Index(content, "\n")
		if idx != -1 {
			newContent := content[idx+1:]
			if err := os.WriteFile(filePath, []byte(newContent), 0600); err == nil { //nolint:gosec
				log.Debugf("Stripped CSV header from %s", filePath)
			}
		}
	}
}
