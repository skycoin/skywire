// Package commands cmd/visor-map/commands/root.go
package commands

import (
	"bytes"
	"encoding/json"
	"image/color"
	"image/jpeg"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	sm "github.com/flopp/go-staticmaps"
	"github.com/golang/geo/s2"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
)

const (
	defaultUptimeTrackerHost = "http://uptime-tracker.skywire.skycoin.com"
	imageExtension           = ".jpg"

	mapMarkerSize = 25.0

	worldCenterLat = 14.5570
	worldCenterLon = 121.0193
)

var mapMarkerColor = color.RGBA{
	R: 0xff,
	G: 0,
	B: 0,
	A: 0xff,
}

var (
	width      int
	height     int
	output     string
	trackerURL string
)

func init() {
	rootCmd.Flags().IntVar(&width, "width", 1200, "image width\033[0m")
	rootCmd.Flags().IntVar(&height, "height", 800, "image height\033[0m")
	rootCmd.Flags().StringVarP(&output, "output", "o", "./map"+imageExtension, "output .jpg file\033[0m")
	rootCmd.Flags().StringVar(&trackerURL, "tracker-url", defaultUptimeTrackerHost, "uptime tracker URL\033[0m")
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var rootCmd = &cobra.Command{
	Use:   "visor-map",
	Short: "Utility to render visors map",
	Long: `
	┬  ┬┬┌─┐┌─┐┬─┐   ┌┬┐┌─┐┌─┐
	└┐┌┘│└─┐│ │├┬┘───│││├─┤├─┘
	 └┘ ┴└─┘└─┘┴└─   ┴ ┴┴ ┴┴  `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		const loggerTag = "visor_map"
		logger := logging.MustGetLogger(loggerTag)

		if !strings.HasSuffix(output, imageExtension) {
			logger.Fatalf("Invalid output path: no %s extension", imageExtension)
		}

		if width <= 0 {
			logger.Fatalf("Invalid width: %v", width)
		}

		if height <= 0 {
			logger.Fatalf("Invalid height: %v", height)
		}

		resp, err := http.Get(trackerURL + "/visors")
		if err != nil {
			logger.WithError(err).Fatalln("Failed to get data from uptime tracker")
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.WithError(err).Errorln("Failed to close uptime tracker response body")
			}
		}()

		if resp.StatusCode != http.StatusOK {
			logger.Fatalf("Got code %d from uptime tracker", resp.StatusCode)
		}

		var visors VisorsResponse
		if err := json.NewDecoder(resp.Body).Decode(&visors); err != nil {
			logger.WithError(err).Fatalln("Failed to unmarshal uptime tracker response")
		}

		mapCtx := sm.NewContext()
		mapCtx.SetSize(width, height)

		if len(visors) == 0 {
			// for map to be rendered we need to set at least one point or a center
			mapCtx.SetCenter(s2.LatLngFromDegrees(worldCenterLat, worldCenterLon))
		}

		for _, v := range visors {
			latLng := s2.LatLngFromDegrees(v.Lat, v.Lon)
			marker := sm.NewMarker(latLng, mapMarkerColor, mapMarkerSize)

			mapCtx.AddMarker(marker) //nolint
		}

		img, err := mapCtx.Render()
		if err != nil {
			logger.WithError(err).Fatalln("Failed to render map")
		}

		buf := new(bytes.Buffer)
		if err := jpeg.Encode(buf, img, nil); err != nil {
			logger.WithError(err).Fatalln("Failed to encode jpeg")
		}

		f, err := os.Create(filepath.Clean(output))
		if err != nil {
			logger.WithError(err).Errorln("Failed to create image file")
		}

		if _, err := f.Write(buf.Bytes()); err != nil {
			logger.WithError(err).Errorln("Failed to write image file")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

// VisorsResponse is the tracker API response format for `/visors`.
type VisorsResponse []VisorDef

// VisorDef is the item of `VisorsResponse`.
type VisorDef struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
