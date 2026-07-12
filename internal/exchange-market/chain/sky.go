// Package chain implements the real blockchain backend behind jobs.Chain: a
// Skycoin fullnode client for SKY deposit verification and spending, plus a
// pluggable Explorer seam for external-coin payment verification. It is swapped
// in for jobs.NoopChain once the operator configures a SKY node URL. External
// payment verification stays a no-op until a provider is implemented.
package chain

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// skyEpsilon is half a droplet (SKY has 6 decimal places); amounts within this
// of each other are treated as equal.
const skyEpsilon = 0.0000005

// SkyNode talks to a Skycoin fullnode's REST API (default port 6420). It
// verifies incoming SKY deposits and spends SKY from a hot wallet on the node.
type SkyNode struct {
	baseURL  string
	walletID string
	password string
	confs    int
	hc       *http.Client
}

// NewSkyNode builds a SkyNode client. confs is the required confirmation depth.
func NewSkyNode(baseURL, walletID, password string, confs int, hc *http.Client) *SkyNode {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	if confs <= 0 {
		confs = 1
	}
	return &SkyNode{
		baseURL:  strings.TrimRight(baseURL, "/"),
		walletID: walletID,
		password: password,
		confs:    confs,
		hc:       hc,
	}
}

// txnStatus mirrors Skycoin's transaction status. In Skycoin, Height is the
// number of confirmations once Confirmed is true (headSeq - blockSeq + 1).
type txnStatus struct {
	Confirmed bool   `json:"confirmed"`
	Height    uint64 `json:"height"`
	BlockSeq  uint64 `json:"block_seq"`
}

type txnOutput struct {
	Dst   string `json:"dst"`
	Coins string `json:"coins"`
}

// verboseTxn is one element of GET /api/v1/transactions?verbose=1.
type verboseTxn struct {
	Status txnStatus `json:"status"`
	Txn    struct {
		TxID    string      `json:"txid"`
		Outputs []txnOutput `json:"outputs"`
	} `json:"txn"`
}

// DepositConfirmed reports whether marketWallet has received a confirmed SKY
// output of amountSKY (within a droplet) with at least the required
// confirmations. Returns the funding transaction id on success.
func (s *SkyNode) DepositConfirmed(marketWallet string, amountSKY float64) (bool, string, error) {
	q := url.Values{}
	q.Set("addrs", marketWallet)
	q.Set("confirmed", "1")
	q.Set("verbose", "1")

	var txns []verboseTxn
	if err := s.getJSON("/api/v1/transactions?"+q.Encode(), &txns); err != nil {
		return false, "", err
	}
	for _, t := range txns {
		if !t.Status.Confirmed || int(t.Status.Height) < s.confs { //nolint
			continue
		}
		for _, o := range t.Txn.Outputs {
			if o.Dst != marketWallet {
				continue
			}
			coins, err := strconv.ParseFloat(o.Coins, 64)
			if err != nil {
				continue
			}
			if math.Abs(coins-amountSKY) <= skyEpsilon {
				return true, t.Txn.TxID, nil
			}
		}
	}
	return false, "", nil
}

// createTxnResp is the response of POST /api/v1/wallet/transaction.
type createTxnResp struct {
	EncodedTransaction string `json:"encoded_transaction"`
}

// SendSKY spends amountSKY from the market's hot wallet to toAddr and returns
// the broadcast transaction id.
func (s *SkyNode) SendSKY(toAddr string, amountSKY float64) (string, error) {
	if s.walletID == "" {
		return "", fmt.Errorf("sky spend: no wallet_id configured")
	}
	csrf, err := s.csrfToken()
	if err != nil {
		return "", err
	}

	// Build + sign the transaction from the wallet.
	createReq := map[string]any{
		"wallet_id": s.walletID,
		"hours_selection": map[string]any{
			"type":         "auto",
			"mode":         "share",
			"share_factor": "0.5",
		},
		"to": []map[string]string{
			{"address": toAddr, "coins": formatSKY(amountSKY)},
		},
	}
	if s.password != "" {
		createReq["password"] = s.password
	}
	var created createTxnResp
	if err := s.postJSON("/api/v1/wallet/transaction", csrf, createReq, &created); err != nil {
		return "", fmt.Errorf("sky spend: create txn: %w", err)
	}
	if created.EncodedTransaction == "" {
		return "", fmt.Errorf("sky spend: node returned an empty transaction")
	}

	// Broadcast it.
	var txid string
	if err := s.postJSON("/api/v1/injectTransaction", csrf, map[string]string{"rawtx": created.EncodedTransaction}, &txid); err != nil {
		return "", fmt.Errorf("sky spend: inject txn: %w", err)
	}
	return txid, nil
}

// csrfToken fetches a CSRF token. If the node has CSRF disabled (404) it returns
// an empty token, which is fine — the POST simply omits the header.
func (s *SkyNode) csrfToken() (string, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/v1/csrf", nil)
	if err != nil {
		return "", err
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("sky csrf: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNotFound {
		return "", nil // CSRF disabled on this node
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sky csrf: status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("sky csrf: decode: %w", err)
	}
	return out.Token, nil
}

func (s *SkyNode) getJSON(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	return s.do(req, out)
}

func (s *SkyNode) postJSON(path, csrf string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.baseURL+path, strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	return s.do(req, out)
}

func (s *SkyNode) do(req *http.Request, out any) error {
	resp, err := s.hc.Do(req) //nolint
	if err != nil {
		return err
	}
	defer resp.Body.Close()                                 //nolint:errcheck
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node %s: status %d: %s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// formatSKY renders a SKY amount with Skycoin's fixed 6-decimal precision.
func formatSKY(amount float64) string {
	return strconv.FormatFloat(amount, 'f', 6, 64)
}
