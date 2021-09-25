//go:build darwin && systray
// +build darwin,systray

package gui

import (
	"image/png"
	"os"

	"golang.org/x/image/tiff"
)

const (
	pngIconPath     = "/Applications/Skywire.app/Contents/Resources/icon.png"
	iconPath        = "/Applications/Skywire.app/Contents/Resources/tray_icon.tiff"
	deinstallerPath = "/Applications/Skywire.app/Contents/deinstaller"
	appPath         = "/Applications/Skywire.app"
)

func preReadIcon() error {
	imgFile, err := os.Open(pngIconPath)
	if err != nil {
		return err
	}
	img, err := png.Decode(imgFile)
	if err != nil {
		return err
	}

	tiffFile, err := os.Create(iconPath)
	if err != nil {
		return err
	}

	if err = tiff.Encode(tiffFile, img, nil); err != nil {
		return err
	}

	return tiffFile.Close()
}

func checkIsPackage() bool {
	_, err := os.Stat(appPath)
	return err == nil
}
