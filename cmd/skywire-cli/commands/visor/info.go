// Package clivisor cmd/skywire-cli/commands/visor/info.go
package clivisor

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var path string
var pkg bool

func init() {
	RootCmd.AddCommand(pkCmd)
	pkCmd.Flags().StringVarP(&path, "input", "i", "", "path of input config file.")
	pkCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from "+fmt.Sprintf("%v", visorconfig.PackageConfig()))
	RootCmd.AddCommand(summaryCmd)
	RootCmd.AddCommand(readyCmd)
	RootCmd.AddCommand(buildInfoCmd)
	RootCmd.AddCommand(portsCmd)
	RootCmd.AddCommand(dmsgServersCmd)
	RootCmd.AddCommand(runtimeLogsCmd)
	RootCmd.AddCommand(runtimeStatsCmd)
}

var pkCmd = &cobra.Command{
	Use:   "pk",
	Short: "Public key of the visor",
	Long:  "\n  Public key of the visor",
	Run: func(cmd *cobra.Command, _ []string) {
		if pkg {
			path = visorconfig.SkywireConfig()
		}
		var outputPK string
		if path != "" {
			conf, err := visorconfig.ReadFile(path)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Failed to read config: %v", err))
			}
			outputPK = conf.PK.Hex()
		} else {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				os.Exit(1)
			}
			overview, err := rpcClient.Overview()
			if err != nil {
				internal.PrintFatalRPCError(cmd.Flags(), err)
			}
			outputPK = overview.PubKey.Hex() + "\n"
		}
		internal.PrintOutput(cmd.Flags(), strings.TrimSuffix(outputPK, "\n"), outputPK)
	},
}

