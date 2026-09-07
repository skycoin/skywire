//go:build !tinygo

// Package visorconfig pkg/visor/visorconfig/preserve.go c3-vis-core
//
// Unknown-key preservation for the config read-modify-write cycle.
//
// The visor loads skywire-config.json into the typed V1 struct and
// later re-marshals that struct back over the same file (Common.flush).
// Anything in the file without a matching struct field never survives
// that round trip: it is not ignored, it is ERASED. Running an older
// build for a single restart — routine in the dev loop, and the whole
// of a downgrade — therefore deletes every setting newer than that
// binary, along with any third-party or experimental key an operator
// added by hand.
//
// The fix: before overwriting, flush reads the file it is about to
// replace and walks that JSON against the Go type being marshaled,
// recording the RESIDUE — the subtrees, at any depth, that have no
// counterpart field. The marshaled struct is then written with the
// residue re-attached. Because residue keys are by construction keys
// the struct does not model, they can never shadow a known key, and
// clearing a known field still clears it on disk: the struct remains
// the sole authority for everything it does model.
//
// Reading at flush rather than capturing at load is deliberate. The
// residue then belongs to the file actually being overwritten, so it
// survives the config being rebuilt in memory or handed a fresh
// Common (`skywire cli config update` does exactly that, which is
// where a load-time capture was observed to fall out), and an
// operator's hand-edit landing while the visor runs is no longer
// clobbered by the next flush.
//
// The alternative — json.RawMessage catch-alls with hand-written
// Marshal/Unmarshal on V1, HypervisorConfig, WasmServeConf, … — was
// rejected: it needs a new method pair on every nested block anyone
// might ever extend, and each one is a fresh chance to get the
// catch-all wrong. This walk covers every block at once and needs no
// per-type maintenance.
//
// Deliberate omissions:
//
//   - retiredKeys. A load-time migration that RENAMES a key (V1's
//     legacy "dmsgpty" → "pty", see config_compat.go) leaves the old
//     key looking "unknown". Preserving it would resurrect a key the
//     migration exists to retire, and leave two copies of the same
//     block on disk. Such keys are listed below and dropped from the
//     residue explicitly.
//
//   - Types with their own json.Unmarshaler / encoding.TextUnmarshaler
//     are opaque: their on-disk shape is not their Go shape, so a
//     field-name walk cannot tell known from unknown inside them.
//     (Duration, cipher.PubKey, and Launcher.Apps' appsList wrapper.)
//
//   - Array elements re-attach by index and only while the flushed
//     array still has the original length; a resized array drops that
//     array's residue rather than risk grafting a key onto the wrong
//     element.
//
// A config regenerated from scratch (`skywire cli config gen`,
// `skywire autoconfig`) does not go through flush at all — those
// paths build V1 in memory and write it with their own os.WriteFile —
// so regenerating remains the way to clear genuinely stale keys.
package visorconfig

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// retiredKeys are JSON keys, given as dot-separated paths from the root
// of the config, that a load-time migration deliberately consumes and
// renames away. They read as "unknown" to the walk below but must NOT
// be preserved: doing so would defeat the migration.
var retiredKeys = map[string]struct{}{
	// config_compat.go's V1.UnmarshalJSON reads the legacy "dmsgpty"
	// block into V1.Pty; marshaling always emits the canonical "pty".
	"dmsgpty": {},
}

var (
	jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*interface{ UnmarshalText([]byte) error })(nil)).Elem()
)

// captureUnknown returns the residue of raw against the Go type t: the
// parts of the JSON document that t has no field for, at every level of
// nesting. nil when the document is fully modeled by t (the common
// case), which makes flush byte-for-byte identical to what it wrote
// before this file existed.
//
// t is the type of the value that will later be marshaled back; its own
// json.Unmarshaler (V1 has one) is not consulted — only its fields'.
func captureUnknown(raw []byte, t reflect.Type) any {
	if t == nil {
		return nil
	}
	return residueOf(raw, t, "")
}

// residueOf walks one JSON value against one Go type. The result is
// either nil (nothing unknown below here), a map[string]any for an
// object, or a []any for an array; leaves are json.RawMessage holding
// the wholly-unknown subtree verbatim.
func residueOf(raw json.RawMessage, t reflect.Type, path string) any {
	t = derefType(t)
	switch firstByte(raw) {
	case '{':
		return objectResidue(raw, t, path)
	case '[':
		return arrayResidue(raw, t, path)
	default:
		// Scalars have no keys to lose.
		return nil
	}
}

