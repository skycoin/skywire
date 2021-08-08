//+build linux

package visor

func appendExecPlatform(envs []string) {
	append(envs, "pkexec")
}