var summaryCmd = &cobra.Command{
	Use:   "info",
	Short: "Summary of visor info",
	Long:  "Summary of visor info",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		summary, err := rpcClient.Summary()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		// Build list of connected DMSG servers with latencies
		dmsgServersStr := ""
		for i, server := range summary.DMSGServers {
			if i > 0 {
				dmsgServersStr += "\n              "
			}
			latStr := ""
			if server.Latency > 0 {
				latStr = fmt.Sprintf(" (%.1fms)", float64(server.Latency.Milliseconds()))
			}
			dmsgServersStr += server.PK.String() + latStr
		}
		if dmsgServersStr == "" {
			dmsgServersStr = "(none)"
		}

		// Build geo location string
		geoStr := ""
		if summary.Overview.CityName != "" || summary.Overview.RegionName != "" || summary.Overview.CountryCode != "" {
			parts := []string{}
			if summary.Overview.CityName != "" {
				parts = append(parts, summary.Overview.CityName)
			}
			if summary.Overview.RegionName != "" {
				parts = append(parts, summary.Overview.RegionName)
			}
			if summary.Overview.CountryCode != "" {
				parts = append(parts, summary.Overview.CountryCode)
			}
			geoStr = strings.Join(parts, ", ")
		}

		msg := fmt.Sprintf(".:: Visor Summary ::.\nPublic key: %q\nSymmetric NAT: %t\nLocal IP: %s\nPublic IP: %s\n",
			summary.Overview.PubKey, summary.Overview.IsSymmetricNAT, summary.Overview.LocalIP, summary.Overview.PublicIP)

		if geoStr != "" {
			msg += fmt.Sprintf("Location: %s\n", geoStr)
		}

		msg += fmt.Sprintf("DMSG Servers (%d connected):\n              %s\n", len(summary.DMSGServers), dmsgServersStr)
		msg += fmt.Sprintf("DMSG Latency: %s\n", summary.DmsgStats.RoundTrip)

		// Transport summary by type
		tpCounts := make(map[string]int)
		for _, tp := range summary.Overview.Transports {
			tpCounts[string(tp.Type)]++
		}
		tpTotal := len(summary.Overview.Transports)
		tpStr := fmt.Sprintf("%d", tpTotal)
		if tpTotal > 0 {
			tpStr += " ("
			first := true
			for tpType, count := range tpCounts {
				if !first {
					tpStr += ", "
				}
				tpStr += fmt.Sprintf("%s: %d", strings.ToUpper(tpType), count)
				first = false
			}
			tpStr += ")"
		}
		msg += fmt.Sprintf("Transports: %s\n", tpStr)

		// AR registration: best-effort. A failure here means we'll skip the
		// AR section in human output and emit nothing for it in JSON; the
		// rest of the summary is still useful.
		var arSelf *visor.ARSelfRegistration
		if reg, err := rpcClient.ARSelfInfo(); err == nil {
			arSelf = reg
			if reg != nil && len(reg.Entries) > 0 {
				msg += "AR Registration:\n"
				for _, e := range reg.Entries {
					addr := formatARAddr(e)
					msg += fmt.Sprintf("  %-6s %s\n", strings.ToUpper(e.Type), addr)
				}
			} else {
				msg += "AR Registration: (none)\n"
			}
		}

		msg += fmt.Sprintf("Visor Version: %s\nConfig Version: %s\nUptime Tracker: %s\nTime Online: %f seconds\nBuild Tag: %s\n",
			summary.Overview.BuildInfo.Version, summary.ConfigVersion, summary.Health.ServicesHealth, summary.Uptime, summary.BuildTag)

		// Last-24h rolling uptime per local-tier bitmap. Same source as
		// the hvui's per-visor Uptime tab and `/stats/uptime` on the
		// logserver — all read pkg/visor/stats — so the bar here is
		// what the integrated tracker recorded for THIS visor, not the
		// network-wide TPD aggregate. Best-effort: if the store isn't
		// initialized (rare; e.g. partial startup) we skip the section.
		if uptimeBars, uptimePcts, ok := localUptime24h(rpcClient); ok && len(uptimeBars) > 0 {
			msg += "Local uptime (last 24h, hour blocks):\n"
			for _, name := range []string{"process", "dmsg", "skynet"} {
				bar, hasBar := uptimeBars[name]
				if !hasBar {
					continue
				}
				pct := uptimePcts[name]
				msg += fmt.Sprintf("  %-7s  %s  %5.1f%%\n", name, bar, pct)
			}
		}

		outputJSON := struct {
			PublicKey      string                    `json:"public_key"`
			IsSymmetricNAT bool                      `json:"symmetric_nat"`
			LocalIP        string                    `json:"local_ip"`
			PublicIP       string                    `json:"public_ip"`
			CountryCode    string                    `json:"country_code,omitempty"`
			RegionName     string                    `json:"region_name,omitempty"`
			CityName       string                    `json:"city_name,omitempty"`
			DMSGServers    []visor.DMSGServerInfo    `json:"dmsg_servers"`
			DmsgLatency    string                    `json:"dmsg_latency"`
			VisorVersion   string                    `json:"visor_version"`
			ConfigVersion  string                    `json:"config_version"`
			UptimeTracker  string                    `json:"uptime_tracker"`
			TimeOnline     float64                   `json:"time_online"`
			BuildTag       string                    `json:"build_tag"`
			ARRegistration *visor.ARSelfRegistration `json:"ar_registration,omitempty"`
		}{
			PublicKey:      summary.Overview.PubKey.String(),
			IsSymmetricNAT: summary.Overview.IsSymmetricNAT,
			LocalIP:        summary.Overview.LocalIP,
			PublicIP:       summary.Overview.PublicIP,
			CountryCode:    summary.Overview.CountryCode,
			RegionName:     summary.Overview.RegionName,
			CityName:       summary.Overview.CityName,
			DMSGServers:    summary.DMSGServers,
			DmsgLatency:    summary.DmsgStats.RoundTrip.String(),
			VisorVersion:   summary.Overview.BuildInfo.Version,
			ConfigVersion:  summary.ConfigVersion,
			UptimeTracker:  summary.Health.ServicesHealth,
			TimeOnline:     summary.Uptime,
			BuildTag:       summary.BuildTag,
			ARRegistration: arSelf,
		}
		internal.PrintOutput(cmd.Flags(), outputJSON, msg)
	},
}

var readyCmd = &cobra.Command{
	Use:   "ready",
	Short: "Wait for visor startup to complete",
	Long:  "Polls the visor and exits once startup is complete.\nUseful in scripts and systemd ExecStartPost.",
	Run: func(cmd *cobra.Command, _ []string) {
		timeout := 3 * time.Minute
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			rpcClient, err := clirpc.Client(cmd.Flags())
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			if rpcClient.IsStartupComplete() {
				internal.PrintOutput(cmd.Flags(), "ready\n", "ready\n")
				return
			}
			time.Sleep(2 * time.Second)
		}
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor startup not complete after %v", timeout))
	},
}

var buildInfoCmd = &cobra.Command{
	Use:   "ver",
	Short: "Version and build info",
	Long:  "\n  Version and build info",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		overview, err := rpcClient.Overview()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		buildInfo := overview.BuildInfo
		msg := fmt.Sprintf("Version %q built on %q against commit %q\n", buildInfo.Version, buildInfo.Date, buildInfo.Commit)
		internal.PrintOutput(cmd.Flags(), buildInfo, msg)
	},
}