func objectResidue(raw json.RawMessage, t reflect.Type, path string) any {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	switch {
	case t.Kind() == reflect.Struct:
		fields := jsonFields(t)
		out := make(map[string]any, len(obj))
		for key, val := range obj {
			sub := join(path, key)
			ft, known := fields[key]
			if !known {
				ft, known = fields[strings.ToLower(key)]
			}
			if !known {
				if _, retired := retiredKeys[sub]; retired {
					continue
				}
				out[key] = val
				continue
			}
			if isOpaque(ft) {
				continue
			}
			if child := residueOf(val, ft, sub); child != nil {
				out[key] = child
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out

	case t.Kind() == reflect.Map && t.Key().Kind() == reflect.String:
		// Every key of a map is modeled by definition; only the values
		// can hide unknown fields.
		et := t.Elem()
		if isOpaque(derefType(et)) {
			return nil
		}
		out := make(map[string]any, len(obj))
		for key, val := range obj {
			if child := residueOf(val, et, join(path, key)); child != nil {
				out[key] = child
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out

	default:
		// A JSON object against a Go type that is neither struct nor
		// string-keyed map (an interface field, say). Nothing to
		// compare names against; leave it to the marshaler.
		return nil
	}
}

func arrayResidue(raw json.RawMessage, t reflect.Type, path string) any {
	if k := t.Kind(); k != reflect.Slice && k != reflect.Array {
		return nil
	}
	et := t.Elem()
	if isOpaque(derefType(et)) {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	out := make([]any, len(arr))
	found := false
	for i, val := range arr {
		if child := residueOf(val, et, path+"[]"); child != nil {
			out[i] = child
			found = true
		}
	}
	if !found {
		return nil
	}
	return out
}

// mergeUnknown re-attaches residue to freshly marshaled JSON, returning
// compact JSON for the caller to indent. Known keys keep their order and
// their marshaled bytes; residue keys are appended, sorted, at the end of
// the object they came from. A nil residue returns marshaled untouched.
func mergeUnknown(marshaled []byte, residue any) ([]byte, error) {
	if residue == nil {
		return marshaled, nil
	}
	var buf bytes.Buffer
	if err := writeMerged(&buf, marshaled, residue); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeMerged(buf *bytes.Buffer, val json.RawMessage, residue any) error {
	switch res := residue.(type) {
	case map[string]any:
		return writeMergedObject(buf, val, res)
	case []any:
		return writeMergedArray(buf, val, res)
	default:
		buf.Write(val)
		return nil
	}
}

func writeMergedObject(buf *bytes.Buffer, val json.RawMessage, res map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(val))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		// The field no longer marshals as an object; its residue no
		// longer has anywhere to attach. Drop it rather than guess.
		buf.Write(val)
		return nil //nolint:nilerr // shape change is not an error, just a dropped residue
	}

	used := make(map[string]bool, len(res))
	buf.WriteByte('{')
	first := true
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		var sub json.RawMessage
		if err := dec.Decode(&sub); err != nil {
			return err
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		if err := writeKey(buf, key); err != nil {
			return err
		}
		child, ok := res[key]
		if !ok {
			buf.Write(sub)
			continue
		}
		used[key] = true
		// A key present in BOTH the marshaled struct and the residue
		// means the struct models it after all (a case-insensitive
		// match, say): the struct wins, and its bytes are written
		// unchanged. Only a nested residue descends.
		switch child.(type) {
		case map[string]any, []any:
			if err := writeMerged(buf, sub, child); err != nil {
				return err
			}
		default:
			buf.Write(sub)
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}

	leftover := make([]string, 0, len(res))
	for key, child := range res {
		if used[key] {
			continue
		}
		// Only whole-subtree residue can be appended; a nested residue
		// whose parent key vanished from the marshaled output has
		// nothing to attach to.
		if _, ok := child.(json.RawMessage); ok {
			leftover = append(leftover, key)
		}
	}
	sort.Strings(leftover)
	for _, key := range leftover {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		if err := writeKey(buf, key); err != nil {
			return err
		}
		buf.Write(res[key].(json.RawMessage)) //nolint:forcetypeassert // filtered above
	}
	buf.WriteByte('}')
	return nil
}

func writeMergedArray(buf *bytes.Buffer, val json.RawMessage, res []any) error {
	var arr []json.RawMessage
	if err := json.Unmarshal(val, &arr); err != nil || len(arr) != len(res) {
		// Resized (or no longer an array): re-attaching by index could
		// graft a key onto the wrong element. Drop the residue.
		buf.Write(val)
		return nil //nolint:nilerr // length change is not an error, just a dropped residue
	}
	buf.WriteByte('[')
	for i, el := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		if res[i] == nil {
			buf.Write(el)
			continue
		}
		if err := writeMerged(buf, el, res[i]); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

// writeKey emits key as a JSON object key. Re-encoding through
// json.Marshal reproduces exactly what the marshaler wrote for a known
// key, so known keys round-trip byte-for-byte.
func writeKey(buf *bytes.Buffer, key string) error {
	b, err := json.Marshal(key)
	if err != nil {
		return err
	}
	buf.Write(b)
	buf.WriteByte(':')
	return nil
}

// jsonFields maps the JSON names a struct type accepts to the Go types
// behind them, flattening anonymous embedded structs the way
// encoding/json does. Lowercased aliases are added for the
// case-insensitive fallback match encoding/json performs.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := make(map[string]reflect.Type)
	collectFields(t, out)
	return out
}

func collectFields(t reflect.Type, out map[string]reflect.Type) {
	t = derefType(t)
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" && derefType(f.Type).Kind() == reflect.Struct {
			// Embedded struct without an explicit name: its fields are
			// promoted into this object. (An embedded NON-struct is
			// encoded under its type name, so it falls through.)
			collectFields(f.Type, out)
			continue
		}
		if f.PkgPath != "" {
			// Unexported and not embedded: invisible to encoding/json.
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
		if lower := strings.ToLower(name); lower != name {
			if _, exists := out[lower]; !exists {
				out[lower] = f.Type
			}
		}
	}
}

// isOpaque reports whether a type decodes itself, in which case its
// on-disk shape need not resemble its Go shape and a field-name walk
// through it would mistake its own encoding for unknown keys.
func isOpaque(t reflect.Type) bool {
	pt := reflect.PointerTo(t)
	return t.Implements(jsonUnmarshalerType) || pt.Implements(jsonUnmarshalerType) ||
		t.Implements(textUnmarshalerType) || pt.Implements(textUnmarshalerType)
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func firstByte(raw json.RawMessage) byte {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return c
		}
	}
	return 0
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
