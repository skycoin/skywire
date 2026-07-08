package gopus

import (
	encodercore "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

type applicationSettings struct {
	lowDelay  bool
	voip      bool
	mode      encodercore.Mode
	bandwidth types.Bandwidth
	signal    types.Signal
}

func settingsForApplication(application Application) (applicationSettings, error) {
	switch application {
	case ApplicationVoIP:
		return applicationSettings{
			voip:      true,
			mode:      encodercore.ModeAuto,
			bandwidth: types.BandwidthWideband,
			signal:    types.SignalAuto,
		}, nil
	case ApplicationAudio:
		return applicationSettings{
			mode:      encodercore.ModeAuto,
			bandwidth: types.BandwidthFullband,
			signal:    types.SignalAuto,
		}, nil
	case ApplicationLowDelay:
		return applicationSettings{
			lowDelay:  true,
			mode:      encodercore.ModeCELT,
			bandwidth: types.BandwidthFullband,
			signal:    types.SignalAuto,
		}, nil
	case ApplicationRestrictedSilk:
		return applicationSettings{
			mode:      encodercore.ModeSILK,
			bandwidth: types.BandwidthWideband,
			signal:    types.SignalAuto,
		}, nil
	case ApplicationRestrictedCelt:
		return applicationSettings{
			lowDelay:  true,
			mode:      encodercore.ModeCELT,
			bandwidth: types.BandwidthFullband,
			signal:    types.SignalAuto,
		}, nil
	default:
		return applicationSettings{}, ErrInvalidApplication
	}
}

func validateMutableApplication(current Application, encodedOnce bool, next Application) error {
	if current == ApplicationRestrictedSilk || current == ApplicationRestrictedCelt {
		return ErrInvalidApplication
	}
	switch next {
	case ApplicationVoIP, ApplicationAudio, ApplicationLowDelay:
		if encodedOnce && current != next {
			return ErrInvalidApplication
		}
		return nil
	default:
		return ErrInvalidApplication
	}
}
