package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func Test_updateStringArg(t *testing.T) {
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
		{
			name: "Case 6",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: "skysocks-client",
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

func Test_updateBoolArg(t *testing.T) {
	type args struct {
		conf    *V1Launcher
		appName string
		argName string
		value   bool
	}
	tests := []struct {
		name       string
		args       args
		wantResult bool
		wantConf   *V1Launcher
	}{
		{
			name: "Single dash flag, absent value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"-passcode", "1234"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "-killswitch",
				value:   true,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-passcode", "1234", "-killswitch=true"},
					},
				},
			},
		},
		{
			name: "Double dash flag, absent value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"-passcode", "1234"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   false,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-passcode", "1234", "-killswitch=false"},
					},
				},
			},
		},
		{
			name: "Present valid double-dash-named value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"-passcode", "1234", "--killswitch=true"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   false,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-passcode", "1234", "-killswitch=false"},
					},
				},
			},
		},
		{
			name: "Present valid single-dash-named value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"-passcode", "1234", "-killswitch=false"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   true,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-passcode", "1234", "-killswitch=true"},
					},
				},
			},
		},
		{
			name: "Present invalid single-dash-named value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"-passcode", "1234", "-killswitch", "false"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   true,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-passcode", "1234", "-killswitch=true"},
					},
				},
			},
		},
		{
			name: "Present invalid double-dash-named value",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
							Args: []string{"--killswitch", "true"},
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   false,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-killswitch=false"},
					},
				},
			},
		},
		{
			name: "Empty args list",
			args: args{
				conf: &V1Launcher{
					Apps: []launcher.AppConfig{
						{
							Name: skyenv.VPNClientName,
						},
					},
				},
				appName: skyenv.VPNClientName,
				argName: "--killswitch",
				value:   false,
			},
			wantResult: true,
			wantConf: &V1Launcher{
				Apps: []launcher.AppConfig{
					{
						Name: skyenv.VPNClientName,
						Args: []string{"-killswitch=false"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateBoolArg(tt.args.conf, tt.args.appName, tt.args.argName, tt.args.value)
			assert.Equal(t, tt.wantResult, result)
			assert.EqualValues(t, tt.wantConf, tt.args.conf)
		})
	}
}
