//+build windows

package visor

// InitDmsgpty initializes dmsgpty.
func InitDmsgpty(v *Visor) bool {
	report := v.makeReporter("dmsgpty")
	conf := v.conf.Dmsgpty

	if conf == nil {
		v.log.Info("'dmsgpty' is not configured, skipping.")
		return report(nil)
	}

	v.log.Error("dmsgpty is not supported on windows.")
	return report(nil)
}
