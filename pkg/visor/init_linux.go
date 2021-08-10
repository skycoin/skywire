//+build linux

package visor

func appendExecPlatform(envs []string) {
	envs = append(envs, "pkexec")
}
