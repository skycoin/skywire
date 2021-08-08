//+build windows

package visor

func appendExecPlatform(envs []string) {
	envs = append(envs, "runas")
}
