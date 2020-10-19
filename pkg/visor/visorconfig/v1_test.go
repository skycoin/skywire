package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/app/launcher"
)

func Test_updateArg(t *testing.T) {
	type args struct {
		conf    *V1Launcher
		appName string
		argName string
		value   string
	}
	tests := []struct {
		name       string
		args       args
		wantResult bool
		wantConf   *V1Launcher
	}{
		{
			name: "Case 1",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
							Args: []string{"-passcode", "1234"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "4321",
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: "skysocks-client",
						Args: []string{"-passcode", "4321"},
					},
				},
			},
		},
		{
			name: "Case 2",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
							Args: []string{"-passcode", "1234"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "",
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: "skysocks-client",
						Args: []string{},
					},
				},
			},
		},
		{
			name: "Case 3",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
							Args: []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-passcode",
				value:   "",
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: "skysocks-client",
						Args: []string{"-t", "-test", "abc"},
					},
				},
			},
		},
		{
			name: "Case 4",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
							Args: []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "skysocks-client",
				argName: "-arg1",
				value:   "678",
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: "skysocks-client",
						Args: []string{"-t", "-passcode", "1234", "-test", "abc", "-arg1", "678"},
					},
				},
			},
		},
		{
			name: "Case 5",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
							Args: []string{"-t", "-passcode", "1234", "-test", "abc"},
						},
					},
				},
				appName: "unknown",
				argName: "-arg1",
				value:   "678",
			},
			wantResult: false,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: "skysocks-client",
						Args: []string{"-t", "-passcode", "1234", "-test", "abc"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateArg(tt.args.conf, tt.args.appName, tt.args.argName, tt.args.value)
			assert.Equal(t, result, tt.wantResult)
			assert.EqualValues(t, tt.args.conf, tt.wantConf)
		})
	}
}
