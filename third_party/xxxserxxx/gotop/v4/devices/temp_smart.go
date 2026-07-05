//go:build linux || (darwin && cgo)
// +build linux darwin,cgo

package devices

import (
	"log"

	"github.com/anatol/smart.go"
	"github.com/jaypipes/ghw"
)

var smDevices map[string]smart.Device

func startBlock(vars map[string]string) error {
	smDevices = make(map[string]smart.Device)

	block, err := ghw.Block()
	if err != nil {
		log.Printf("error getting block device info: %s", err)
		return err
	}
	for _, disk := range block.Disks {
		dev, err := smart.Open("/dev/" + disk.Name)
		if err != nil {
			log.Printf("error opening smart info for %s: %s", disk.Name, err)
			continue
		}
		smDevices[disk.Name+"_"+disk.Model] = dev
	}
	return nil
}

func endBlock() error {
	for name, dev := range smDevices {
		err := dev.Close()
		if err != nil {
			log.Printf("error closing device %s: %s", name, err)
		}
	}
	return nil
}

func readDiskTemps(temps map[string]int) {
	for name, dev := range smDevices {
		attr, err := dev.ReadGenericAttributes()
		if err != nil {
			log.Printf("error getting smart data for %s: %s", name, err)
			continue
		}
		temps[name] = int(attr.Temperature) //nolint:gosec // upstream code; safe under documented invariants
	}
}
