// Command fakenode is a fake Skycoin node for the iOS wallet's bench tests
// (ios/SkywireUITests/WalletBenchChecks.swift). A phrase's first addresses hold
// a few outputs, and every injected transaction is checked with skycoin's own
// signature and fee rules before it is applied. The /bench/ routes reset it,
// report the last transaction, and make it look behind, alone or busy.
//
//	go run ./ios/scripts/fakenode
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/skycoin/skycoin/src/util/droplet"
	"github.com/skycoin/skycoin/src/util/fee"
)

const burnFactor = 10

type entry struct {
	Status map[string]any `json:"status"`
	Time   uint64         `json:"time"`
	Txn    map[string]any `json:"txn"`
}

var (
	mu       sync.Mutex
	unspent  = map[string]coin.UxOut{}
	history  []entry
	headSeq  uint64 = 1000
	headTime        = uint64(time.Now().Unix())
	pending  []map[string]any
	last     map[string]any
	// What /bench/node last set: whether the chain reports itself behind, and its peer count.
	syncing     bool
	connections = 8
)

func coinsText(d uint64) string {
	s, err := droplet.ToString(d)
	if err != nil {
		panic(err)
	}
	return s
}

// hoursAt is ux's coin hours at t, or 0 where they overflow.
func hoursAt(ux coin.UxOut, t uint64) uint64 {
	h, err := ux.CoinHours(t)
	if err != nil {
		return 0
	}
	return h
}

func output(ux coin.UxOut) map[string]any {
	h := hoursAt(ux, headTime)
	return map[string]any{
		"hash": ux.Hash().Hex(), "time": ux.Head.Time, "block_seq": ux.Head.BkSeq,
		"src_tx": ux.Body.SrcTransaction.Hex(), "address": ux.Body.Address.String(),
		"coins": coinsText(ux.Body.Coins), "hours": ux.Body.Hours, "calculated_hours": h,
	}
}

func owned(r *http.Request) map[string]bool {
	out := map[string]bool{}
	for _, a := range strings.Split(r.URL.Query().Get("addrs"), ",") {
		if a != "" {
			out[a] = true
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write: %v", err)
	}
}

func sortedUnspent() []coin.UxOut {
	var list []coin.UxOut
	for _, ux := range unspent {
		list = append(list, ux)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Hash().Hex() < list[j].Hash().Hex() })
	return list
}

