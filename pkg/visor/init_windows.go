//+build windows

package visor

func initDmsgpty(ctx context.Context, log *logging.Logger) error {
	log.Error("dmsgpty is not supported on windows.")
	return nil
}
