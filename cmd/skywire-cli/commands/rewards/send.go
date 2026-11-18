// Package clirewards cmd/skywire-cli/commands/rewards/send.go c4-vis-cli
package clirewards

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/api"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/skycoin/skycoin/src/util/droplet"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"
	"golang.org/x/term"

	"github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/deployment/rewards"
)

// payoutCSVHeader and payoutDayHeader are the headers of GET
// /skycoin-rewards/payout (see rewardsserver.PayoutCSVHeader).
const (
	payoutCSVHeader = "X-Reward-CSV"
	payoutDayHeader = "X-Reward-Day"
)

type sendConfig struct {
	wallet string
	server string
	proxy  string
	http   string
	node   string
	yes    bool
}

var sendFlags sendConfig

func init() {
	RootCmd.AddCommand(sendCmd)
	f := sendCmd.Flags()
	f.SortFlags = false
	f.StringVarP(&sendFlags.wallet, "wallet", "w", "", "wallet file name or path (WALLET_FILE)")
	f.StringVar(&sendFlags.server, "server", "", "reward system dmsg://<pk>:80 (REWARD_SYS_URL)\n(default the deployment reward_system_dmsg)")
	f.StringVar(&sendFlags.proxy, "proxy", "socks5h://127.0.0.1:4445", "the visor's resolving SOCKS5 proxy")
	f.StringVar(&sendFlags.http, "http", "", "reward system http base URL in place of --server and --proxy\n(e.g. http://127.0.0.1:8081 from skywire dmsg web)")
	f.StringVar(&sendFlags.node, "node", "http://127.0.0.1:6420", "skycoin node API")
	f.BoolVarP(&sendFlags.yes, "yes", "y", false, "send without asking")
}

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "build the day's reward transaction and send it to the reward system",
	Long: `Builds the reward transaction of the oldest undistributed day with the local
skycoin node and wallet, and posts it to the reward system, which broadcasts it.

It checks that the node is up and the reward system is not calculating, prints
the day's stats and asks before sending. The transaction pays exactly the
payout csv (amounts truncated to 0.001 SKY) with its change to a new address
with no history: a new wallet entry for a deterministic wallet, the next unused
change address for a bip44 wallet. An encrypted wallet's password is asked on
the terminal.

Requests go through the visor's resolving SOCKS5 proxy, so the visor's key
must be in the reward system's whitelist.

WALLET_FILE and REWARD_SYS_URL can come from an env file, flags override it:

  SKYENV=/path/to/sendrewards.conf skywire cli rewards send`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg := sendFlags
		if err := applySkyenv(cmd, &cfg, os.Getenv("SKYENV")); err != nil {
			return err
		}
		s, err := newSender(cfg)
		if err != nil {
			return err
		}
		return s.run(cmd.Context())
	},
}

// applySkyenv fills the values not set by flag from the env file named by
// SKYENV. Only WALLET_FILE and REWARD_SYS_URL are read.
func applySkyenv(cmd *cobra.Command, cfg *sendConfig, skyenv string) error {
	if skyenv == "" {
		return nil
	}
	path, err := filepath.Abs(skyenv)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil { //nolint:gosec
		return fmt.Errorf("SKYENV: %w", err)
	}
	// The file may run skywire (WALLET_FILE=$(skywire skycoin cli ...)). Without
	// SKYENV in its environment that skywire does not read the file again.
	// Git Bash on Windows gives MSYS paths (/c/Users/...); cygpath turns them
	// back into native ones. cygpath exists only there.
	c := exec.Command("bash", "-c", `source "$SENDREWARDS_CONF" >/dev/null 2>&1
w=$WALLET_FILE
case $w in /*) command -v cygpath >/dev/null 2>&1 && w=$(cygpath -w "$w") ;; esac
printf '%s\n%s\n' "$w" "$REWARD_SYS_URL"`) //nolint:gosec
	c.Env = append(envWithout(os.Environ(), "SKYENV"), "SENDREWARDS_CONF="+path)
	c.Stderr = io.Discard
	out, err := c.Output()
	if err != nil {
		return fmt.Errorf("read SKYENV %s with bash: %w", path, err)
	}
	v := strings.SplitN(string(out), "\n", 3)
	if len(v) < 2 {
		return fmt.Errorf("read SKYENV %s: unexpected output", path)
	}
	if !cmd.Flags().Changed("wallet") && v[0] != "" {
		cfg.wallet = v[0]
	}
	if !cmd.Flags().Changed("server") && v[1] != "" {
		cfg.server = v[1]
	}
	return nil
}

