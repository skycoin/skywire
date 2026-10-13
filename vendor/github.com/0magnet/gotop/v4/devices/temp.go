package devices

import (
	"log"
)

// TODO add thermal history graph. Update when something changes?

var tempUpdates []func(map[string]int) map[string]error

func RegisterTemp(update func(map[string]int) map[string]error) {
	tempUpdates = append(tempUpdates, update)
}

// ClearTemps removes every registered temperature device and the sensor list
// the widget starts from, as ClearCPU does.
func ClearTemps() {
	tempUpdates = nil
	delete(_devs, Temperatures)
	delete(_defaults, Temperatures)
}

func UpdateTemps(temps map[string]int) {
	for _, f := range tempUpdates {
		errs := f(temps)
		for k, e := range errs {
			log.Print(tr.Value("error.recovfetch", "temp", k, e.Error()))
		}
	}
}