var portsCmd = &cobra.Command{
	Use:   "ports",
	Short: "List of Ports",
	Long:  "\n  List of all ports used by visor services and apps",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		ports, err := rpcClient.Ports()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		msg := "+-------------------------------------------+\n"
		msg += fmt.Sprintf("| %-21s | %-7s | %-7s |\n", "App/Service", "Type", "Port")
		msg += "|-------------------------------------------|\n"

		portsName := make([]string, 0, len(ports))
		for portName := range ports {
			portsName = append(portsName, portName)
		}
		sort.Strings(portsName)

		for _, portName := range portsName {
			msg += fmt.Sprintf("| %-21s | %-7s | %7s |\n", portName, ports[portName].Type, ports[portName].Port)
		}

		msg += "|===========================================|\n"
		msg += "| SKYNET: connection between apps and visor |\n"
		msg += "| DMSG: connection by dmsg service          |\n"
		msg += "+-------------------------------------------+\n"
		internal.PrintOutput(cmd.Flags(), ports, msg)
	},
}

var dmsgServersCmd = &cobra.Command{
	Use:   "dmsg-servers",
	Short: "List connected DMSG servers with latencies",
	Long:  "List of connected DMSG servers sorted by latency (lowest first)",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		servers, err := rpcClient.DMSGServers()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		if len(servers) == 0 {
			fmt.Println("No DMSG servers connected")
			return
		}

		msg := "+--------------------------------------------------------------------+-------------+\n"
		msg += fmt.Sprintf("| %-66s | %11s |\n", "Server Public Key", "Latency")
		msg += "|--------------------------------------------------------------------+-------------|\n"

		for _, server := range servers {
			latStr := "-"
			if server.Latency > 0 {
				latStr = fmt.Sprintf("%.1fms", float64(server.Latency.Milliseconds()))
			}
			msg += fmt.Sprintf("| %-66s | %11s |\n", server.PK.String(), latStr)
		}

		msg += "+--------------------------------------------------------------------+-------------+\n"
		msg += "Note: Latency is measured via self-ping through each server.\n"
		msg += "Servers with '-' latency have not been measured yet (wait ~5s after startup).\n"
		internal.PrintOutput(cmd.Flags(), servers, msg)
	},
}

var runtimeLogsCmd = &cobra.Command{
	Use:   "log",
	Short: "Visor runtime logs",
	Long:  "\n  Returns runtime logs from the visor",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		logs, err := rpcClient.RuntimeLogs()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), logs, logs)
	},
}

var runtimeStatsCmd = &cobra.Command{
	Use:   "go",
	Short: "Go runtime statistics",
	Long:  "Returns Go runtime statistics including goroutine count and memory usage",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			os.Exit(1)
		}
		stats, err := rpcClient.RuntimeStats()
		if err != nil {
			internal.PrintFatalRPCError(cmd.Flags(), err)
		}

		msg := ".:: Go Runtime Stats ::.\n"
		msg += fmt.Sprintf("Goroutines:  %d\n", stats.NumGoroutine)
		msg += fmt.Sprintf("CPUs:        %d\n", stats.NumCPU)
		msg += fmt.Sprintf("GOMAXPROCS:  %d\n", stats.GOMAXPROCS)
		msg += fmt.Sprintf("Go Version:  %s\n", stats.GoVersion)
		msg += "\n.:: Memory Stats ::.\n"
		msg += fmt.Sprintf("Alloc:       %.2f MB\n", float64(stats.MemAlloc)/1024/1024)
		msg += fmt.Sprintf("TotalAlloc:  %.2f MB\n", float64(stats.MemTotalAlloc)/1024/1024)
		msg += fmt.Sprintf("Sys:         %.2f MB\n", float64(stats.MemSys)/1024/1024)
		msg += fmt.Sprintf("HeapAlloc:   %.2f MB\n", float64(stats.MemHeapAlloc)/1024/1024)
		msg += fmt.Sprintf("HeapSys:     %.2f MB\n", float64(stats.MemHeapSys)/1024/1024)
		msg += fmt.Sprintf("NumGC:       %d\n", stats.NumGC)

		internal.PrintOutput(cmd.Flags(), stats, msg)
	},
}