type sender struct {
	hc   *http.Client
	base string
	node *api.Client
	wlt  string

	yes         bool
	in          *bufio.Reader
	out         io.Writer
	interactive func() bool
	password    func() ([]byte, error)
}

func newSender(cfg sendConfig) (*sender, error) {
	if cfg.wallet == "" {
		return nil, errors.New("no wallet: set --wallet or WALLET_FILE in the SKYENV file")
	}
	s := &sender{
		node:        api.NewClient(cfg.node),
		wlt:         filepath.Base(cfg.wallet),
		yes:         cfg.yes,
		in:          bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }, //nolint:gosec
		password: func() ([]byte, error) {
			fmt.Fprint(os.Stderr, "Wallet password: ")      //nolint:errcheck
			p, err := term.ReadPassword(int(os.Stdin.Fd())) //nolint:gosec
			fmt.Fprintln(os.Stderr)                         //nolint:errcheck
			return p, err
		},
	}
	if cfg.http != "" {
		s.base = strings.TrimRight(cfg.http, "/")
		s.hc = &http.Client{Timeout: 2 * time.Minute}
		return s, nil
	}
	server := cfg.server
	if server == "" {
		server = deployment.Prod.RewardSystemDmsg
	}
	base, err := dmsgProxyBase(server)
	if err != nil {
		return nil, err
	}
	tr, err := socks5Transport(cfg.proxy)
	if err != nil {
		return nil, err
	}
	s.base = base
	s.hc = &http.Client{Transport: tr, Timeout: 2 * time.Minute}
	return s, nil
}

// dmsgProxyBase turns dmsg://<pk>:<port> (or a bare pk) into the
// http://<pk>.dmsg base a resolving proxy serves.
func dmsgProxyBase(server string) (string, error) {
	host := strings.TrimRight(strings.TrimPrefix(server, "dmsg://"), "/")
	port := ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		host, port = h, p
	}
	if _, err := cipher.PubKeyFromHex(host); err != nil {
		return "", fmt.Errorf("reward system %q: want dmsg://<pk>:80", server)
	}
	if port == "" || port == "80" {
		return "http://" + host + ".dmsg", nil
	}
	return "http://" + host + ".dmsg:" + port, nil
}

// socks5Transport dials through a SOCKS5 proxy (socks5h://host:port or
// host:port), which resolves the <pk>.dmsg names.
func socks5Transport(p string) (*http.Transport, error) {
	addr := p
	if u, err := url.Parse(p); err == nil && u.Host != "" {
		if u.Scheme != "socks5h" && u.Scheme != "socks5" {
			return nil, fmt.Errorf("--proxy %q: want socks5h://host:port", p)
		}
		addr = u.Host
	}
	d, err := proxy.SOCKS5("tcp", addr, nil, cliutil.LoopbackDialer)
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 dialer for %s does not support contexts", addr)
	}
	return &http.Transport{DialContext: cd.DialContext}, nil
}

func (s *sender) printf(format string, a ...any) {
	fmt.Fprintf(s.out, format, a...) //nolint:errcheck
}

func (s *sender) do(ctx context.Context, method, path string, body []byte, hdr http.Header) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.base+"/"+strings.TrimLeft(path, "/"), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, b, nil
}

