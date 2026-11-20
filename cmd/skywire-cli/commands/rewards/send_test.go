package clirewards

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/skycoin/skycoin/src/api"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/skycoin/skycoin/src/readable"
	"github.com/skycoin/skycoin/src/util/droplet"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/rewards"
)

func newAddr() cipher.Address {
	pk, _ := cipher.GenerateKeyPair()
	return cipher.AddressFromPubKey(pk)
}

// fakeNode is a skycoin node API with one wallet.
type fakeNode struct {
	t          *testing.T
	walletType string
	encrypted  bool
	input      cipher.Address
	newAddr    cipher.Address // deterministic: newAddress hands it out
	bip44Chg   cipher.Address // bip44: the node's change address
	pending    []readable.UnconfirmedTransactions
	history    map[string]int
	extraOut   []cipher.Address // more outputs the node adds
	gotReq     api.WalletCreateTransactionRequest
	gotPW      string
	requests   atomic.Int32
}

func (n *fakeNode) serve() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/csrf", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) }) //nolint:errcheck,gosec
	mux.HandleFunc("GET /api/v1/wallet", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(n.t, "rewards.wlt", r.URL.Query().Get("id"))
		json.NewEncoder(w).Encode(api.WalletResponse{ //nolint:errcheck,gosec
			Meta:    readable.WalletMeta{Filename: "rewards.wlt", Type: n.walletType, Encrypted: n.encrypted},
			Entries: []readable.WalletEntry{{Address: n.input.String()}},
		})
	})
	mux.HandleFunc("GET /api/v1/pendingTxs", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(n.pending) //nolint:errcheck,gosec
	})
	mux.HandleFunc("POST /api/v1/wallet/newAddress", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(n.t, r.ParseForm())
		n.gotPW = r.Form.Get("password")
		json.NewEncoder(w).Encode(map[string][]string{"addresses": {n.newAddr.String()}}) //nolint:errcheck,gosec
	})
	mux.HandleFunc("POST /api/v1/wallet/transaction", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(n.t, json.NewDecoder(r.Body).Decode(&n.gotReq))
		txn := coin.Transaction{In: []cipher.SHA256{cipher.SumSHA256([]byte("ux"))}, Sigs: []cipher.Sig{{}}}
		for _, to := range n.gotReq.To {
			d, err := droplet.FromString(to.Coins)
			require.NoError(n.t, err)
			txn.Out = append(txn.Out, coin.TransactionOutput{Address: cipher.MustDecodeBase58Address(to.Address), Coins: d, Hours: 1})
		}
		chg := n.bip44Chg
		if n.gotReq.ChangeAddress != nil {
			chg = cipher.MustDecodeBase58Address(*n.gotReq.ChangeAddress)
		}
		for _, a := range append([]cipher.Address{chg}, n.extraOut...) {
			txn.Out = append(txn.Out, coin.TransactionOutput{Address: a, Coins: 1e6, Hours: 1})
		}
		enc, err := txn.SerializeHex()
		require.NoError(n.t, err)
		json.NewEncoder(w).Encode(api.CreateTransactionResponse{ //nolint:errcheck,gosec
			Transaction:        api.CreatedTransaction{In: []api.CreatedTransactionInput{{Address: n.input.String()}}},
			EncodedTransaction: enc,
		})
	})
	mux.HandleFunc("POST /api/v1/transactions", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(n.t, r.ParseForm())
		out := []readable.TransactionWithStatus{}
		for range n.history[r.Form.Get("addrs")] {
			out = append(out, readable.TransactionWithStatus{})
		}
		json.NewEncoder(w).Encode(out) //nolint:errcheck,gosec
	})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
}

// fakeRewards is the reward system's side of the payout.
type fakeRewards struct {
	t        *testing.T
	active   string
	payout   string
	full     string
	postCode int
	posted   string
}

const testDay = "2026-11-16"

