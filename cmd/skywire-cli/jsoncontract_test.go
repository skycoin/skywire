package main

// The CLI's JSON output is an API. Callers pipe it into jq, the e2e suite
// unmarshals it, and other programs depend on the field names. This file is the
// guard on three ways that API used to rot silently.
//
// Each check names what it found and how to fix it, because the failure it
// prevents is not a crash — it is a command that quietly prints prose to a
// caller expecting JSON, or two declarations of one shape drifting apart. Both
// happened: `skywire cli tp` emitted a different document depending on which
// code path ran, because the shape was declared twice inside function bodies.
//
// Every check carries an allowlist of the cases that are legitimately exempt,
// each with a reason. Shrinking those lists is the work; the numbers they
// report are the progress.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// scanDirs are the trees searched for commands. pkg/flags is here because
// commands can be defined outside cmd/skywire-cli/commands and were invisible
// to these checks when they were: `tree` was added there and shipped ignoring
// --json, which is exactly what this file exists to prevent.
var scanDirs = []string{"commands", "../../pkg/flags"}

// streamingCommands print continuously, interactively, or as a byte stream, so
// there is no single document for --json to shape. Anything added here needs a
// reason that survives the question "could this have emitted a JSON summary
// instead?".
var streamingCommands = map[string]string{
	"commands/gotop/root.go":                   "a full-screen TUI; there is no document to emit",
	"commands/visor/top.go":                    "a full-screen TUI; there is no document to emit",
	"commands/visor/ping/mux_bandwidth_tui.go": "a live bandwidth TUI",
	"commands/proxy/mux_plot.go":               "a live per-leg bandwidth+RTT terminal chart; there is no document to emit",
	"commands/tp/tp-viz.go":                    "starts a visualizer HTTP server; it serves pages, it does not return a value",
	"commands/visor/ping/tree.go":              "an interactive Bubble Tea TUI over a live BFS walk",
	"commands/reward/rules.go":                 "prints the mainnet rules as markdown or rendered HTML — a document to read, not data",
	"commands/rewards/services.go":             "emits shell script bodies meant to be piped into sh or redirected to a file",
	"commands/rewards/calc.go":                 "writes its results to files; stdout is a human narrative of how the calculation went",
	"commands/jq/jq.go":                        "it IS jq; the caller's filter decides the shape",
	"commands/hv/shell.go":                     "drives a browser terminal over CDP; output is a live transcript",
	"commands/hv/eval.go":                      "prints whatever the page's JS returned; the caller chose the shape",
	"commands/hv/probe.go":                     "streams CDP events as they arrive, unbounded",
	"commands/dmsg/curl.go":                    "writes the fetched body, which is the point",
	"commands/dmsg/iperf.go":                   "a throughput meter; prints intervals as they elapse",
	"commands/dmsg/probe.go":                   "prints per-port progress while probing",
	"commands/got/got.go":                      "an HTTP client; writes the response body",
	"commands/got/got_skywire.go":              "as got.go",
	"commands/skychat/sendfile.go":             "progress for a file transfer",
	"commands/util/foreach.go":                 "relays whatever the inner command printed",
	"commands/log/log.go":                      "bulk log collection; per-file progress",
	"commands/rewards/server/server.go":        "an HTTP server, not a command that returns a value",
	"commands/rewards/server/login.go":         "part of the rewards HTTP server",
	"commands/rewards/server/logging.go":       "part of the rewards HTTP server",
	"commands/rewards/server/stats.go":         "part of the rewards HTTP server",
	"commands/rewards/server/htmpl.go":         "part of the rewards HTTP server",
	"commands/rewards/server/nodeproxy.go":     "part of the rewards HTTP server",
	"commands/rewards/server/loginchain.go":    "part of the rewards HTTP server",
}

// localJSONFlags are commands that declare their own --json instead of using
// the persistent one from the root. Two variables for one concept: a command
// reading its own package-level bool cannot see the root's flag, so behavior
// depends on where the user put the word.
// pendingJSON is debt, not exemption: each of these prints without
// consulting --json and should be converted to a typed output in
// pkg/cliout/<group>. The list only shrinks — report() fails on an entry
// that no longer applies, so a fix must delete its line.
var pendingJSON = map[string]string{}

var localJSONFlags = map[string]string{}

// funcLocalShapes are output structs declared inside a function body. They
// cannot be imported, so every other consumer — the e2e suite above all —
// retypes them by hand and the compiler never compares the copies.
var funcLocalShapes = map[string]string{}

// TestNoLocalJSONFlag fails on a command that registers its own --json.
func TestNoLocalJSONFlag(t *testing.T) {
	var found []string
	forEachFile(t, func(path string, file *ast.File, fset *token.FileSet) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "BoolVar") {
				return true
			}
			// BoolVar(&v, "name", ...) / BoolVarP(&v, "name", "n", ...)
			if len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			name, uerr := strconv.Unquote(lit.Value)
			if uerr == nil && name == "json" {
				found = append(found, path+":"+posLine(fset, call.Pos()))
			}
			return true
		})
	})
	report(t, found, localJSONFlags,
		"declares its own --json, shadowing the persistent flag on RootCmd",
		"delete the local flag and read the inherited one (cliout.JSONMode)")
}

