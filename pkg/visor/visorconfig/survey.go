package visorconfig

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProtonMail/gopenpgp/v2/helper"
	coincipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/util/pathutil"
)

// GenerateSurvey generate survey handler
func GenerateSurvey(conf *V1, log *logging.Logger, routine, rawSurvey bool) {
	if IsRoot() {
		for {
			//check for valid reward address set as prerequisite for generating the system survey
			rewardAddressBytes, err := os.ReadFile(PackageConfig().LocalPath + "/" + RewardFile) //nolint
			if err == nil {
				//remove any newline from rewardAddress string
				rewardAddress := strings.TrimSuffix(string(rewardAddressBytes), "\n")
				//validate the skycoin address
				cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
				if err != nil {
					log.WithError(err).Error("Invalid skycoin reward address.")
					return
				}
				log.Info("Skycoin reward address: ", cAddr.String())
				//generate the system survey
				pathutil.EnsureDir(conf.LocalPath) //nolint
				survey, err := SystemSurvey()
				if err != nil {
					log.WithError(err).Error("Could not read system info.")
					return
				}
				survey.PubKey = conf.PK
				survey.SkycoinAddress = cAddr.String()
				// Print results.
				s, err := json.MarshalIndent(survey, "", "\t")
				if err != nil {
					log.WithError(err).Error("Could not marshal json.")
					return
				}

				if rawSurvey {
					err = os.WriteFile(conf.LocalPath+"/"+NodeInfo, []byte(s), 0644) //nolint
					if err != nil {
						log.WithError(err).Error("Failed to write system hardware survey to file.")
						return
					}
				} else {
					skycoinKeyPath := SkywirePath + "/" + SkycoinKeyName
					skycoinKey, err := os.ReadFile(skycoinKeyPath)
					if err != nil {
						log.WithError(err).Error("Could not find skycoin key.")
					}
					skycoinKeyString := string(skycoinKey)
					encryptedNodeInfo, err := helper.EncryptBinaryMessageArmored(skycoinKeyString, s)
					if err != nil {
						log.WithError(err).Error("Could not encrypt survey.")
					}

					err = os.WriteFile(conf.LocalPath+"/"+NodeInfo, []byte(encryptedNodeInfo), 0644) //nolint
					if err != nil {
						log.WithError(err).Error("Failed to write system hardware survey to file.")
						return
					}
				}
				log.Info("Generating system survey")
				f, err := os.ReadFile(filepath.Clean(conf.LocalPath + "/" + NodeInfo))
				if err != nil {
					log.WithError(err).Error("Failed to write system hardware survey to file.")
					return
				}
				srvySha256Byte32 := sha256.Sum256([]byte(f))
				err = os.WriteFile(conf.LocalPath+"/"+NodeInfoSha256, srvySha256Byte32[:], 0644) //nolint
				if err != nil {
					log.WithError(err).Error("Failed to write system hardware survey to file.")
					return
				}
			} else {
				err := os.Remove(PackageConfig().LocalPath + "/" + NodeInfo)
				if err == nil {
					log.Debug("Removed hadware survey for visor not seeking rewards")
				}
				err = os.Remove(PackageConfig().LocalPath + "/" + NodeInfoSha256)
				if err == nil {
					log.Debug("Removed hadware survey checksum file")
				}
			}
			// break loop for generate each 24hours if just reward address chenged
			if !routine {
				break
			}
			time.Sleep(24 * time.Hour)
		}
	}
}
