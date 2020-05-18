//+build windows

package visor

type pty struct {
}

func initStack() []initFunc {
	return []initFunc{
		initUpdater,
		initSNet,
		initTransport,
		initRouter,
		initLauncher,
		initCLI,
		initHypervisors,
		initUptimeTracker,
	}
}
