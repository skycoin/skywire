// Package clilog cmd/skywire-cli/commands/log/st.go
package clilog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(stCmd)
	stCmd.Flags().StringVarP(&pubKey, "pk", "p", "", "public key to check")
	stCmd.Flags().StringVarP(&lcDir, "lcdir", "d", "", "path to surveys & transport bandwidth logging ")
	stCmd.Flags().StringVarP(&tpsnDir, "tpsndir", "e", "", "path to transport setup-node surveys")
}

var stCmd = &cobra.Command{
	Use:   "st",
	Short: "survey tree",
	Run: func(_ *cobra.Command, _ []string) {
		makeTree(lcDir)
	},
}

func makeTree(dir string) {
	var tree pterm.TreeNode
	rootDir := lcDir + "/" + pubKey
	otherDir := tpsnDir + "/" + pubKey
	if pubKey == "" {
		dn, _ := script.ListFiles(rootDir).String()
		dn1, _ := script.ListFiles(otherDir).String()
		dirNodes, _ := script.Echo(dn + dn1).Basename().Freq().Column(2).Slice()
		nodes1 := []pterm.TreeNode{}
		for _, dirNode := range dirNodes {
			children, _ := script.ListFiles(rootDir + "/" + dirNode).String()
			children1, _ := script.ListFiles(otherDir + "/" + dirNode).String()
			kids, _ := script.Echo(children + children1).Slice()
			nodes := []pterm.TreeNode{}
			for _, kid := range kids {
				if filepath.Base(kid) == "health.json" || filepath.Base(kid) == "tp.json" {
					fileContents, _ := script.File(kid).String() //nolint
					fileInfo, _ := os.Stat(kid)                  //nolint
					var coloredFile string
					if time.Since(fileInfo.ModTime()) < time.Hour {
						coloredFile = pterm.Green(filepath.Base(kid))
					} else {
						coloredFile = pterm.Red(filepath.Base(kid))
					}
					if filepath.Base(kid) == "health.json" {
						nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s     Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()).Truncate(time.Second).String(), strings.TrimSuffix(string(fileContents), "\n"))})
					}
					if filepath.Base(kid) == "tp.json" {
						nodes = append(nodes, pterm.TreeNode{Text: fmt.Sprintf("%s         Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()).Truncate(time.Second).String(), strings.TrimSuffix(string(fileContents), "\n"))})
					}
				} else if filepath.Base(kid) == "node-info.json" {
					nodes = append(nodes, pterm.TreeNode{Text: pterm.Blue(filepath.Base(kid))})
				} else {
					nodes = append(nodes, pterm.TreeNode{Text: pterm.Yellow(filepath.Base(kid))})
				}
			}
			nodes1 = append(nodes1, pterm.TreeNode{Text: pterm.Cyan(dirNode), Children: nodes})
		}
		tree = pterm.TreeNode{Text: pterm.Cyan("Index"), Children: nodes1}
	} else {
		children := getDirChildren(rootDir)
		children = append(children, getDirChildren(otherDir)...)
		tree = pterm.TreeNode{
			Text:     pterm.Cyan(pubKey),
			Children: children,
		}
	}
	pterm.DefaultTree.WithRoot(tree).Render() //nolint
}

func getDirNodes(dirPath string) []pterm.TreeNode {
	nodes := []pterm.TreeNode{}

	files, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Printf("Not found\n")
		return nodes
	}

	for _, file := range files {
		if file.IsDir() {
			dirName := file.Name()
			nodes = append(nodes, pterm.TreeNode{
				Text:     pterm.Cyan(dirName),
				Children: getDirChildren(filepath.Join(dirPath, dirName)),
			})
		}
	}

	return nodes
}

func getDirChildren(dirPath string) []pterm.TreeNode {
	nodes := []pterm.TreeNode{}

	files, err := os.ReadDir(dirPath)
	if err != nil {
		fmt.Printf("Not found\n")
		return nodes
	}

	for _, file := range files {
		fileName := file.Name()
		if file.IsDir() {
			nodes = append(nodes, pterm.TreeNode{
				Text:     pterm.Cyan(fileName),
				Children: getDirChildren(filepath.Join(dirPath, fileName)),
			})
		} else if fileName == "health.json" || fileName == "tp.json" {
			fileContents, err := os.ReadFile(filepath.Join(dirPath, fileName)) //nolint
			if err != nil {
				fmt.Printf("Error reading file\n")
				continue
			}
			// Get file information
			fileInfo, err := os.Stat(filepath.Join(dirPath, fileName)) //nolint
			if err != nil {
				fmt.Printf("Error stat-ing file\n")
				continue
			}
			var coloredFile string
			if time.Since(fileInfo.ModTime()) < time.Hour {
				coloredFile = pterm.Green(fileName)
			} else {
				coloredFile = pterm.Red(fileName)
			}
			nodes = append(nodes, pterm.TreeNode{
				Text: fmt.Sprintf("%s Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()), strings.TrimSuffix(string(fileContents), "\n")),
			})
		} else if fileName == "node-info.json" {
			nodes = append(nodes, pterm.TreeNode{
				Text: pterm.Blue(fileName),
			})
		} else {
			nodes = append(nodes, pterm.TreeNode{
				Text: pterm.Yellow(fileName),
			})
		}
	}

	return nodes
}
