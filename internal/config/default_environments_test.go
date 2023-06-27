package config

import "fmt"

func ExampleEnvConfig_AddThreeChatVisors() {
	globalEnv := &EnvConfig{
		Description: "Example of environment configuration with global skywire-services and 3 skywire-visors",
		Runners: RunnersConfig{
			SkywireVisor: "skywire-visor {{.Name}}.json",
		},
		Skywire: DefaultPublicSkywire(),
	}

	env := globalEnv.AddThreeChatVisors()

	fmt.Printf("VisorA skychat args: %v\n", env.Visors[0].Config.Launcher.Apps[0].Args)
	fmt.Printf("VisorB apps: %v\n", env.Visors[1].Config.Launcher.Apps)
	fmt.Printf("VisorC skychat args: %v\n", env.Visors[2].Config.Launcher.Apps[0].Args)

	// Output: VisorA skychat args: [-addr :8002]
	// VisorB apps: []
	// VisorC skychat args: [-addr :8003]
}
