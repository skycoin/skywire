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

func init() {
    RootCmd.AddCommand(
        testCmd,
    )
    testCmd.Flags().SortFlags = false
    testCmd.Flags().StringVarP(&logLvl, "loglvl", "s", "info", "[ debug | warn | error | fatal | panic | trace ] \u001b[0m*")
    testCmd.Flags().StringVarP(&pubkey, "pk", "k", pubkey, "verify services in survey for pubkey")
    testCmd.Flags().StringVarP(&hwSurveyPath, "lpath", "p", "log_collecting", "path to the surveys")
    testCmd.Flags().StringVarP(&svcConfig, "svcconf", "f", "/opt/skywire/services-config.json", "path to the services-config.json")
    testCmd.Flags().StringVarP(&dmsghttpConfig, "dmsghttpconf", "g", "/opt/skywire/dmsghttp-config.json", "path to the dmsghttp-config.json")
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

        checkFileExists(hwSurveyPath)
        checkFileExists(svcConfig)
        checkFileExists(dmsghttpConfig)

        if pubkey == "" {
            log.Fatal("must specify public key\n")
        }

        nodeInfo := fmt.Sprintf("%s/%s/node-info.json", hwSurveyPath, pubkey)
        checkFileExists(nodeInfo)

        nodeInfoData, err := script.File(nodeInfo).JQ(`.services`).Bytes()
        if err != nil {
            log.Fatal("error on script.File(", nodeInfo, ").JQ(`.services`):\n", err)
        }

        svcConfigData, err := script.File(svcConfig).JQ(`.prod`).Bytes()
        if err != nil {
            log.Fatal("error on script.File(", svcConfig, ").JQ(`.prod`):\n", err)
        }

        dmsghttpConfigData, err := script.File(dmsghttpConfig).JQ(`.prod`).Bytes()
        if err != nil {
            log.Fatal("error on script.File(", dmsghttpConfig, ").JQ(`.prod`):\n", err)
        }

        // Pretty print the original files for reference
        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(nodeInfoData), nil))
        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(svcConfigData), nil))
        fmt.Printf("%s\n", pretty.Color(pretty.Pretty(dmsghttpConfigData), nil))

        // Compare the services from node-info.json with services-config.json
        compareAndPrintDiffs(nodeInfoData, svcConfigData)
    },
}

func checkFileExists(path string) {
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

func compareMaps(map1, map2 map[string]interface{}) {
    for key, value1 := range map1 {
        if value2, ok := map2[key]; ok {
            if !reflect.DeepEqual(value1, value2) {
                printDifference(key, value1, value2)
            } else {
                printSame(key, value1)
            }
        } else {
            printMissingInSecondMap(key, value1)
        }
    }

    for key, value2 := range map2 {
        if _, ok := map1[key]; !ok {
            printMissingInFirstMap(key, value2)
        }
    }
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

func printSame(key string, value interface{}) {
    fmt.Printf("%s: %s\n", key, toJSON(value))
}

func printMissingInSecondMap(key string, value1 interface{}) {
    red := color.New(color.FgRed).SprintFunc()
    fmt.Printf("%s: %s (missing in second map)\n", key, red(toJSON(value1)))
}

func printMissingInFirstMap(key string, value2 interface{}) {
    red := color.New(color.FgRed).SprintFunc()
    fmt.Printf("%s: %s (missing in first map)\n", key, red(toJSON(value2)))
}
