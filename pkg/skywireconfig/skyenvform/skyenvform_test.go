package skyenvform

import (
	"reflect"
	"slices"
	"testing"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func testState(vals map[string]string) visorapi.SkyenvState {
	return visorapi.SkyenvState{Exists: true, Writable: true, Values: vals, Flags: autoconfigcmd.Describe()}
}

func rowByKey(t *testing.T, rows []Row, key string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no row for %s", key)
	return Row{}
}

func TestSettingRowsOnePerVariable(t *testing.T) {
	rows := Rows(testState(map[string]string{
		"ISHYPERVISOR": "true", "HYPERVISORPKS": "aa bb", "DMSGWEBSK": visorapi.SkyenvRedacted,
	}))
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Key] {
			t.Fatalf("%s has two rows", r.Key)
		}
		seen[r.Key] = true
		if r.Key == "SK" {
			t.Fatal("the secret key must not be offered")
		}
	}
	// --ishv and --no-ishv are one row, edited through the positive flag.
	hv := rowByKey(t, rows, "ISHYPERVISOR")
	if hv.Flag != "ishv" || hv.Negate || !hv.Set || hv.Value != "true" {
		t.Fatalf("ISHYPERVISOR row = %+v", hv)
	}
	if pks := rowByKey(t, rows, "HYPERVISORPKS"); pks.Value != "aa,bb" {
		t.Fatalf("array shown as %q, want aa,bb", pks.Value)
	}
	if sk := rowByKey(t, rows, "DMSGWEBSK"); !sk.Secret {
		t.Fatal("DMSGWEBSK should be a secret row")
	}
	if r := rowByKey(t, rows, "MINDMSGSESS"); r.Locked || r.Set {
		t.Fatalf("MINDMSGSESS row = %+v", r)
	}
}

func TestSettingsEdits(t *testing.T) {
	rows := Rows(testState(map[string]string{
		"MINDMSGSESS": "3", "HYPERVISORPKS": "aa bb", "DMSGWEBSK": visorapi.SkyenvRedacted,
		"PROXYSERVER": "true", "LOGLVL": "debug", "SURVEYPKS": "",
	}))
	for i := range rows {
		rows[i].Locked = rows[i].Key == "ISHYPERVISOR"
	}
	edits, changes, err := Edits(rows, map[string]Input{
		"MINDMSGSESS":   {Value: "3"},      // unchanged
		"HYPERVISORPKS": {Value: "aa, bb"}, // unchanged once normalized
		"DMSGWEBSK":     {Value: "(set)"},  // placeholder echoed back
		"PROXYSERVER":   {Value: "off"},    // set: via the positive flag
		"SKYCHAT":       {Value: "on"},     // unset → set
		"LOGLVL":        {Value: ""},       // cleared text → back to default
		"MAXTRANSPORTS": {Unset: true},     // already at default
		"ISHYPERVISOR":  {Value: "false"},  // locked by the caller
		"ROUTESETUPPKS": {Value: "cc\ndd"}, // newline-separated list
		"VPNROUTERSSID": {Value: "home"},   // any other row is editable
		"SURVEYPKS":     {Value: ""},       // set to nothing and left empty
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSet := map[string]string{"proxyserver": "false", "skychat": "true", "routesetup": "cc,dd", "vpnrouter-ssid": "home"}
	if !reflect.DeepEqual(edits.Set, wantSet) {
		t.Fatalf("Set = %v, want %v", edits.Set, wantSet)
	}
	if !reflect.DeepEqual(edits.Unset, []string{"loglvl"}) {
		t.Fatalf("Unset = %v", edits.Unset)
	}
	var keys []string
	for _, c := range changes {
		keys = append(keys, c.Key)
	}
	slices.Sort(keys)
	if want := []string{"LOGLVL", "PROXYSERVER", "ROUTESETUPPKS", "SKYCHAT", "VPNROUTERSSID"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("changes = %v, want %v", keys, want)
	}
}

// A --no-X flag seen alone is sent inverted, and a new secret is never
// echoed into the preview.
func TestSettingsEditsNegatedAndSecret(t *testing.T) {
	rows := []Row{
		{Key: "NODIRECT", Flag: "no-direct", Negate: true, Format: "bool"},
		{Key: "DMSGWEBSK", Flag: "dmsgweb-sk", Format: "string"},
	}
	edits, changes, err := Edits(rows, map[string]Input{
		"NODIRECT": {Value: "true"}, "DMSGWEBSK": {Value: "abc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edits.Set["no-direct"] != "false" {
		t.Fatalf("negated flag sent %q, want false", edits.Set["no-direct"])
	}
	for _, c := range changes {
		if c.Key == "DMSGWEBSK" && c.To != "(changed)" {
			t.Fatalf("secret previewed as %q", c.To)
		}
	}
	if _, _, err := Edits(rows, map[string]Input{"NODIRECT": {Value: "maybe"}}); err == nil {
		t.Fatal("a non-bool value for a bool should fail")
	}
}

func TestCheckAndCommand(t *testing.T) {
	rows := Rows(testState(nil))
	hops := rowByKey(t, rows, "MINDMSGSESS")
	if Check(hops, "x") == "" || Check(hops, "3") != "" || Check(hops, "") != "" {
		t.Fatal("int check")
	}
	hvpks := rowByKey(t, rows, "HYPERVISORPKS")
	if Check(hvpks, "nope") == "" {
		t.Fatal("pks check")
	}
	got := Command(rows, visorapi.SkyenvEdits{Set: map[string]string{"skychat": "true", "loglvl": "a b"}, Unset: []string{"ishv"}})
	if want := "sudo skywire autoconfig '--loglvl=a b' --skychat=true --ishv=" + rowByKey(t, rows, "ISHYPERVISOR").Default; got != want {
		t.Fatalf("Command = %q, want %q", got, want)
	}
}
