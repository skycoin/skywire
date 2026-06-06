// Package visorconfig pkg/visor/visorconfig/v1_test.go
package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

func Test_updateStringArg(t *testing.T) {
	type args struct {
		conf    *Launcher
		appName string
		argName string
		value   string
	}
	tests := []struct {
		name       string
		args       args
		wantResult bool
		wantConf   *Launcher
	}{
		{
			name: "Case 1",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
							Args:   []string{"-passcode", "1234"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "4321",
			},
			wantResult: true,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
						Args:   []string{"-passcode", "4321"},
					},
				},
			},
		},
		{
			name: "Case 2",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
							Args:   []string{"-passcode", "1234"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "",
			},
			wantResult: true,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
						Args:   []string{},
					},
				},
			},
		},
		{
			name: "Case 3",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
							Args:   []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "",
			},
			wantResult: true,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
						Args:   []string{"-t", "-test", "abc"},
					},
				},
			},
		},
		{
			name: "Case 4",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
							Args:   []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-arg1",
				value:   "678",
			},
			wantResult: true,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
						Args:   []string{"-t", "-passcode", "1234", "-test", "abc", "-arg1", "678"},
					},
				},
			},
		},
		{
			name: "Case 5",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
							Args:   []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "unknown",
				argName: "-arg1",
				value:   "678",
			},
			wantResult: false,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
						Args:   []string{"-t", "-passcode", "1234", "-test", "abc"},
					},
				},
			},
		},
		{
			name: "Case 6",
			args: args{
				conf: &Launcher{
					Apps: []appserver.AppConfig{
						{
							Name:   "skysocks-client",
							Binary: "skysocks-client",
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "",
			},
			wantResult: true,
			wantConf: &Launcher{
				Apps: []appserver.AppConfig{
					{
						Name:   "skysocks-client",
						Binary: "skysocks-client",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateStringArg(tt.args.conf, tt.args.appName, tt.args.argName, tt.args.value)
			assert.Equal(t, tt.wantResult, result)
			assert.EqualValues(t, tt.wantConf, tt.args.conf)
		})
	}
}