func (s *sender) run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.yes && !s.interactive() {
		return errors.New("stdin is not a terminal: pass --yes to send without asking")
	}
	// 1. the node and the wallet
	if _, err := s.node.Health(); err != nil {
		return fmt.Errorf("skycoin node not reachable: %w", err)
	}
	w, err := s.node.Wallet(s.wlt)
	if err != nil {
		return fmt.Errorf("wallet %s: %w", s.wlt, err)
	}
	switch w.Meta.Type {
	case "deterministic", "bip44":
	default:
		return fmt.Errorf("wallet %s has type %q: only deterministic and bip44 wallets are supported", s.wlt, w.Meta.Type)
	}
	if w.Meta.Encrypted && !s.interactive() {
		return fmt.Errorf("wallet %s is encrypted and stdin is not a terminal to ask its password", s.wlt)
	}

	// 2. not while the reward system calculates
	resp, b, err := s.do(ctx, http.MethodGet, "skycoin-rewards/s", nil, nil)
	if err != nil {
		return err
	}
	var st struct {
		Active string `json:"active"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &st) != nil {
		return fmt.Errorf("reward system status: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	s.printf("Reward system status: %s\n\n", st.Active)
	if !strings.Contains(st.Active, "inactive") {
		return errors.New("rewards are calculating, not sending to avoid a partial csv. Try again later")
	}

	// 3. preview
	resp, b, err = s.do(ctx, http.MethodGet, "skycoin-rewards/csv", nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("no undistributed rewards (http status %d)", resp.StatusCode)
	}
	fullPath := strings.TrimSpace(string(b))
	resp, b, err = s.do(ctx, http.MethodGet, strings.Replace(fullPath, "_rewardtxn0.csv", "_stats.txt", 1), nil, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch the reward statistics: %v", errOrStatus(err, resp))
	}
	s.printf("%s\n", b)

	// 4. confirm
	if !s.yes {
		s.printf("Send rewards? [y/N]: ")
		line, _ := s.in.ReadString('\n') //nolint:errcheck
		if a := strings.TrimSpace(line); a != "y" && a != "Y" {
			s.printf("exiting\n")
			return nil
		}
	}

	// 5. the payout csv
	resp, payout, err := s.do(ctx, http.MethodGet, "skycoin-rewards/payout", nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get the payout csv: %s", statusMessage(resp.StatusCode, payout))
	}
	csvHash := strings.TrimSpace(resp.Header.Get(payoutCSVHeader))
	if csvHash == "" {
		return fmt.Errorf("the reward system sent no %s header", payoutCSVHeader)
	}
	day := resp.Header.Get(payoutDayHeader)
	s.printf("Payout day: %s\n", day)
	want, err := rewards.PayoutDroplets(payout)
	if err != nil {
		return err
	}
	if len(want) == 0 {
		return errors.New("empty reward csv")
	}
	to, err := receivers(payout, want)
	if err != nil {
		return err
	}
	payees := rewards.CSVAddresses(payout)
	if day != "" && strings.HasSuffix(fullPath, "/"+day+"_rewardtxn0.csv") {
		if resp, full, err := s.do(ctx, http.MethodGet, fullPath, nil, nil); err == nil && resp.StatusCode == http.StatusOK {
			for a := range rewards.CSVAddresses(full) {
				payees[a] = true
			}
		}
	}

	// 6. no unconfirmed transaction to the wallet
	walletAddrs := map[string]bool{}
	from := make([]string, 0, len(w.Entries))
	for _, e := range w.Entries {
		walletAddrs[e.Address] = true
		from = append(from, e.Address)
	}
	pending, err := s.node.PendingTransactions()
	if err != nil {
		return fmt.Errorf("pending transactions: %w", err)
	}
	for _, p := range pending {
		for _, o := range p.Transaction.Out {
			if walletAddrs[o.Address] {
				return fmt.Errorf("unconfirmed transaction %s pays wallet address %s: wait for it to confirm", p.Transaction.Hash, o.Address)
			}
		}
	}

	var pw []byte
	if w.Meta.Encrypted {
		if pw, err = s.password(); err != nil {
			return fmt.Errorf("read the wallet password: %w", err)
		}
		defer clear(pw)
	}

	// 7. the change address
	var newChange string
	var changeAddr *string
	if w.Meta.Type == "deterministic" {
		a, err := s.node.NewWalletAddress(s.wlt, string(pw))
		if err != nil {
			return fmt.Errorf("add a change address to %s: %w", s.wlt, err)
		}
		if len(a) != 1 || a[0] == "" {
			return fmt.Errorf("add a change address to %s: got %v", s.wlt, a)
		}
		newChange = a[0]
		changeAddr = &newChange
	}

	// 8. build
	rsp, err := s.node.WalletCreateTransaction(api.WalletCreateTransactionRequest{
		WalletID: s.wlt,
		Password: string(pw),
		CreateTransactionRequest: api.CreateTransactionRequest{
			HoursSelection: api.HoursSelection{Type: "auto", Mode: "share", ShareFactor: "0.5"},
			ChangeAddress:  changeAddr,
			Addresses:      from,
			To:             to,
		},
	})
	if err != nil {
		return fmt.Errorf("create the transaction: %w", err)
	}
	if len(rsp.Transactions) > 1 {
		return fmt.Errorf("the node split the payout into %d transactions, the reward system takes one", len(rsp.Transactions))
	}

	// 9. verify
	change, err := s.verify(rsp, want, payees, newChange)
	if err != nil {
		return err
	}
	if change != "" {
		s.printf("Change address: %s (unused)\n", change)
	}

	// 10. send
	resp, b, err = s.do(ctx, http.MethodPost, "reward", []byte(rsp.EncodedTransaction), http.Header{payoutCSVHeader: {csvHash}})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the reward system did not send it: %s", statusMessage(resp.StatusCode, b))
	}
	s.printf("%s\n", strings.TrimSpace(string(b)))
	return nil
}

// verify checks the transaction pays exactly want, with at most one change
// output, to an address that is no input, is newChange when set, and has no
// history. It returns the change address, "" for none.
func (s *sender) verify(rsp *api.CreateTransactionResponse, want map[cipher.Address]uint64, payees map[cipher.Address]bool, newChange string) (string, error) {
	txn, err := coin.DeserializeTransactionHex(rsp.EncodedTransaction)
	if err != nil {
		return "", fmt.Errorf("decode the transaction: %w", err)
	}
	var change []string
	for _, o := range txn.Out {
		if _, ok := want[o.Address]; !ok {
			if a := o.Address.String(); !contains(change, a) {
				change = append(change, a)
			}
		}
	}
	if len(change) > 1 {
		return "", fmt.Errorf("more than one change output: %s", strings.Join(change, " "))
	}
	if err := rewards.CheckTransactionPaysExactly(rsp.EncodedTransaction, want, payees); err != nil {
		return "", err
	}
	if len(change) == 0 {
		return "", nil
	}
	c := change[0]
	for _, in := range rsp.Transaction.In {
		if in.Address == c {
			return "", fmt.Errorf("change address %s is also an input address", c)
		}
	}
	if newChange != "" && c != newChange {
		return "", fmt.Errorf("change went to %s, expected %s", c, newChange)
	}
	hist, err := s.node.Transactions([]string{c})
	if err != nil {
		return "", fmt.Errorf("history of change address %s: %w", c, err)
	}
	if len(hist) != 0 {
		return "", fmt.Errorf("change address %s has transaction history", c)
	}
	return c, nil
}

// receivers lists want in the order of the payout csv.
func receivers(csv []byte, want map[cipher.Address]uint64) ([]api.Receiver, error) {
	var to []api.Receiver
	seen := map[cipher.Address]bool{}
	sc := bufio.NewScanner(bytes.NewReader(csv))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		a, _, _ := strings.Cut(strings.ReplaceAll(sc.Text(), " ", ""), ",")
		addr, err := cipher.DecodeBase58Address(a)
		if err != nil || seen[addr] {
			continue
		}
		d, ok := want[addr]
		if !ok {
			continue
		}
		seen[addr] = true
		coins, err := droplet.ToString(d)
		if err != nil {
			return nil, err
		}
		to = append(to, api.Receiver{Address: a, Coins: coins})
	}
	return to, sc.Err()
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func errOrStatus(err error, resp *http.Response) string {
	if err != nil {
		return err.Error()
	}
	return resp.Status
}

// statusMessage explains a refusal of the payout or reward routes.
func statusMessage(code int, body []byte) string {
	b := strings.TrimSpace(string(body))
	switch code {
	case http.StatusConflict:
		return "409 conflict: the payout csv changed since it was fetched, a send did not finish, or the transaction does not pay the csv. Run send again once it is resolved.\n" + b
	case http.StatusPreconditionRequired:
		return "428 precondition required: the " + payoutCSVHeader + " header was not sent.\n" + b
	case http.StatusServiceUnavailable:
		return "503 unavailable: rewards are calculating or the payout csv could not be checked. Try again later.\n" + b
	case http.StatusNotFound:
		return "404: no undistributed rewards.\n" + b
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Sprintf("%d: this visor's key is not in the reward system whitelist.\n%s", code, b)
	}
	return fmt.Sprintf("http status %d\n%s", code, b)
}

// envWithout is env less the variable named key.
func envWithout(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}
