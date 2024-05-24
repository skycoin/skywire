// Package clilog cmd/skywire-cli/commands/log/st.go
package clilog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

func init() {
	RootCmd.AddCommand(stCmd)
	stCmd.Flags().StringVarP(&pubKey, "pk", "p", "", "public key(s) to check ; comma separated")
	stCmd.Flags().StringVarP(&lcDir, "lcdir", "d", "", "path to surveys & transport bandwidth logging ")
	stCmd.Flags().StringVarP(&tpsnDir, "tpsndir", "e", "", "path to transport setup-node surveys")
	stCmd.Flags().StringVarP(&proxyCSV, "proxycsv", "x", "", "path to proxy test csv")
	stCmd.Flags().BoolVarP(&hideErr, "noerr", "r", false, "hide error logging from output")
	stCmd.Flags().BoolVarP(&showUT, "ut", "u", false, "show uptime percentage for the past two days and current online status")
}

var stCmd = &cobra.Command{
	Use:   "st",
	Short: "survey tree",
	Run: func(_ *cobra.Command, _ []string) {
		if pubKey != "" {
			pks := strings.Split(pubKey, ",")
			for _, pk := range pks {
				var pK cipher.PubKey
				err := pK.Set(pk)
				if err != nil {
					log.Fatal(err)
				}
				pubKeys = append(pubKeys, pK)
			}
		}
		makeTree()
	},
}

