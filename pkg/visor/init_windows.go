//+build windows

package visor

func initDmsgpty(ctx context.Context, log *logging.Logger) error {
	conf := v.conf.Dmsgpty

	if conf == nil {
		log.Info("'dmsgpty' is not configured, skipping.")
		return nil
	}

	log.Error("dmsgpty is not supported on windows.")
	return nil
}