// TestNoFunctionLocalOutputShape fails on a struct with json tags declared
// inside a function.
func TestNoFunctionLocalOutputShape(t *testing.T) {
	var found []string
	forEachFile(t, func(path string, file *ast.File, fset *token.FileSet) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// Only shapes that are actually PRINTED. A json-tagged struct
			// declared in a function is just as often the other direction —
			// tp.go decodes the uptime tracker and service discovery into
			// local types, and those describe someone else's API, not this
			// command's output. Moving them to pkg/cliout would be a
			// mislabelling, so the rule is "reaches the printer", not "has
			// json tags".
			printed := printedTypes(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil || !printed[ts.Name.Name] {
					return true
				}
				for _, f := range st.Fields.List {
					if f.Tag != nil && strings.Contains(f.Tag.Value, "json:") {
						found = append(found, path+":"+posLine(fset, ts.Pos())+" ("+ts.Name.Name+")")
						return false
					}
				}
				return true
			})
		}
	})
	report(t, found, funcLocalShapes,
		"declares a json-tagged struct inside a function, so nothing can import it",
		"move it to pkg/cliout/<group> and alias it here")
}

// TestEveryPrintingCommandHonoursJSON fails on a file that prints but never
// routes through the printer, so --json is silently ignored.
func TestEveryPrintingCommandHonoursJSON(t *testing.T) {
	var found []string
	forEachFile(t, func(path string, file *ast.File, fset *token.FileSet) {
		var prints, honors bool
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkg.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Print"):
				prints = true
			// Any use of the printer counts, including Fprint — a command whose
			// output is ALWAYS a document (survey) reaches for that one, since it
			// has no human rendering to choose between.
			case sel.Sel.Name == "PrintOutput",
				pkg.Name == "cliout" && (sel.Sel.Name == "Print" || sel.Sel.Name == "Fprint"),
				sel.Sel.Name == "JSONMode":
				honors = true
			}
			return true
		})
		if prints && !honors {
			found = append(found, path)
		}
	})
	allowed := map[string]string{}
	for k, v := range streamingCommands {
		allowed[k] = v
	}
	for k, v := range pendingJSON {
		allowed[k] = v
	}
	report(t, found, allowed,
		"prints without ever consulting --json, so a caller asking for JSON gets prose",
		"render through cliout.Print, or add it to streamingCommands with a reason")
}

// forEachFile parses every non-test Go file under the commands tree.
func forEachFile(t *testing.T, fn func(path string, file *ast.File, fset *token.FileSet)) {
	t.Helper()
	fset := token.NewFileSet()
	for _, dir := range scanDirs {
		walkOne(t, dir, fn, fset)
	}
}

func walkOne(t *testing.T, dir string, fn func(path string, file *ast.File, fset *token.FileSet), fset *token.FileSet) {
	t.Helper()
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return perr
		}
		fn(filepath.ToSlash(path), parsed, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}

func posLine(fset *token.FileSet, p token.Pos) string {
	return strconv.Itoa(fset.Position(p).Line)
}

// report fails with everything found that is not allowlisted, and — just as
// importantly — with anything allowlisted that no longer needs to be, so the
// lists cannot outlive the problems they describe.
func report(t *testing.T, found []string, allow map[string]string, problem, fix string) {
	t.Helper()
	var unexpected []string
	seen := map[string]bool{}
	for _, f := range found {
		key := strings.SplitN(f, ":", 2)[0]
		seen[key] = true
		if _, ok := allow[key]; !ok {
			unexpected = append(unexpected, f)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("%d file(s) %s.\nFix: %s.\n  %s",
			len(unexpected), problem, fix, strings.Join(unexpected, "\n  "))
	}
	var stale []string
	for k := range allow {
		if !seen[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("%d allowlist entr(ies) no longer apply — delete them:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// printedTypes returns the names of struct types declared in fn whose values
// are handed to the printer — i.e. the ones that ARE this command's output.
//
// It works by name because that is all the AST gives without type checking:
// find the local variables declared as T or []T, then see whether any of them
// is an argument to PrintOutput/cliout.Print. A decode-only type never is.
func printedTypes(fn *ast.FuncDecl) map[string]bool {
	varOfType := map[string]string{} // variable name -> struct type name
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || vs.Type == nil {
			return true
		}
		name := typeName(vs.Type)
		if name == "" {
			return true
		}
		for _, id := range vs.Names {
			varOfType[id.Name] = name
		}
		return true
	})

	printed := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "PrintOutput" && sel.Sel.Name != "Print") {
			return true
		}
		for _, arg := range call.Args {
			id, ok := arg.(*ast.Ident)
			if !ok {
				continue
			}
			if t, ok := varOfType[id.Name]; ok {
				printed[t] = true
			}
		}
		return true
	})
	return printed
}

// typeName unwraps []T, *T and T to the bare identifier.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.ArrayType:
		return typeName(t.Elt)
	case *ast.StarExpr:
		return typeName(t.X)
	}
	return ""
}