func makeTree() {
	utFileInfo, utFileInfoErr := os.Stat("/tmp/ut.json")
	utData, utDataErr := script.File("/tmp/ut.json").String()
	var tree pterm.TreeNode
	rootDir := lcDir
	otherDir := tpsnDir
	dn, err := script.ListFiles(rootDir).String()
	if err != nil && !hideErr {
		errstring := "script.ListFiles(" + rootDir + ").String()"
		log.Printf("%v error: %v\n", errstring, err)
	}
	dn1, err := script.ListFiles(otherDir).String()
	if err != nil && !hideErr {
		log.Printf("script.ListFiles("+otherDir+").String() error: %v\n", err)
	}
	dirNodes, err := script.Echo(dn + dn1).Basename().Freq().Column(2).Slice()
	if err != nil && !hideErr {
		log.Printf("script.Echo(dn + dn1).Basename().Freq().Column(2).Slice() error: %v\n", err)
	}
	if len(pubKeys) > 0 {
		var checkTheseKeys string
		for _, k := range pubKeys {
			checkTheseKeys += k.String() + "\n"

		}
		dirNodes, err = script.Echo(strings.Join(dirNodes, "\n") + "\n" + checkTheseKeys).Freq().Match("2 ").Column(2).Slice()
		if err != nil && !hideErr {
			log.Printf("script.Echo(dirNodes+checkTheseKeys).Freq().Match(\"2 \").Column(2).Slice() error: %v\n", err)
		}
	}
	nodes1 := []pterm.TreeNode{}
	for _, dirNode := range dirNodes {
		children, err := script.ListFiles(rootDir + "/" + dirNode).String()
		if err != nil && !hideErr {
			errstring := fmt.Sprintf("script.ListFiles(\"%v\"/\"%v\").String()", rootDir, dirNode)
			log.Printf("%v error: %v\n", errstring, err)
		}
		children1, err := script.ListFiles(otherDir + "/" + dirNode).String()
		if err != nil && !hideErr {
			errstring := fmt.Sprintf("script.ListFiles(\"%v\"/\"%v\").String()", otherDir, dirNode)
			log.Printf("%v error: %v\n", errstring, err)
		}
		var ks string
		if showUT {
			ks = fmt.Sprintf("uptime\n%s\n%s", children, children1)
		} else {
			ks = fmt.Sprintf("%s\n%s", children, children1)
		}
		if proxyCSV != "" {
			ks = fmt.Sprintf("%s\nproxy", ks)
		}
		kids, err := script.Echo(ks).Slice()
		if err != nil && !hideErr {
			log.Printf("script.Echo(ks).Slice()  error: %v\n", err)
		}
		nodes := []pterm.TreeNode{}
		for _, kid := range kids {
			var coloredFile string
			if kid == "proxy" {
				testtime, _ := script.File(proxyCSV).Match(dirNode).Replace(",", " ").Column(2).String() //nolint
				testres, _ := script.File(proxyCSV).Match(dirNode).Replace(",", " ").Column(3).String()  //nolint
				if testtime != "" && testres != "" {
					coloredFile = fmt.Sprintf("%s           %s	%s", pterm.Green("proxy"), strings.ReplaceAll(testtime, "\n", ""), strings.ReplaceAll(testres, "\n", ""))
				} else {
					coloredFile = fmt.Sprintf("%s           No data", pterm.Red("proxy"))
				}
				nodes = append(nodes, pterm.TreeNode{Text: coloredFile})
				continue
			}
			if kid == "uptime" {
				if utFileInfoErr != nil || utDataErr != nil {
					continue
				}
				pkUt, err := script.Echo(utData).JQ(".[] | select(.pk == \"" + dirNode + "\")  | {on, daily: (.daily  | with_entries(select(.key == (\"" + time.Now().Format("2006-01-02") + "\", \"" + time.Now().AddDate(0, 0, -1).Format("2006-01-02") + "\"))))}").String()
				if err != nil && !hideErr {
					log.Printf(" script.Echo(utData).JQ(\".[] | select(.pk == \""+dirNode+"\") | .daily | with_entries(select(.key == (\""+time.Now().Format("2006-01-02")+"\", \""+time.Now().AddDate(0, 0, -1).Format("2006-01-02")+"\")))\").String()  error: %v\n", err)
				}
				pkOnline, err := script.Echo(pkUt).JQ(".on").String()
				if err != nil && !hideErr {
					log.Printf("script.Echo(pkUt).JQ(\".on\").String()   error: %v\n", err)
				}
				if pkOnline == "true" || pkOnline == "true\n" {
					coloredFile = pterm.Green("uptime")
				} else {
					coloredFile = pterm.Red("uptime")
				}
				nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s     Age: %s %s", coloredFile, time.Since(utFileInfo.ModTime()).Truncate(time.Second).String(), strings.TrimSuffix(pkUt, "\n"))})
				continue
			}
			if filepath.Base(kid) == "health.json" || filepath.Base(kid) == "tp.json" {
				fileContents, _ := script.File(kid).String() //nolint
				fileInfo, _ := os.Stat(kid)                  //nolint
				if time.Since(fileInfo.ModTime()) < time.Hour {
					coloredFile = pterm.Green(filepath.Base(kid))
				} else {
					coloredFile = pterm.Red(filepath.Base(kid))
				}
				if filepath.Base(kid) == "health.json" {
					nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s     Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()).Truncate(time.Second).String(), strings.TrimSuffix(string(fileContents), "\n"))})
				}
				if filepath.Base(kid) == "tp.json" {
					_, err := script.Echo(strings.TrimSuffix(string(fileContents), "\n")).JQ(".[]").String() //nolint
					if err != nil {
						fileContents = pterm.Red(strings.TrimSuffix(string(fileContents), "\n"))
						coloredFile = pterm.Red(filepath.Base(kid))
					}
					nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s         Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()).Truncate(time.Second).String(), strings.TrimSuffix(string(fileContents), "\n"))})
				}
				continue
			}
			if filepath.Base(kid) == "node-info.json" {
				coloredFile = pterm.Blue(filepath.Base(kid))
				ver, err := script.File(kid).JQ(".skywire_version").String()
				if err != nil {
					ver = pterm.Red(strings.TrimSuffix(string(ver), "\n"))
					coloredFile = pterm.Red(filepath.Base(kid))
					nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s          %s", coloredFile, strings.TrimSuffix(string(ver), "\n"))})
				} else {
					nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s          %s", coloredFile, strings.TrimSuffix(string(ver), "\n"))})
				}
				continue
			}
			if filepath.Base(kid) != "." {
				nodes = append(nodes, pterm.TreeNode{Text: pterm.Yellow(filepath.Base(kid))})
			}
		}
		nodes1 = append(nodes1, pterm.TreeNode{Text: pterm.Cyan(dirNode), Children: nodes})
	}
	tree = pterm.TreeNode{Text: pterm.Cyan("Index"), Children: nodes1}
	pterm.DefaultTree.WithRoot(tree).Render() //nolint
}
