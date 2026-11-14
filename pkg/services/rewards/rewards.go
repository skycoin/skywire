//go:build !(js && wasm) && !tinygo

// Package rewards pkg/services/rewards/rewards.go c3-vis-appsvc
//
// The reward system as a pkg/services.Service: the reward UI server
// (`skywire cli rewards ui`) with the hourly reward cycle inside it
// (`skywire cli rewards serve`), so a visor can host it under its own key.
//
// Block shape:
//
//	{
//	  "type": "rewards", "name": "rewards", "keyfile": "/path/rewards.key",
//	  "work_dir": "/var/lib/skywire/rewards",
//	  "skycoin_node": "http://127.0.0.1:6420",
//	  "proxy": "127.0.0.1:4443", "fetch": false,
//	  "bot_token_file": "/etc/skywire/rewardbot.token", "bot_chat_id": -100123
//	}
//
// work_dir holds hist/, log_backups/ and xpub_index.json. The bot token is
// read from bot_token_file or TG_BOT_TOKEN, never from the block, and the chat
// from bot_chat_id or TG_CHAT_ID.
package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	rewardsbot "github.com/skycoin/skywire/pkg/deployment/rewards/bot"
	rewardsserver "github.com/skycoin/skywire/pkg/deployment/rewards/server"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

// Type is the registry key used in services.json blocks.
const Type = "rewards"

func init() {
	services.Register(Type, factory)
}

// Config is the JSON shape of a rewards block.
type Config struct {
	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`
	// Addr is an optional plain-HTTP listen address. Empty serves dmsg only.
	Addr     string `json:"addr,omitempty"`
	DmsgPort uint16 `json:"dmsg_port,omitempty"`
	WorkDir  string `json:"work_dir"`
	// Whitelist are the keys allowed to post a reward transaction.
	Whitelist        []cipher.PubKey `json:"whitelist,omitempty"`
	SkycoinNode      string          `json:"skycoin_node,omitempty"`
	LoginNode        string          `json:"login_node,omitempty"`
	CanonicalDomain  string          `json:"canonical_domain,omitempty"`
	SurveyMinVersion string          `json:"survey_min_version,omitempty"`

	// The cycle: hourly unless cycle_interval says otherwise, none with no_cycle.
	NoCycle       bool              `json:"no_cycle,omitempty"`
	CycleInterval services.Duration `json:"cycle_interval,omitempty"`
	SkipCollect   bool              `json:"skip_collect,omitempty"`
	Fetch         bool              `json:"fetch,omitempty"`
	Proxy         string            `json:"proxy,omitempty"`
	MinVersion    string            `json:"min_version,omitempty"`
	PruneBelow    string            `json:"prune_below_version,omitempty"`

	BotTokenFile string `json:"bot_token_file,omitempty"`
	BotChatID    int64  `json:"bot_chat_id,omitempty"`
}

type service struct {
	cfg Config
	log *logging.Logger

	mu    sync.Mutex
	sched *rewardsserver.Scheduler
	bot   bool
}

func factory(raw json.RawMessage, log *logging.Logger) (services.Service, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("rewards: parse block: %w", err)
	}
	if c.WorkDir == "" {
		return nil, errors.New("rewards: work_dir is required")
	}
	if c.SecKey.Null() {
		return nil, errors.New("rewards: secret_key or keyfile is required")
	}
	if log == nil {
		log = logging.MustGetLogger(Type)
	}
	return &service{cfg: c, log: log}, nil
}

// Run serves the reward system and runs its cycle until ctx ends.
func (s *service) Run(ctx context.Context) error {
	c := s.cfg
	if fi, err := os.Stat(c.WorkDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("rewards: work_dir %s is not a directory", c.WorkDir)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !c.NoCycle {
		if rewardsserver.NewCycle == nil {
			return errors.New("rewards: this binary has no reward cycle")
		}
		sched := &rewardsserver.Scheduler{
			Cycle: rewardsserver.NewCycle(rewardsserver.CycleConfig{
				WorkDir:     c.WorkDir,
				SkycoinNode: c.SkycoinNode,
				SkipCollect: c.SkipCollect,
				Fetch:       c.Fetch,
				Proxy:       c.Proxy,
				MinVersion:  c.MinVersion,
				PruneBelow:  c.PruneBelow,
			}),
			Interval: time.Duration(c.CycleInterval),
			Log:      s.log,
		}
		s.mu.Lock()
		s.sched = sched
		s.mu.Unlock()
		rewardsserver.SetScheduler(sched)
		defer rewardsserver.SetScheduler(nil)
		go sched.Start(ctx)
	}

	if token, chatID, err := s.botCredentials(); err != nil {
		s.log.WithError(err).Info("Telegram bot off")
	} else {
		s.mu.Lock()
		s.bot = true
		s.mu.Unlock()
		rewardsbot.Start(ctx, token, chatID, filepath.Join(c.WorkDir, "transactions0.txt"), s.log)
	}

	return rewardsserver.Serve(ctx, rewardsserver.RewardConfig{
		WorkDir:          c.WorkDir,
		WhitelistPKs:     c.Whitelist,
		CanonicalDomain:  c.CanonicalDomain,
		SkycoinNode:      c.SkycoinNode,
		LoginNode:        c.LoginNode,
		DisableTpVizAPI:  true,
		SecKey:           c.SecKey,
		DmsgPort:         c.DmsgPort,
		SurveyMinVersion: c.SurveyMinVersion,
	}, c.Addr, s.log)
}

// botCredentials reads the bot token from bot_token_file or TG_BOT_TOKEN,
// and the chat from bot_chat_id or TG_CHAT_ID.
func (s *service) botCredentials() (string, int64, error) {
	token := os.Getenv("TG_BOT_TOKEN")
	if f := s.cfg.BotTokenFile; f != "" {
		b, err := os.ReadFile(f) //nolint:gosec // the operator names the file
		if err != nil {
			return "", 0, fmt.Errorf("bot_token_file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return "", 0, errors.New("no bot token (bot_token_file or TG_BOT_TOKEN)")
	}
	chatID := s.cfg.BotChatID
	if chatID == 0 {
		id, err := strconv.ParseInt(os.Getenv("TG_CHAT_ID"), 10, 64)
		if err != nil {
			return "", 0, errors.New("no bot chat (bot_chat_id or TG_CHAT_ID)")
		}
		chatID = id
	}
	return token, chatID, nil
}

// detail is the service's part of `visor state`.
type detail struct {
	WorkDir     string                     `json:"work_dir"`
	SkycoinNode string                     `json:"skycoin_node,omitempty"`
	Bot         bool                       `json:"bot"`
	Cycle       *rewardsserver.CycleStatus `json:"reward_cycle,omitempty"`
}

// State implements services.Stater.
func (s *service) State() services.State {
	s.mu.Lock()
	d := detail{WorkDir: s.cfg.WorkDir, SkycoinNode: s.cfg.SkycoinNode, Bot: s.bot}
	sched := s.sched
	s.mu.Unlock()
	if sched != nil {
		st := sched.Status()
		d.Cycle = &st
	}
	raw, _ := json.Marshal(d) //nolint:errcheck
	return services.State{Store: "files", Detail: raw}
}
