// Package cliout pkg/cliout/filter.go
//
// Output post-processors shared by every command that renders through Print,
// plus the flag registration and the --help --json hook so a single call wires
// them onto any root command (the top-level `skywire` binary and the standalone
// `skywire-cli` both need them):
//
//	--jq <filter>  run the JSON value through gojq (already vendored) before
//	               printing. Implies --json. Lets a caller pull one field out
//	               of a large payload instead of dumping it.
//	--shape        print the output type's SKELETON — every field at its zero
//	               value, omitempty forced visible — instead of live data, so
//	               the schema is discoverable without a populated response.
package cliout

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/cliout/clihelp"
)

// JQFlag / ShapeFlag are the persistent flags added by RegisterOutputFlags.
const (
	JQFlag    = "jq"
	ShapeFlag = "shape"
)

// RegisterOutputFlags adds --json, --jq and --shape as persistent flags on cmd,
// so every subcommand inherits them. Call it once per root command.
func RegisterOutputFlags(cmd *cobra.Command) {
	fs := cmd.PersistentFlags()
	if fs.Lookup(JSONFlag) == nil {
		fs.Bool(JSONFlag, false, "print output as JSON")
	}
	if fs.Lookup(JQFlag) == nil {
		fs.String(JQFlag, "", "filter JSON output through a jq/gojq expression (implies --json)")
	}
	if fs.Lookup(ShapeFlag) == nil {
		fs.Bool(ShapeFlag, false, "print the output schema skeleton (zero values, all fields) instead of data")
	}
}

// SetJSONHelp installs a help function on root that, in machine mode
// (--json/--jq/--shape), prints the command's machine-readable description
// (clihelp.Of) instead of the human help text. cobra hands the help function
// down to every child, so one call covers the whole tree.
func SetJSONHelp(root *cobra.Command) {
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if !machineMode(cmd) {
			defaultHelp(cmd, args)
			return
		}
		if err := Print(cmd, clihelp.Of(cmd)); err != nil {
			fmt.Fprintln(os.Stderr, err) //nolint:errcheck
		}
	})
}

// machineMode reports whether any machine-output flag was set.
func machineMode(cmd *cobra.Command) bool {
	return JSONMode(cmd) || JQFilter(cmd) != "" || ShapeMode(cmd)
}

// flagLookup finds a flag on the command's own or inherited set.
func flagLookup(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	return cmd.InheritedFlags().Lookup(name)
}

// JQFilter returns the --jq expression, or "" if unset.
func JQFilter(cmd *cobra.Command) string {
	if f := flagLookup(cmd, JQFlag); f != nil {
		return f.Value.String()
	}
	return ""
}

// ShapeMode reports whether --shape was set.
func ShapeMode(cmd *cobra.Command) bool {
	if f := flagLookup(cmd, ShapeFlag); f != nil {
		return f.Value.String() == "true"
	}
	return false
}

// printJQ marshals v, runs it through the jq filter, and writes the results.
func printJQ(w io.Writer, v interface{}, filter string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	out, err := ApplyJQ(b, filter)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, out)
	return err
}

// ApplyJQ runs filter over the JSON in src and returns the printed results (one
// per line). Object/array results are pretty-printed; scalars print as their
// JSON literal, matching jq's default (non -r) behavior.
func ApplyJQ(src []byte, filter string) (string, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return "", fmt.Errorf("invalid --jq filter: %w", err)
	}
	var input interface{}
	if err := json.Unmarshal(src, &input); err != nil {
		return "", fmt.Errorf("decode JSON for --jq: %w", err)
	}
	var sb strings.Builder
	iter := query.Run(input)
	for {
		val, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := val.(error); ok {
			return "", fmt.Errorf("--jq: %w", err)
		}
		b, err := json.MarshalIndent(val, "", "  ")
		if err != nil {
			return "", fmt.Errorf("--jq encode result: %w", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// orderedMap marshals its entries in insertion order (a Go map sorts keys), so
// a --shape skeleton preserves struct field order. MarshalIndent re-indents the
// compact bytes MarshalJSON returns, so nested orderedMaps still pretty-print.
type orderedMap struct {
	keys []string
	vals map[string]interface{}
}

func newOrderedMap() *orderedMap { return &orderedMap{vals: map[string]interface{}{}} }

func (o *orderedMap) set(k string, v interface{}) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// Skeleton returns a shape skeleton for v's type: every exported field at its
// zero value, omitempty ignored so nothing is hidden. Types with a custom
// MarshalJSON (cipher.PubKey, time.Time, uuid.UUID, …) render as their zero
// marshaled form rather than being recursed into. Native-only (the CLI is never
// built under TinyGo), so reflect is fine here.
func Skeleton(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	return skeletonType(reflect.TypeOf(v), map[reflect.Type]bool{})
}

func skeletonType(t reflect.Type, seen map[reflect.Type]bool) interface{} {
	if t == nil {
		return nil
	}
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) {
		if s, ok := marshalZero(t); ok {
			return s
		}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return skeletonType(t.Elem(), seen)
	case reflect.Interface:
		return nil
	case reflect.Struct:
		if seen[t] {
			return "<recursive:" + t.Name() + ">"
		}
		seen[t] = true
		defer delete(seen, t)
		om := newOrderedMap()
		addStructFields(t, om, seen)
		return om
	case reflect.Map:
		inner := newOrderedMap()
		inner.set("<key>", skeletonType(t.Elem(), seen))
		return inner
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "" // []byte marshals as a base64 string
		}
		return []interface{}{skeletonType(t.Elem(), seen)}
	case reflect.String:
		return ""
	case reflect.Bool:
		return false
	case reflect.Float32, reflect.Float64:
		return 0.0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return 0
	default:
		return nil
	}
}

func addStructFields(t reflect.Type, om *orderedMap, seen map[reflect.Type]bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !(ft.Implements(jsonMarshalerType) || reflect.PointerTo(ft).Implements(jsonMarshalerType)) {
				addStructFields(ft, om, seen)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		om.set(name, skeletonType(f.Type, seen))
	}
}

func marshalZero(t reflect.Type) (interface{}, bool) {
	b, err := json.Marshal(reflect.Zero(t).Interface())
	if err != nil {
		return nil, false
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, false
	}
	return v, true
}
