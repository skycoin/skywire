// Package evalorder checks the repository for return statements whose
// result depends on an evaluation order the Go spec leaves open.
package evalorder

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestReturnEvalOrder rejects `return x, f(&x)` and `return x, f(func() { x = ... })`.
// The order between reading x and calling f is unspecified
// (https://go.dev/ref/spec#Order_of_evaluation). gc reads x after the call
// and TinyGo before it, so under TinyGo such a function returns the zero x.
func TestReturnEvalOrder(t *testing.T) {
	fset := token.NewFileSet()
	for _, dir := range []string{"pkg", "cmd", "internal"} {
		root := filepath.Join("..", "..", dir)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if n := d.Name(); n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				ret, ok := n.(*ast.ReturnStmt)
				if !ok || len(ret.Results) < 2 {
					return true
				}
				for i, res := range ret.Results {
					id, ok := res.(*ast.Ident)
					if !ok || id.Name == "_" || id.Name == "nil" {
						continue
					}
					for j, other := range ret.Results {
						if i != j && writesInCall(other, id.Name) {
							t.Errorf("%s: return reads %s while a call in the same return writes it", fset.Position(ret.Pos()), id.Name)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// writesInCall reports whether a call in e takes &name or runs a closure
// that assigns name.
func writesInCall(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.UnaryExpr:
					if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && id.Name == name {
						found = true
					}
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
							found = true
						}
					}
				}
				return !found
			})
		}
		return !found
	})
	return found
}
