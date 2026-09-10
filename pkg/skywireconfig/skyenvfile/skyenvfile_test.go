// Package skyenvfile pkg/skywireconfig/skyenvfile/skyenvfile_test.go c4-vis-cli
package skyenvfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateReplacesCommentedKeyAndAppendsMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "skywire.conf")
	src := "PKGENV=true\n#--\tSet remote hypervisor public keys\n#HYPERVISORPKS=('')\nISHYPERVISOR=false\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	edits := []Edit{
		{Key: "HYPERVISORPKS", Value: FormatBashArray("aa,bb"), Raw: "aa,bb"},
		{Key: "DMSGSERVERCONF", Value: FormatString("/etc/skywire-dmsg.json"), Raw: "/etc/skywire-dmsg.json"},
	}
	if err := Update(p, edits); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"PKGENV=true\n",
		"#--\tSet remote hypervisor public keys\n",
		"HYPERVISORPKS=('aa' 'bb')\n",
		"ISHYPERVISOR=false\n",
		"DMSGSERVERCONF='/etc/skywire-dmsg.json'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "#HYPERVISORPKS") {
		t.Errorf("commented key not replaced:\n%s", s)
	}
	if LineKey("  # FOO_1=x") != "FOO_1" || LineKey("foo=x") != "" {
		t.Errorf("LineKey mismatch")
	}
	if FormatBashArray("") != "('')" {
		t.Errorf("empty array = %q", FormatBashArray(""))
	}
}
