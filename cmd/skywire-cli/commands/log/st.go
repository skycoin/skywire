// Package clilog cmd/skywire-cli/commands/log/st.go
package clilog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(stCmd)
	stCmd.Flags().StringVarP(&pubKey, "pk", "p", "", "public key to check")
	stCmd.Flags().StringVarP(&lcDir, "dir", "d", "", "path to surveys & transport bandwidth logging ")
}

var stCmd = &cobra.Command{
	Use:   "st",
	Short: "survey tree",
	Run: func(_ *cobra.Command, _ []string) {
		makeTree(lcDir)
	},
}

func init() {
	RootCmd.AddCommand(tpsnCmd)
	tpsnCmd.Flags().StringVarP(&pubKey, "pk", "p", "", "public key to check")
	tpsnCmd.Flags().StringVarP(&tpsnDir, "dir", "d", "", "path to surveys & transport bandwidth logging ")
}

var tpsnCmd = &cobra.Command{
	Use:   "tpsn",
	Short: "transport setup survey tree",
	Run: func(_ *cobra.Command, _ []string) {
		makeTree(tpsnDir)
	},
}

func makeTree(dir string) {
	var tree pterm.TreeNode
	rootDir := dir + "/" + pubKey
	if pubKey == "" {
		tree = pterm.TreeNode{
			Text:     "Index",
			Children: getDirNodes(rootDir),
		}
	} else {
		tree = pterm.TreeNode{
			Text:     pterm.Cyan(pubKey),
			Children: getDirChildren(rootDir),
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
				Text: fmt.Sprintf("%s Age: %s %s", coloredFile, time.Since(fileInfo.ModTime()), string(fileContents)),
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
