// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/ipclass.go c5-reward-server
//
// The reward system's IP-class feed (ipclass/all): per visor, a keyed hash of
// the IP its survey reports. TPD reads it to leave transports between visors
// on one IP out of the per-visor bandwidth that pool 2 is paid from, without
// learning any IP (see pkg/deployment/tpd/store/visorbw.go).
//
// The hash key is derived from this server's secret key, so it is stable
// across restarts and known to nobody else. The feed is published only under
// the deployment's reward-system key: classes from a throwaway key would be
// meaningless, and TPD subscribes to that key alone.
package clirewardsserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ipClassInterval is how often the surveys are re-read. Surveys change as
// visors come and go; TPD reads the classes once per settled day.
const ipClassInterval = 15 * time.Minute

// ipClassKey derives the class hash key from the server's secret key.
func ipClassKey(sk cipher.SecKey) []byte {
	sum := sha256.Sum256(append([]byte("skywire-reward-ipclass/v1\x00"), sk[:]...))
	return sum[:]
}

// readIPClasses reads every survey under surveyDir (<pk>/node-info.json) and
// classes each visor by the IP it reports. A survey without an IP is skipped.
func readIPClasses(surveyDir string, key []byte) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(surveyDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pk := cipher.PubKey{}
		if pk.Set(e.Name()) != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(surveyDir, e.Name(), "node-info.json")) //nolint:gosec
		if err != nil {
			continue
		}
		var s struct {
			IPAddr string `json:"ip_address"`
		}
		if json.Unmarshal(b, &s) != nil || s.IPAddr == "" {
			continue
		}
		out[pk.Hex()] = tpdstore.IPClass(key, s.IPAddr)
	}
	return out
}

// startIPClassPublisher publishes the IP classes of the surveys in surveyDir,
// re-reading them every ipClassInterval, until ctx ends. It does nothing when
// sk is not the deployment's reward-system key.
func startIPClassPublisher(ctx context.Context, dmsgC *dmsg.Client, sk cipher.SecKey, surveyDir string) {
	log := logging.MustGetLogger("reward-ipclass-pub")
	pk, err := sk.PubKey()
	want := cmdutil.PKFromDmsgURL(deployment.Prod.RewardSystem)
	if err != nil || pk != want {
		log.WithField("pk", pk).WithField("reward_system", want).
			Warn("Not the reward system's key; not publishing IP classes, so TPD's per-visor bandwidth cannot leave out same-IP transports")
		return
	}
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:     log,
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgRewardIPClassCXOPort,
	})
	if err != nil {
		log.WithError(err).Error("IP-class publisher failed to start")
		return
	}
	// Only TPD reads the classes.
	var allow []cipher.PubKey
	for _, u := range []string{deployment.Prod.TransportDiscoveryDmsg, deployment.Prod.TransportDiscovery} {
		if tpd := cmdutil.PKFromDmsgURL(u); tpd != (cipher.PubKey{}) {
			allow = append(allow, tpd)
		}
	}
	pub.SetAllowlist(allow)
	log.WithField("feed_pk", pub.Feed()).WithField("dmsg_port", skyenv.DmsgRewardIPClassCXOPort).
		WithField("readers", allow).Info("Publishing IP classes")

	go func() {
		defer pub.Close() //nolint:errcheck
		key := ipClassKey(sk)
		var last map[string]string
		t := time.NewTicker(ipClassInterval)
		defer t.Stop()
		for {
			classes := readIPClasses(surveyDir, key)
			if len(classes) > 0 && !reflect.DeepEqual(classes, last) {
				body, err := json.Marshal(tpdstore.IPClasses{Version: 1, GeneratedAt: time.Now().UTC(), Classes: classes})
				if err == nil {
					err = pub.PutBatch([]treestore.PutOp{{Path: tpdstore.IPClassPath, Value: cxoutils.Gzip(body)}})
				}
				if err != nil {
					log.WithError(err).Warn("IP-class publish failed; retrying next interval")
				} else {
					last = classes
					log.WithField("visors", len(classes)).Info("Published IP classes")
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
