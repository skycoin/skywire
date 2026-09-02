//go:build js && wasm

// Package putils cmd/skywire-cli/cliutil/putils/putils_js.go c5-cli-util
package putils

import "github.com/skycoin/skywire/cmd/skywire-cli/cliutil/pterm"

// TreeFromLeveledList converts a LeveledList into a nested TreeNode, matching
// putils.TreeFromLeveledList: the first item is expected at level 0 and
// becomes a child of the returned root; deeper levels nest under the nearest
// shallower predecessor (jumps clamp to parent+1).
func TreeFromLeveledList(items pterm.LeveledList) pterm.TreeNode {
	root := &pterm.TreeNode{}
	// stack[i] points at the last node emitted at level i.
	stack := []*pterm.TreeNode{root}
	for _, it := range items {
		lvl := it.Level
		if lvl < 0 {
			lvl = 0
		}
		if lvl > len(stack)-1 {
			lvl = len(stack) - 1
		}
		parent := stack[lvl]
		parent.Children = append(parent.Children, pterm.TreeNode{Text: it.Text})
		node := &parent.Children[len(parent.Children)-1]
		stack = append(stack[:lvl+1], node)
	}
	return *root
}
