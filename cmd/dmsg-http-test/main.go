// Package main provides a diagnostic tool for testing HTTP-over-DMSG accessibility
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

var (
	targetAddr   string
	dmsgDisc     string
	minSessions  int
	testInterval time.Duration
	timeout      time.Duration
	sk           cipher.SecKey
	verbose      bool
)

func init() {
	flag.StringVar(&targetAddr, "target", "", "Target DMSG HTTP address (e.g., dmsg://<pk>:<port>/health)")
	flag.StringVar(&dmsgDisc, "dmsg-disc", "http://dmsgd.skywire.skycoin.com", "DMSG discovery URL")
	flag.IntVar(&minSessions, "min-sessions", 1, "Minimum DMSG sessions to maintain")
	flag.DurationVar(&testInterval, "interval", 30*time.Second, "Interval between tests")
	flag.DurationVar(&timeout, "timeout", 30*time.Second, "HTTP request timeout")
	flag.Var(&sk, "sk", "Secret key (random if not specified)")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
}

func main() {
	flag.Parse()

	if targetAddr == "" {
		fmt.Println("Usage: dmsg-http-test -target dmsg://<pk>:<port>[/path]")
		fmt.Println("\nThis tool monitors HTTP-over-DMSG accessibility and logs diagnostics.")
		fmt.Println("\nExample:")
		fmt.Println("  dmsg-http-test -target dmsg://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f:80/health -interval 30s")
		flag.PrintDefaults()
		os.Exit(1)
	}

	log := logging.MustGetLogger("dmsg-http-test")
	if verbose {
		lvl, _ := logging.LevelFromString("debug")
		logging.SetLevel(lvl)
	}

	// Generate key pair if not provided
	var pk cipher.PubKey
	if sk.Null() {
		pk, sk = cipher.GenerateKeyPair()
	} else {
		var err error
		pk, err = sk.PubKey()
		if err != nil {
			log.WithError(err).Fatal("Invalid secret key")
		}
	}
	log.WithField("pk", pk).Info("Using public key")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Info("Shutting down...")
		cancel()
	}()

	// Create DMSG client
	conf := dmsg.DefaultConfig()
	conf.MinSessions = minSessions
	dmsgC := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), conf)
	defer dmsgC.Close() //nolint:errcheck

	go dmsgC.Serve(ctx)

	log.Info("Waiting for DMSG client to be ready...")
	select {
	case <-ctx.Done():
		return
	case <-dmsgC.Ready():
		log.Info("DMSG client ready")
	}

	// Create HTTP client over DMSG
	httpTransport := dmsghttp.MakeHTTPTransport(ctx, dmsgC)
	httpClient := &http.Client{
		Transport: httpTransport,
		Timeout:   timeout,
	}

	// Statistics
	var (
		totalRequests   int
		successCount    int
		failCount       int
		consecutiveFail int
		lastSuccess     time.Time
		lastFail        time.Time
		lastError       string
	)

	log.WithFields(logrus.Fields{
		"target":   targetAddr,
		"interval": testInterval,
		"timeout":  timeout,
	}).Info("Starting HTTP-over-DMSG monitoring")

	ticker := time.NewTicker(testInterval)
	defer ticker.Stop()

	// Run first test immediately
	testAndLog := func() {
		totalRequests++
		sessionCount := dmsgC.SessionCount()
		sessions := dmsgC.AllSessions()

		// Log session details
		sessionInfo := make([]string, 0, len(sessions))
		for _, s := range sessions {
			sessionInfo = append(sessionInfo, fmt.Sprintf("%s@%s", s.RemotePK().String()[:8], s.RemoteTCPAddr()))
		}

		startTime := time.Now()
		resp, err := httpClient.Get(targetAddr)
		elapsed := time.Since(startTime)

		fields := logrus.Fields{
			"request_num":      totalRequests,
			"session_count":    sessionCount,
			"sessions":         sessionInfo,
			"elapsed":          elapsed.Round(time.Millisecond),
			"success_count":    successCount,
			"fail_count":       failCount,
			"consecutive_fail": consecutiveFail,
		}

		if err != nil {
			failCount++
			consecutiveFail++
			lastFail = time.Now()
			lastError = err.Error()

			fields["error"] = err.Error()
			if !lastSuccess.IsZero() {
				fields["time_since_last_success"] = time.Since(lastSuccess).Round(time.Second)
			}

			log.WithFields(fields).Error("HTTP request FAILED")

			// Additional diagnostics on failure
			if consecutiveFail > 0 && consecutiveFail%5 == 0 {
				log.WithFields(logrus.Fields{
					"consecutive_failures": consecutiveFail,
					"last_error":           lastError,
					"session_count":        sessionCount,
					"total_requests":       totalRequests,
					"success_rate":         fmt.Sprintf("%.1f%%", float64(successCount)/float64(totalRequests)*100),
				}).Warn("Multiple consecutive failures - possible stability issue")
			}
		} else {
			successCount++
			consecutiveFail = 0
			lastSuccess = time.Now()

			// Read and discard body
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close() //nolint:errcheck

			fields["status"] = resp.StatusCode
			fields["body_size"] = len(body)

			if verbose {
				log.WithFields(fields).Debug("HTTP request OK")
			} else if totalRequests%10 == 0 {
				// Log every 10th success to show it's still running
				fields["success_rate"] = fmt.Sprintf("%.1f%%", float64(successCount)/float64(totalRequests)*100)
				log.WithFields(fields).Info("HTTP request OK (periodic status)")
			}

			// Log recovery after failures
			if failCount > 0 && lastFail.After(lastSuccess.Add(-testInterval*2)) {
				log.WithFields(logrus.Fields{
					"recovered_after": time.Since(lastFail).Round(time.Second),
					"failures_before": failCount,
				}).Info("Recovered from previous failures")
			}
		}
	}

	testAndLog()

	for {
		select {
		case <-ctx.Done():
			log.WithFields(logrus.Fields{
				"total_requests": totalRequests,
				"success_count":  successCount,
				"fail_count":     failCount,
				"success_rate":   fmt.Sprintf("%.1f%%", float64(successCount)/float64(totalRequests)*100),
			}).Info("Final statistics")
			return
		case <-ticker.C:
			testAndLog()
		}
	}
}
