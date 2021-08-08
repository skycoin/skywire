//+build darwin

package visor

func appendExecPlatform(envs []string) {
	envs = append(envs, "sudo")
}


