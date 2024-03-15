package cliconfig

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/bitfield/script"
)

var (
	shell string
)

func init() {
	switch runtime.GOOS {
	case "windows":
		if _, err := exec.LookPath("bash"); err == nil {
			shell = "bash"
		} else if _, err := exec.LookPath("powershell"); err == nil {
			shell = "powershell"
		} else {
			panic("Required binaries 'bash' and 'powershell' not found")
		}
	case "linux", "darwin":
		if _, err := exec.LookPath("bash"); err != nil {
			panic("Required binary 'bash' not found")
		}
		shell = "bash"
	default:
		panic("Unsupported operating system: " + runtime.GOOS)
	}
}

// Reference Issue https://github.com/skycoin/skywire/issues/1606

func TestConfigGenCmdFunc(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		expectedErr bool
	}{
		{
			name:        "first config gen -r",
			command:     "config gen -r -o test-config.json",
			expectedErr: false,
		},
		{
			name:        "second config gen -r",
			command:     "config gen -r -o test-config.json",
			expectedErr: false,
		},
		{
			name:        "config gen -rf",
			command:     "config gen -rf -o test-config.json",
			expectedErr: true,
		},
	}
	_ = os.Remove("test-config.json") //nolint
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := script.Exec(shell + ` -c "go run ../../skywire-cli.go ` + test.command + `"`).Stdout()
			if err != nil {
				if !test.expectedErr {
					t.Fatalf("Expected error: %v, but got: %v", test.expectedErr, err)
				}
			}
			if err == nil {
				if test.expectedErr {
					t.Fatalf("Expected error: %v, but got: %v", test.expectedErr, err)
				}
			}
		})
	}
	_ = os.Remove("test-config.json") //nolint
}
