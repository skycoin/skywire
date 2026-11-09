// Package clivisor cmd/skywire-cli/commands/visor/alerts.go c4-vis-cli
package clivisor

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
)

func init() {
	RootCmd.AddCommand(alertsCmd)
	alertsCmd.AddCommand(alertsSetCmd, alertsTestCmd)
	f := alertsSetCmd.Flags()
	f.StringSliceVar(&alEmail, "email", nil, "email address(es) to alert, comma separated (empty clears)")
	f.DurationVar(&alDelay, "offline-after", 0, "how long a visor must stay offline before an alert, 1m to 1h (default 5m)")
	f.StringVar(&alWebhook, "webhook", "", "URL to POST a JSON alert to (empty clears)")
	f.StringSliceVar(&alWatch, "watch", nil, "extra visor public keys to watch by their dmsg discovery entry (empty clears)")
	f.StringVar(&alFrom, "from", "", "sender address (default skywire-alerts@<hostname>, or the relay login)")
	f.StringVar(&alSMTPHost, "smtp-host", "", "SMTP relay host; without one, mail goes to the recipient's MX on port 25")
	f.IntVar(&alSMTPPort, "smtp-port", 0, "SMTP relay port (default 587 with --smtp-starttls, else 25; 465 is implicit TLS)")
	f.StringVar(&alSMTPUser, "smtp-user", "", "SMTP relay username")
	f.StringVar(&alSMTPPass, "smtp-pass", "", "SMTP relay password (kept when not given)")
	f.BoolVar(&alSMTPTLS, "smtp-starttls", false, "require STARTTLS on the relay")
	f.BoolVar(&alNoSMTP, "no-smtp", false, "remove the relay and deliver directly")
	f.Float64Var(&alDiskWarn, "disk-warn", 0, "disk use percent that raises a storage warning (default 90)")
}

var (
	alEmail    []string
	alDelay    time.Duration
	alWebhook  string
	alWatch    []string
	alFrom     string
	alSMTPHost string
	alSMTPPort int
	alSMTPUser string
	alSMTPPass string
	alSMTPTLS  bool
	alNoSMTP   bool
	alDiskWarn float64
)

var alertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Show the offline alerts this visor sends",
	Long: `Show the offline alerts this visor sends.

The visor mails, or posts to a webhook, when a watched visor has been
offline for the offline delay, and once more when it is back. A visor
that returns within the delay sends nothing. Watched visors are those its
hypervisor manages, remembered offline ones included, plus the --watch
list, which is checked by its dmsg discovery entry.

When every watched visor behind one public IP goes down together, one
site alert replaces the per-board alerts. For a single board, an online
visor on the same LAN checks whether the board still answers there. A
recovery says whether the board rebooted during the outage (power loss
or crash) or stayed up (network outage). A storage warning is sent while
a board is still online when its root filesystem turns read-only, the
kernel logs storage I/O errors, or the disk passes --disk-warn.

Without a relay, mail goes straight to each recipient domain's MX on
port 25. Many home connections block port 25 or are refused by large
mail providers; set a relay with 'alerts set --smtp-host' then.`,
	Run: func(cmd *cobra.Command, _ []string) {
		st := alertsStatus(cmd)
		internal.PrintOutput(cmd.Flags(), st, formatAlerts(st))
	},
}

func alertsStatus(cmd *cobra.Command) *alerts.Status {
	rpcClient, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	st, err := rpcClient.AlertsStatus()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return st
}

