package rewardsserver

import (
	"os"
	"path/filepath"
	"testing"
)

// The SKYENV file is parsed, not run: a command substitution in it must not
// execute, or a file that runs skywire recurses through this package's init.
func TestSkyenvIsNotExecuted(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	env := filepath.Join(dir, "fr.conf")
	if err := os.WriteFile(env, []byte("WEBPORT=8083\nREWARDPKS=('a' 'b')\nWALLET_FILE=$(touch "+marker+")\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := skyenvfile
	skyenvfile = env
	defer func() { skyenvfile = old }()
	if got := scriptExecUint("${WEBPORT:-80}"); got != 8083 {
		t.Fatalf("WEBPORT = %d", got)
	}
	if got := scriptExecArray("${REWARDPKS[@]}"); got != "a,b" {
		t.Fatalf("REWARDPKS = %q", got)
	}
	_ = scriptExecString("${WALLET_FILE}")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the SKYENV file was executed")
	}
}