func main() {
	addr := flag.String("addr", "127.0.0.1:6420", "listen address")
	phrase := flag.String("seed", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "wallet phrase")
	flag.Parse()

	keys := cipher.MustGenerateDeterministicKeyPairs([]byte(*phrase), 3)
	var addrs []cipher.Address
	for _, sk := range keys {
		addrs = append(addrs, cipher.MustAddressFromSecKey(sk))
	}
	reset := func() {
		unspent = map[string]coin.UxOut{}
		history = nil
		last = nil
		headSeq = 1000
		syncing = false
		connections = 8
		pending = nil
		stranger := cipher.MustAddressFromSecKey(cipher.MustGenerateDeterministicKeyPairs([]byte("stranger"), 1)[0])
		funding := []struct {
			to    int
			coins uint64
			hours uint64
		}{{0, 10_000_000, 400}, {0, 2_500_000, 60}, {0, 1_000_000, 8}, {1, 4_000_000, 120}}
		for i, f := range funding {
			src := cipher.SumSHA256([]byte(fmt.Sprintf("funding %d", i)))
			ux := coin.UxOut{
				Head: coin.UxHead{Time: headTime - 3600, BkSeq: 900 + uint64(i)},
				Body: coin.UxBody{SrcTransaction: src, Address: addrs[f.to], Coins: f.coins, Hours: f.hours},
			}
			unspent[ux.Hash().Hex()] = ux
			history = append(history, entry{
				Status: map[string]any{"confirmed": true, "unconfirmed": false, "height": headSeq - ux.Head.BkSeq + 1, "block_seq": ux.Head.BkSeq},
				Time:   ux.Head.Time,
				Txn: map[string]any{
					"txid": src.Hex(), "timestamp": ux.Head.Time, "inner_hash": src.Hex(), "fee": 1,
					"inputs":  []map[string]any{{"uxid": cipher.SumSHA256([]byte(fmt.Sprintf("in %d", i))).Hex(), "owner": stranger.String(), "coins": coinsText(f.coins), "hours": f.hours + 1, "calculated_hours": f.hours + 1}},
					"outputs": []map[string]any{{"uxid": ux.Hash().Hex(), "dst": addrs[f.to].String(), "coins": coinsText(f.coins), "hours": f.hours}},
				},
			})
		}
	}
	reset()
	log.Printf("addresses: %v", addrs)

	http.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writeJSON(w, map[string]any{
			"blockchain":              map[string]any{"head": map[string]any{"seq": headSeq, "timestamp": headTime}},
			"user_verify_transaction": map[string]any{"burn_factor": burnFactor, "max_transaction_size": 32768, "max_decimals": 3},
			"coin":                    "skycoin", "version": map[string]any{"version": "0.28.0"},
			"blockchain_pubkey": "0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a",
			"fiber":             map[string]any{"name": "skycoin", "display_name": "Skycoin", "ticker": "SKY", "explorer_url": "https://explorer.skycoin.com"},
			"open_connections":  connections,
		})
	})
	http.HandleFunc("/api/v1/blockchain/progress", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		highest := headSeq
		if syncing {
			highest += 50
		}
		writeJSON(w, map[string]any{"current": headSeq, "highest": highest, "peers": []any{}})
	})
	http.HandleFunc("/api/v1/blockchain/metadata", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"head": map[string]any{"seq": headSeq, "timestamp": headTime, "block_hash": cipher.SumSHA256([]byte("head")).Hex()}})
	})
	http.HandleFunc("/api/v1/coinSupply", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"current_supply": "24239050.000000", "total_supply": "100000000.000000"})
	})
	http.HandleFunc("/api/v1/network/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"connections": []map[string]any{
			{"address": "139.162.161.41:20002", "height": headSeq, "outgoing": true, "is_trusted_peer": true, "user_agent": "skycoin:0.28.0", "last_sent": headTime, "last_received": headTime},
			{"address": "45.33.32.156:6000", "height": headSeq - 2, "outgoing": false, "is_trusted_peer": false, "user_agent": "skycoin:0.27.1", "last_sent": headTime - 30, "last_received": headTime - 40},
		}})
	})
	http.HandleFunc("/api/v1/pendingTxs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodDelete {
			txid := r.URL.Query().Get("txid")
			kept := pending[:0]
			for _, p := range pending {
				if p["transaction"].(map[string]any)["txid"] != txid {
					kept = append(kept, p)
				}
			}
			pending = kept
			writeJSON(w, map[string]any{})
			return
		}
		writeJSON(w, pending)
	})
	http.HandleFunc("/api/v1/outputs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		mine := owned(r)
		head := []map[string]any{}
		for _, ux := range sortedUnspent() {
			if mine[ux.Body.Address.String()] {
				head = append(head, output(ux))
			}
		}
		writeJSON(w, map[string]any{"head": map[string]any{"seq": headSeq, "timestamp": headTime}, "head_outputs": head, "outgoing_outputs": []any{}, "incoming_outputs": []any{}})
	})
	http.HandleFunc("/api/v1/balance", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		mine := owned(r)
		var c, h uint64
		for _, ux := range unspent {
			if mine[ux.Body.Address.String()] {
				c += ux.Body.Coins
				hh := hoursAt(ux, headTime)
				h += hh
			}
		}
		pair := map[string]any{"coins": c, "hours": h}
		writeJSON(w, map[string]any{"confirmed": pair, "predicted": pair})
	})
	http.HandleFunc("/api/v1/transactions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		mine := owned(r)
		out := []entry{}
		for _, e := range history {
			touches := false
			for _, in := range e.Txn["inputs"].([]map[string]any) {
				touches = touches || mine[in["owner"].(string)]
			}
			for _, o := range e.Txn["outputs"].([]map[string]any) {
				touches = touches || mine[o["dst"].(string)]
			}
			if touches {
				out = append(out, e)
			}
		}
		writeJSON(w, out)
	})
	http.HandleFunc("/api/v1/injectTransaction", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Rawtx string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "400 Bad Request - "+err.Error(), 400)
			return
		}
		txn, err := coin.DeserializeTransactionHex(body.Rawtx)
		if err != nil {
			http.Error(w, "400 Bad Request - "+err.Error(), 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var in coin.UxArray
		for _, h := range txn.In {
			ux, ok := unspent[h.Hex()]
			if !ok {
				log.Printf("INJECT REFUSED unknown input %s", h.Hex())
				http.Error(w, "400 Bad Request - unspent output of "+h.Hex()+" does not exist", 400)
				return
			}
			in = append(in, ux)
		}
		if err := txn.Verify(); err != nil {
			log.Printf("INJECT REFUSED verify: %v", err)
			http.Error(w, "400 Bad Request - "+err.Error(), 400)
			return
		}
		if err := txn.VerifyInputSignatures(in); err != nil {
			log.Printf("INJECT REFUSED signatures: %v", err)
			http.Error(w, "400 Bad Request - "+err.Error(), 400)
			return
		}
		f, err := fee.TransactionFee(&txn, headTime, in)
		if err == nil {
			err = fee.VerifyTransactionFee(&txn, f, burnFactor)
		}
		if err != nil {
			log.Printf("INJECT REFUSED fee: %v", err)
			http.Error(w, "400 Bad Request - "+err.Error(), 400)
			return
		}
		var inCoins, outCoins uint64
		inputs := []map[string]any{}
		for _, ux := range in {
			inCoins += ux.Body.Coins
			hh := hoursAt(ux, headTime)
			inputs = append(inputs, map[string]any{"uxid": ux.Hash().Hex(), "owner": ux.Body.Address.String(), "coins": coinsText(ux.Body.Coins), "hours": ux.Body.Hours, "calculated_hours": hh})
			delete(unspent, ux.Hash().Hex())
		}
		headSeq++
		headTime += 10
		created := coin.CreateUnspents(coin.BlockHeader{Time: headTime, BkSeq: headSeq}, txn)
		outputs := []map[string]any{}
		for _, ux := range created {
			outCoins += ux.Body.Coins
			unspent[ux.Hash().Hex()] = ux
			outputs = append(outputs, map[string]any{"uxid": ux.Hash().Hex(), "dst": ux.Body.Address.String(), "coins": coinsText(ux.Body.Coins), "hours": ux.Body.Hours})
		}
		history = append(history, entry{
			Status: map[string]any{"confirmed": true, "unconfirmed": false, "height": 1, "block_seq": headSeq},
			Time:   headTime,
			Txn:    map[string]any{"txid": txn.Hash().Hex(), "timestamp": headTime, "inner_hash": txn.InnerHash.Hex(), "fee": f, "inputs": inputs, "outputs": outputs},
		})
		last = map[string]any{"txid": txn.Hash().Hex(), "fee": f, "inputs": inputs, "outputs": outputs}
		var outs []string
		for _, o := range txn.Out {
			outs = append(outs, fmt.Sprintf("%s %s/%dh", o.Address, coinsText(o.Coins), o.Hours))
		}
		log.Printf("INJECT OK txid=%s inputs=%d fee=%dh outputs=[%s] raw=%s", txn.Hash().Hex(), len(in), f, strings.Join(outs, "; "), hex.EncodeToString([]byte{})+body.Rawtx[:16])
		writeJSON(w, txn.Hash().Hex())
	})
	http.HandleFunc("/bench/reset", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reset()
		writeJSON(w, "reset")
	})
	// /bench/pending puts two unconfirmed transactions in the pool: one paying the phrase's
	// first address, one between strangers.
	http.HandleFunc("/bench/pending", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		stranger := cipher.MustAddressFromSecKey(cipher.MustGenerateDeterministicKeyPairs([]byte("stranger"), 1)[0])
		other := cipher.MustAddressFromSecKey(cipher.MustGenerateDeterministicKeyPairs([]byte("other"), 1)[0])
		received := time.Now().UTC().Format(time.RFC3339)
		txn := func(name string, to cipher.Address, coins uint64) map[string]any {
			id := cipher.SumSHA256([]byte(name)).Hex()
			return map[string]any{"received": received, "transaction": map[string]any{
				"txid": id, "inner_hash": id, "timestamp": headTime, "fee": 1,
				"inputs":  []map[string]any{{"uxid": cipher.SumSHA256([]byte(name + " in")).Hex(), "owner": stranger.String(), "coins": coinsText(coins), "hours": 10, "calculated_hours": 10}},
				"outputs": []map[string]any{{"uxid": cipher.SumSHA256([]byte(name + " out")).Hex(), "dst": to.String(), "coins": coinsText(coins), "hours": 5}},
			}}
		}
		pending = []map[string]any{txn("pending mine", addrs[0], 1_500_000), txn("pending other", other, 7_000_000)}
		writeJSON(w, len(pending))
	})
	// /bench/node?syncing=1&connections=0 makes the node report itself behind, or alone.
	http.HandleFunc("/bench/node", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		if v := q.Get("syncing"); v != "" {
			syncing = v == "1"
		}
		if v := q.Get("connections"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			connections = n
		}
		writeJSON(w, map[string]any{"syncing": syncing, "connections": connections})
	})
	http.HandleFunc("/bench/last", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writeJSON(w, last)
	})
	log.Printf("fake node on %s", *addr)
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
