// Package ui2 pkg/visor/ui2/rewards.go c3-vis-core
package ui2

import (
	"encoding/json"
	"time"
)

// Rewards is the reward address part of the rewards tab.
type Rewards struct {
	PK      string
	Address string
	IsXpub  bool
	// Addresses are the first the xpub derives, where its rewards go.
	Addresses []string
	XpubErr   string
	// Spent: the address has sent coins, so its public key is on chain.
	Spent   bool
	Balance string
	Err     string
	Editing bool
	// AcceptRisk keeps the single address box ticked when a save fails.
	AcceptRisk bool
	Draft      string
}

// RewardDay is one day of the reward system's history for a visor.
type RewardDay struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
	Share  float64 `json:"share"`
	Sent   bool    `json:"sent"`
	TxID   string  `json:"txid,omitempty"`
}

// RewardHistory is the history part of the rewards tab.
type RewardHistory struct {
	PK    string
	Days  int
	List  []RewardDay
	Total float64
	Err   string
}

// ParseRewardHistory reads the reward system's visor history answer.
func ParseRewardHistory(body []byte) ([]RewardDay, float64, error) {
	var resp struct {
		History []RewardDay `json:"history"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, err
	}
	total := 0.0
	for _, d := range resp.History {
		total += d.Amount
	}
	return resp.History, total, nil
}

// Status is a day's state as the Angular tab classes it.
func (d RewardDay) Status() string {
	switch {
	case d.Amount == 0:
		return "no-reward"
	case d.Sent:
		return "sent"
	}
	return "pending"
}

// DateLabel prints the day like "Nov 3, 2026".
func (d RewardDay) DateLabel() string {
	t, err := time.Parse(time.DateOnly, d.Date)
	if err != nil {
		return d.Date
	}
	return t.Format("Jan 2, 2006")
}

// ShortTx is the start of a transaction id.
func ShortTx(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