func formatAlerts(st *alerts.Status) string {
	var b strings.Builder
	s := st.Settings
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	fmt.Fprintf(&b, "enabled:        %v\n", st.Enabled)
	fmt.Fprintf(&b, "email:          %s\n", or(strings.Join(s.EmailTo, ", "), "-"))
	fmt.Fprintf(&b, "offline after:  %s\n", st.OfflineAfter)
	fmt.Fprintf(&b, "disk warn:      %.0f%%\n", s.DiskWarn())
	fmt.Fprintf(&b, "webhook:        %s\n", or(s.Webhook, "-"))
	fmt.Fprintf(&b, "from:           %s\n", or(s.From, "(default)"))
	if s.SMTP != nil {
		pw := "not set"
		if s.SMTP.PasswordSet {
			pw = "set"
		}
		fmt.Fprintf(&b, "relay:          %s port %d user %s starttls %v password %s\n",
			s.SMTP.Host, s.SMTP.Port, or(s.SMTP.Username, "-"), s.SMTP.StartTLS, pw)
	} else {
		b.WriteString("relay:          none, direct to MX\n")
	}
	if !st.LastSent.IsZero() {
		fmt.Fprintf(&b, "last sent:      %s\n", st.LastSent.UTC().Format(time.RFC3339))
	}
	if st.Pending > 0 {
		fmt.Fprintf(&b, "pending:        %d\n", st.Pending)
	}
	for _, r := range st.LastResults {
		if r.Err != "" {
			fmt.Fprintf(&b, "last error:     %s %s: %s\n", r.Channel, r.Target, r.Err)
		}
	}
	if len(st.Watched) == 0 {
		b.WriteString("\nno visors watched yet\n")
		return b.String()
	}
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PK\tHOSTNAME\tSOURCE\tSTATE\tLAST SEEN\tSTORAGE") //nolint:errcheck
	for _, w := range st.Watched {
		state := "online"
		switch {
		case w.Online && w.Site != "":
			state = "online, site " + w.Site + " recovering"
		case w.Online:
		case w.State == alerts.StateReconnecting && w.LinkAlerted:
			state = "reconnecting, alerted"
		case w.State == alerts.StateReconnecting, w.State == alerts.StateRestarting:
			state = w.State
		case w.Site != "":
			state = "offline, site " + w.Site + " alerted"
		case w.Alerted:
			state = "offline, alerted"
		default:
			state = "offline"
		}
		seen := "-"
		if !w.LastSeen.IsZero() {
			seen = w.LastSeen.UTC().Format(time.RFC3339)
		}
		storage := "-"
		if w.Storage != nil {
			storage = or(strings.Join(w.StorageWarnings, "; "), "ok")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", w.PK, or(w.Hostname, "-"), w.Source, state, seen, storage) //nolint:errcheck
	}
	tw.Flush() //nolint:errcheck,gosec
	if len(st.LastEvents) > 0 {
		b.WriteString("\nrecent alerts:\n")
		for _, ev := range st.LastEvents {
			fmt.Fprintf(&b, "  %s %s\n", ev.At.UTC().Format(time.RFC3339), alerts.Subject(ev))
			for _, s := range append([]string{ev.Diagnosis, ev.RecoveryReason}, ev.Warnings...) {
				if s != "" {
					fmt.Fprintf(&b, "    %s\n", s)
				}
			}
		}
	}
	return b.String()
}

var alertsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Change the offline alert settings, applied at once and saved",
	Long: `Change the offline alert settings. Only the flags given change; the
rest are kept. The change applies at once and is saved to alerts.json in
the visor's local directory, which wins over the config's alerts block
and survives a config regen.

  skywire cli visor alerts set --email ops@example.com --offline-after 15m
  skywire cli visor alerts set --smtp-host smtp.example.com --smtp-starttls --smtp-user me@example.com --smtp-pass '...'
  skywire cli visor alerts set --email ''      # stop mail alerts`,
	Run: func(cmd *cobra.Command, _ []string) {
		f := cmd.Flags()
		rpcClient, err := clirpc.Client(f)
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		st, err := rpcClient.AlertsStatus()
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		s := st.Settings
		if f.Changed("email") {
			s.EmailTo = trimmedList(alEmail)
		}
		if f.Changed("offline-after") {
			if alDelay < alerts.MinOfflineAfter || alDelay > alerts.MaxOfflineAfter {
				internal.PrintFatalError(f, fmt.Errorf("--offline-after must be between %s and %s", alerts.MinOfflineAfter, alerts.MaxOfflineAfter))
			}
			s.OfflineAfter = alerts.Duration(alDelay)
		}
		if f.Changed("webhook") {
			s.Webhook = alWebhook
		}
		if f.Changed("from") {
			s.From = alFrom
		}
		if f.Changed("disk-warn") {
			s.DiskWarnPercent = alDiskWarn
		}
		if f.Changed("watch") {
			s.Watch = nil
			for _, k := range trimmedList(alWatch) {
				var pk cipher.PubKey
				if err := pk.Set(k); err != nil {
					internal.PrintFatalError(f, fmt.Errorf("invalid public key %q: %w", k, err))
				}
				s.Watch = append(s.Watch, pk)
			}
		}
		relayFlags := []string{"smtp-host", "smtp-port", "smtp-user", "smtp-pass", "smtp-starttls"}
		if alNoSMTP {
			s.SMTP = nil
		} else if anyChanged(cmd, relayFlags...) {
			r := alerts.SMTP{}
			if s.SMTP != nil {
				r = *s.SMTP
			}
			if f.Changed("smtp-host") {
				r.Host = alSMTPHost
			}
			if f.Changed("smtp-port") {
				r.Port = alSMTPPort
			}
			if f.Changed("smtp-user") {
				r.Username = alSMTPUser
			}
			if f.Changed("smtp-starttls") {
				r.StartTLS = alSMTPTLS
			}
			r.Password = alSMTPPass
			s.SMTP = &r
		}
		if err := rpcClient.AlertsSet(s); err != nil {
			internal.PrintFatalError(f, err)
		}
		st, err = rpcClient.AlertsStatus()
		if err != nil {
			internal.PrintFatalError(f, err)
		}
		internal.PrintOutput(f, st, formatAlerts(st))
	},
}

var alertsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a test alert to every configured address and webhook",
	Run: func(cmd *cobra.Command, _ []string) {
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		res, err := rpcClient.AlertsTest()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var b strings.Builder
		for _, r := range res {
			if r.Err == "" {
				fmt.Fprintf(&b, "%s %s: sent\n", r.Channel, r.Target)
			} else {
				fmt.Fprintf(&b, "%s %s: FAILED: %s\n", r.Channel, r.Target, r.Err)
			}
		}
		internal.PrintOutput(cmd.Flags(), res, b.String())
	},
}

func trimmedList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func anyChanged(cmd *cobra.Command, names ...string) bool {
	for _, n := range names {
		if cmd.Flags().Changed(n) {
			return true
		}
	}
	return false
}