// localUptime24h fetches the visor's local tier bitmaps and folds
// the trailing 24 wall-clock hours into one row of 24 hourly density
// blocks per tier. Returns (bars, pcts, true) on success or (nil,
// nil, false) when the stats store isn't available — same shape and
// shading as `cli ut tpd graph` so an operator can eyeball the local
// view next to TPD's network-wide one without doing mental conversion.
func localUptime24h(rpc visor.API) (map[string]string, map[string]float64, bool) {
	until := time.Now().UTC()
	since := until.Add(-24 * time.Hour)
	resp, err := rpc.LocalUptimeStats(visor.LocalUptimeArgs{Since: since, Until: until})
	if err != nil || resp == nil || len(resp.Tiers) == 0 {
		return nil, nil, false
	}

	const slotsPerHour = 12 // 5-minute slots
	const totalHours = 24
	bars := make(map[string]string, len(resp.Tiers))
	pcts := make(map[string]float64, len(resp.Tiers))

	// Build a flat slice of 288 slot characters covering the rolling
	// window: yesterday's slots from `now`-onwards onward through
	// today's slots up to `now`. Crossing the UTC midnight boundary
	// is the only fiddly part — same composition logic the CLI's
	// rolling-window mode uses, just inlined since pkg-level helpers
	// aren't exported.
	for tier, days := range resp.Tiers {
		// Pull yesterday + today (if present) — those are the only
		// dates a 24h window can touch.
		todayKey := until.UTC().Format("2006-01-02")
		yesterdayKey := until.UTC().Add(-24 * time.Hour).Format("2006-01-02")
		todayAscii := days[todayKey]
		yestAscii := days[yesterdayKey]

		nowSlot := until.UTC().Hour()*slotsPerHour + until.UTC().Minute()/5
		// Window starts (24h*12 = 288) slots before nowSlot. Negative
		// indices wrap into yesterday's slot space.
		var slots [288]byte
		for i := 0; i < 288; i++ {
			abs := nowSlot - 288 + i
			var src string
			if abs < 0 {
				src = yestAscii
				abs += 288 // map -1 → yesterday's slot 287, etc.
			} else {
				src = todayAscii
			}
			if abs >= 0 && abs < len(src) && src[abs] == '.' {
				slots[i] = '.'
			} else {
				slots[i] = ' '
			}
		}

		// Roll up into 24 hourly density blocks.
		var b strings.Builder
		var onlineSlots int
		var totalSlotsKnown int
		for h := 0; h < totalHours; h++ {
			count := 0
			for s := 0; s < slotsPerHour; s++ {
				idx := h*slotsPerHour + s
				if slots[idx] == '.' {
					count++
				}
			}
			onlineSlots += count
			totalSlotsKnown += slotsPerHour
			b.WriteString(shadeForCount(count))
		}
		bars[tier] = b.String()
		if totalSlotsKnown > 0 {
			pcts[tier] = 100 * float64(onlineSlots) / float64(totalSlotsKnown)
		}
	}
	return bars, pcts, true
}

// shadeForCount maps an online-slot count (0–12) per hour to one of
// five density characters. Same thresholds + glyphs as
// cliuptime.shadeForCount so the visor-info bar reads identically
// next to a `cli ut tpd graph` output.
func shadeForCount(count int) string {
	switch {
	case count == 0:
		return " "
	case count <= 3:
		return "░"
	case count <= 6:
		return "▒"
	case count <= 9:
		return "▓"
	default:
		return "█"
	}
}

// formatARAddr renders one AR self-registration entry.
//
// SUDPH and STCPR populate the two address fields differently:
//   - STCPR: RemoteAddr is just the IP, Port is the listen port —
//     join them with ':' to get a dialable host:port.
//   - SUDPH: RemoteAddr is the NAT-mapped public ip:port observed
//     by STUN, Port is the local listen port. They typically agree
//     when the NAT preserves the port; they diverge under symmetric
//     or port-rewriting NAT and that divergence is worth seeing.
//
// The previous formatter just appended ":port" unconditionally and
// produced "ip:port:port" for SUDPH whenever RemoteAddr already
// carried a port.
func formatARAddr(e visor.ARSelfEntry) string {
	if e.RemoteAddr == "" && e.Port == "" {
		return "(unknown)"
	}
	if e.RemoteAddr == "" {
		return e.Port
	}
	host, mapped, splitErr := net.SplitHostPort(e.RemoteAddr)
	if splitErr != nil {
		// RemoteAddr has no port; fall back to the legacy join.
		if e.Port != "" {
			return e.RemoteAddr + ":" + e.Port
		}
		return e.RemoteAddr
	}
	if e.Port == "" || e.Port == mapped {
		return e.RemoteAddr
	}
	// NAT-mapped port differs from the visor's listen port — surface both.
	return fmt.Sprintf("%s (listen :%s)", net.JoinHostPort(host, mapped), e.Port)
}
