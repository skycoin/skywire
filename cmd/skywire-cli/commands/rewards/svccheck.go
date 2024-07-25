package clirewards

import (
    "encoding/json"
    "fmt"
    "os"
    "reflect"

		"github.com/bitfield/script"
    "github.com/tidwall/pretty"
    "github.com/spf13/cobra"
    "github.com/fatih/color"

    "github.com/skycoin/skywire-utilities/pkg/logging"
    "github.com/skycoin/skywire-utilities/pkg/cipher"
)

var (
  misconfigured bool
  nodeInfo []byte
  sConf []byte
  dConf []byte
  sConfig string
  dConfig string
)

func init() {
    RootCmd.AddCommand(
        testCmd,
    )
    testCmd.Flags().SortFlags = false
    testCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ] \u001b[0m*")
    testCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "verify services in survey for pubkey")
    testCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
    testCmd.Flags().StringVarP(&sConfig, "svcconf", "f", "/opt/skywire/services-config.json", "path to the services-config.json")
    testCmd.Flags().StringVarP(&dConfig, "dmsghttpconf", "g", "/opt/skywire/dmsghttp-config.json", "path to the dmsghttp-config.json")
}

var testCmd = &cobra.Command{
    Use:   "svc",
    Short: "verify services in survey",
    Run: func(cmd *cobra.Command, args []string) {
        var err error
        if log == nil {
            log = logging.MustGetLogger("rewards")
        }
        if logLvl != "" {
            if lvl, err := logging.LevelFromString(logLvl); err == nil {
                logging.SetLevel(lvl)
            }
        }

        var pk1 cipher.PubKey
        err = pk1.Set(pubkey)
        if err != nil {
            log.Fatal("invalid public key\n", err)
        }

        mustExist(hwSurveyPath)
        mustExist(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey))
        mustExist(sConfig)
        mustExist(dConfig)

        //stun_servers does not currently match between conf.skywire.skycoin.com & https://github.com/skycoin/skywire/blob/develop/services-config.json ; omit checking them until next version
        nodeInfo, err = script.File(fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey)).JQ(`.services | del(.stun_servers)`).Bytes()
        if err != nil {
            log.Fatal("error parsing json with jq:\n", err)
        }

        sConf, err := script.File(sConfig).JQ(`.prod  | del(.stun_servers)`).Bytes()
        if err != nil {
          log.Fatal("error parsing json with jq:\n", err)
        }

//        dConf, err := script.File(dConfig).JQ(`.prod`).Bytes()
//        if err != nil {
//            log.Fatal("error parsing json with jq:\n", err)
//        }

        // Pretty print the original files for reference
//        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfo), nil))
//        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(sConf), nil))
//        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(dConf), nil))

        compareAndPrintDiffs(nodeInfo, sConf)
    },
}

func mustExist(path string) {
    _, err := os.Stat(path)
    if os.IsNotExist(err) {
        log.Fatal("the path to the file does not exist: ", path, "\n", err)
    }
    if err != nil {
        log.Fatal("error on os.Stat(", path, "):\n", err)
    }
}

func compareAndPrintDiffs(nodeInfoData, svcConfigData []byte) {
    var nodeInfoServices map[string]interface{}
    var svcConfigServices map[string]interface{}

    if err := json.Unmarshal(nodeInfoData, &nodeInfoServices); err != nil {
        log.Fatal("error unmarshalling nodeInfoData: ", err)
    }
    if err := json.Unmarshal(svcConfigData, &svcConfigServices); err != nil {
        log.Fatal("error unmarshalling svcConfigData: ", err)
    }

    compareMaps(nodeInfoServices, svcConfigServices)
}

func compareMaps(nodeInfoServices, svcConfigServices map[string]interface{}) {
    for key, value1 := range nodeInfoServices {
        if value2, ok := svcConfigServices[key]; ok {
            if reflect.TypeOf(value1).Kind() == reflect.Slice && reflect.TypeOf(value2).Kind() == reflect.Slice {
                slice1 := value1.([]interface{})
                slice2 := value2.([]interface{})
                if !sliceContains(slice1, slice2) {
                    printDifference(key, value1, value2)
                    misconfigured = true
                }
            } else if !reflect.DeepEqual(value1, value2) {
                printDifference(key, value1, value2)
                misconfigured = true
            }
        }
    }
    if !misconfigured {
        log.Info("services are configured correctly")
        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfo), nil))
        return
    }
}

func sliceContains(slice1, slice2 []interface{}) bool {
    for _, v2 := range slice2 {
        found := false
        for _, v1 := range slice1 {
            if reflect.DeepEqual(v1, v2) {
                found = true
                break
            }
        }
        if !found {
            return false
        }
    }
    return true
}


func toJSON(value interface{}) string {
    jsonData, err := json.MarshalIndent(value, "", "  ")
    if err != nil {
        return fmt.Sprintf("%v", value)
    }
    return string(jsonData)
}

func printDifference(key string, value1, value2 interface{}) {
    red := color.New(color.FgRed).SprintFunc()
    fmt.Printf("%s: %s != %s\n", key, red(toJSON(value1)), red(toJSON(value2)))
}