func (f *fakeRewards) serve() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /skycoin-rewards/s", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"active": f.active}) //nolint:errcheck,gosec
	})
	mux.HandleFunc("GET /skycoin-rewards/csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("skycoin-rewards/hist/" + testDay + "_rewardtxn0.csv")) //nolint:errcheck,gosec
	})
	mux.HandleFunc("GET /skycoin-rewards/hist/"+testDay+"_stats.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("stats of the day")) //nolint:errcheck,gosec
	})
	mux.HandleFunc("GET /skycoin-rewards/hist/"+testDay+"_rewardtxn0.csv", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(f.full)) //nolint:errcheck,gosec
	})
	mux.HandleFunc("GET /skycoin-rewards/payout", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(payoutDayHeader, testDay)
		w.Header().Set(payoutCSVHeader, "hash-"+testDay)
		w.Write([]byte(f.payout)) //nolint:errcheck,gosec
	})
	mux.HandleFunc("POST /reward", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(f.t, "hash-"+testDay, r.Header.Get(payoutCSVHeader))
		b, _ := io.ReadAll(r.Body) //nolint:errcheck,gosec
		f.posted = string(b)
		if f.postCode != 0 {
			w.WriteHeader(f.postCode)
			w.Write([]byte("rewards for " + testDay + " were recalculated")) //nolint:errcheck,gosec
			return
		}
		w.Write([]byte("txid-of-the-day")) //nolint:errcheck,gosec
	})
	return httptest.NewServer(mux)
}

type sendRig struct {
	node    *fakeNode
	rewards *fakeRewards
	sender  *sender
	out     *bytes.Buffer
	payees  []cipher.Address
}

func newSendRig(t *testing.T, walletType string) *sendRig {
	a, b, skipped, dust := newAddr(), newAddr(), newAddr(), newAddr()
	node := &fakeNode{t: t, walletType: walletType, input: newAddr(), newAddr: newAddr(), bip44Chg: newAddr()}
	rw := &fakeRewards{
		t:      t,
		active: "inactive",
		payout: "Skycoin Address, Reward Amount\n" + a.String() + ", 12.3456789\n" + b.String() + ", 0.5\n" + dust.String() + ", 0.0009\n",
	}
	rw.full = rw.payout + skipped.String() + ", 3.0\n"
	ns, rs := node.serve(), rw.serve()
	t.Cleanup(ns.Close)
	t.Cleanup(rs.Close)
	out := &bytes.Buffer{}
	return &sendRig{
		node:    node,
		rewards: rw,
		out:     out,
		payees:  []cipher.Address{a, b},
		sender: &sender{
			hc:          rs.Client(),
			base:        rs.URL,
			node:        api.NewClient(ns.URL),
			wlt:         "rewards.wlt",
			yes:         true,
			in:          bufio.NewReader(strings.NewReader("")),
			out:         out,
			interactive: func() bool { return false },
			password:    func() ([]byte, error) { return []byte("secret"), nil },
		},
	}
}

// checkPosted asserts the posted transaction pays exactly the truncated csv
// plus the change.
func (r *sendRig) checkPosted(t *testing.T, change cipher.Address) {
	txn, err := coin.DeserializeTransactionHex(r.rewards.posted)
	require.NoError(t, err)
	got := map[cipher.Address]uint64{}
	for _, o := range txn.Out {
		got[o.Address] += o.Coins
	}
	require.Equal(t, map[cipher.Address]uint64{r.payees[0]: 12_345_000, r.payees[1]: 500_000, change: 1e6}, got)
	want, err := rewards.PayoutDroplets([]byte(r.rewards.payout))
	require.NoError(t, err)
	require.NoError(t, rewards.CheckTransactionPaysExactly(r.rewards.posted, want, rewards.CSVAddresses([]byte(r.rewards.full))))
}

func TestSendDeterministic(t *testing.T) {
	r := newSendRig(t, "deterministic")
	r.node.encrypted = true
	r.sender.interactive = func() bool { return true }
	require.NoError(t, r.sender.run(t.Context()))
	r.checkPosted(t, r.node.newAddr)
	require.Equal(t, "secret", r.node.gotPW)
	require.Equal(t, "secret", r.node.gotReq.Password)
	require.False(t, r.node.gotReq.Unsigned)
	require.Contains(t, r.out.String(), "stats of the day")
	require.Contains(t, r.out.String(), "Change address: "+r.node.newAddr.String()+" (unused)")
	require.Contains(t, r.out.String(), "txid-of-the-day")
}

func TestSendBip44(t *testing.T) {
	r := newSendRig(t, "bip44")
	require.NoError(t, r.sender.run(t.Context()))
	require.Nil(t, r.node.gotReq.ChangeAddress)
	r.checkPosted(t, r.node.bip44Chg)
}

func TestSendAsksFirst(t *testing.T) {
	r := newSendRig(t, "bip44")
	r.sender.yes = false
	r.sender.interactive = func() bool { return true }
	r.sender.in = bufio.NewReader(strings.NewReader("n\n"))
	require.NoError(t, r.sender.run(t.Context()))
	require.Empty(t, r.rewards.posted)
	require.Contains(t, r.out.String(), "Send rewards? [y/N]")
}

func TestSendRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*sendRig)
		err   string
	}{
		{"calculating", func(r *sendRig) { r.rewards.active = "active" }, "rewards are calculating"},
		{"unsupported wallet", func(r *sendRig) { r.node.walletType = "collection" }, "only deterministic and bip44"},
		{"unconfirmed to wallet", func(r *sendRig) {
			var p readable.UnconfirmedTransactions
			p.Transaction.Out = []readable.TransactionOutput{{Address: r.node.input.String()}}
			r.node.pending = []readable.UnconfirmedTransactions{p}
		}, "wait for it to confirm"},
		{"two change outputs", func(r *sendRig) { r.node.extraOut = []cipher.Address{newAddr()} }, "more than one change output"},
		{"change with history", func(r *sendRig) {
			r.node.history = map[string]int{r.node.bip44Chg.String(): 1}
		}, "has transaction history"},
		{"change is an input", func(r *sendRig) { r.node.bip44Chg = r.node.input }, "also an input address"},
		{"stale csv", func(r *sendRig) { r.rewards.postCode = http.StatusConflict }, "409 conflict: the payout csv changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newSendRig(t, "bip44")
			tc.setup(r)
			err := r.sender.run(t.Context())
			require.ErrorContains(t, err, tc.err)
			if tc.name != "stale csv" {
				require.Empty(t, r.rewards.posted)
			}
		})
	}
}

func TestSendNeedsYesWithoutTerminal(t *testing.T) {
	r := newSendRig(t, "bip44")
	r.sender.yes = false
	require.ErrorContains(t, r.sender.run(t.Context()), "pass --yes")
	require.Zero(t, r.node.requests.Load())
	require.Empty(t, r.rewards.posted)
}

func TestSendSkyenv(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "sendrewards.conf")
	require.NoError(t, os.WriteFile(conf, []byte(`WALLET_FILE="$HOME/.skycoin/wallets/conf.wlt"
REWARD_WL_SK=0000000000000000000000000000000000000000000000000000000000000001
REWARD_SYS_URL="dmsg://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f:80"
`), 0o600))
	newCmd := func() (*cobra.Command, *sendConfig) {
		cfg := &sendConfig{}
		c := &cobra.Command{}
		c.Flags().StringVar(&cfg.wallet, "wallet", "", "")
		c.Flags().StringVar(&cfg.server, "server", "", "")
		return c, cfg
	}

	c, cfg := newCmd()
	require.NoError(t, applySkyenv(c, cfg, conf))
	require.Equal(t, filepath.Clean(os.Getenv("HOME")+"/.skycoin/wallets/conf.wlt"), filepath.Clean(cfg.wallet))
	require.Equal(t, "dmsg://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f:80", cfg.server)

	c, cfg = newCmd()
	require.NoError(t, c.Flags().Set("wallet", "flag.wlt"))
	require.NoError(t, c.Flags().Set("server", "dmsg://flag:80"))
	require.NoError(t, applySkyenv(c, cfg, conf))
	require.Equal(t, "flag.wlt", cfg.wallet)
	require.Equal(t, "dmsg://flag:80", cfg.server)

	c, cfg = newCmd()
	require.ErrorContains(t, applySkyenv(c, cfg, filepath.Join(t.TempDir(), "missing.conf")), "SKYENV")

	base, err := dmsgProxyBase("dmsg://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f:80")
	require.NoError(t, err)
	require.Equal(t, "http://036a70e6956061778e1883e928c1236189db14dfd446df23d83e45c321b330c91f.dmsg", base)
	_, err = dmsgProxyBase("dmsg://nope:80")
	require.Error(t, err)
}

// The conf may run skywire to find the wallet. That skywire must not see
// SKYENV, or it would read the conf again, without end.
func TestApplySkyenvHidesSkyenvFromTheConf(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "sendrewards.conf")
	if err := os.WriteFile(conf, []byte(`WALLET_FILE=$([ -n "$SKYENV" ] && echo recursed || echo rewards.wlt)`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SKYENV", conf)
	cmd := &cobra.Command{}
	cmd.Flags().String("wallet", "", "")
	cmd.Flags().String("server", "", "")
	var cfg sendConfig
	if err := applySkyenv(cmd, &cfg, conf); err != nil {
		t.Fatal(err)
	}
	if cfg.wallet != "rewards.wlt" {
		t.Fatalf("wallet = %q", cfg.wallet)
	}
}
