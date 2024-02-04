// /* cmd/skywire-visor/skywire-visor.go
/*
skywire visor
*/
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math"
	"math/rand"
	"os"
	"runtime"
	"time"
	"io"

	"golang.org/x/mobile/asset"
//	mfont "golang.org/x/mobile/exp/font"
	"github.com/bitfield/script"
	"github.com/golang/freetype/truetype"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/mobile/app"
	"golang.org/x/mobile/event/key"
	"golang.org/x/mobile/event/lifecycle"
	"golang.org/x/mobile/event/paint"
	"golang.org/x/mobile/event/size"
	mfont "golang.org/x/mobile/exp/font"
	"golang.org/x/mobile/exp/gl/glutil"
	"golang.org/x/mobile/exp/sprite/clock"
	"golang.org/x/mobile/geom"
	"golang.org/x/mobile/gl"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	setupnode "github.com/skycoin/skywire/cmd/setup-node/commands"
	skywirecli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	if runtime.GOOS == "android" {
		rootCmd.Run = runMobile
	}
	rootCmd.AddCommand(
		visor.RootCmd,
		skywirecli.RootCmd,
		setupnode.RootCmd,
		mobileCmd,
	)
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
	rootCmd.CompletionOptions.DisableDefaultCmd = true

}

var rootCmd = &cobra.Command{
	Use: "skywire",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

func main() {


	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}

const help = "{{if gt (len .Aliases) 0}}" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"

func runMobile(cmd *cobra.Command, args []string) {
		rand.Seed(time.Now().UnixNano())
		app.Main(func(a app.App) {
			var glctx gl.Context
			var sz size.Event
			for e := range a.Events() {
				switch e := a.Filter(e).(type) {
				case lifecycle.Event:
					switch e.Crosses(lifecycle.StageVisible) {
					case lifecycle.CrossOn:
						glctx, _ = e.DrawContext.(gl.Context)
						conf, err := script.Exec(os.Args[0] + " cli config gen -ynN --nofetch").String()
						if err != nil {
							mobilepk = err.Error()
	//							onStop()
	//							glctx = nil
	//							return
						} else {
							mobilepk, _ = script.Echo(conf).JQ(".pk").String()
//						go func() {
//							_, err := script.Exec(os.Args[0] + " visor -a '" + conf + "'").Stdout()
//							if err != nil {
//								onStop()
//								glctx = nil
//								return
//							}
//							}()
						}
						onStart(glctx)
						a.Send(paint.Event{})
					case lifecycle.CrossOff:
						onStop()
						glctx = nil
					}
				case size.Event:
					sz = e
				case paint.Event:
					if glctx == nil || e.External {
						continue
					}
					onPaint(glctx, sz)
					a.Publish()
					a.Send(paint.Event{})
				case key.Event:
					if e.Code != key.CodeSpacebar {
						break
					}
				}
			}
		})
	}

var mobilepk string
var mobileCmd = &cobra.Command{
	Use:    "mobile",
	Short:  "mobile ui",
	Hidden: true,
	Run: runMobile,
}

const (
	dpi = 72
)

type TextAlign int

const (
	Center TextAlign = iota
	Left
	Right
)

type TextSprite struct {
	placeholder     string
	text            string
	font            *truetype.Font
	widthPx         int
	heightPx        int
	textColor       *image.Uniform
	backgroundColor *image.Uniform
	fontSize        float64
	xPt             geom.Pt
	yPt             geom.Pt
	align           TextAlign
}

func (ts TextSprite) Render(sz size.Event) {
	sprite := images.NewImage(ts.widthPx, ts.heightPx)

	draw.Draw(sprite.RGBA, sprite.RGBA.Bounds(), ts.backgroundColor, image.ZP, draw.Src)

	d := &font.Drawer{
		Dst: sprite.RGBA,
		Src: ts.textColor,
		Face: truetype.NewFace(ts.font, &truetype.Options{
			Size:    ts.fontSize,
			DPI:     dpi,
			Hinting: font.HintingNone,
		}),
	}

	// Position
	dy := int(math.Ceil(ts.fontSize * dpi / dpi))
	var textWidth fixed.Int26_6
	if ts.placeholder == "" {
		textWidth = d.MeasureString(ts.text)
	} else {
		textWidth = d.MeasureString(ts.placeholder)
	}

	switch ts.align {
	case Center:
		d.Dot = fixed.Point26_6{
			X: fixed.I(sz.Size().X/2) - (textWidth / 2),
			Y: fixed.I(ts.heightPx/2 + dy/2),
		}
	case Left:
		d.Dot = fixed.Point26_6{
			X: fixed.I(0),
			Y: fixed.I(ts.heightPx/2 + dy/2),
		}
	case Right:
		d.Dot = fixed.Point26_6{
			X: fixed.I(sz.Size().X) - textWidth,
			Y: fixed.I(ts.heightPx/2 + dy/2),
		}
	}

	d.DrawString(ts.text)

	sprite.Upload()
	sprite.Draw(
		sz,
		geom.Point{X: ts.xPt, Y: ts.yPt},
		geom.Point{X: ts.xPt + sz.WidthPt, Y: ts.yPt},
		geom.Point{X: ts.xPt, Y: ts.yPt + sz.HeightPt},
		sz.Bounds())
	sprite.Release()
}

var (
	startTime = time.Now()
	images    *glutil.Images
	game      *Game
	lines     []string
)

func onStart(glctx gl.Context) {
	images = glutil.NewImages(glctx)
	game = NewGame()
	lines = append(lines, mobilepk)
	//	go generateNewLines()
}

func onStop() {
	images.Release()
	game = nil
}

func onPaint(glctx gl.Context, sz size.Event) {
	glctx.ClearColor(1, 1, 1, 1)
	glctx.Clear(gl.COLOR_BUFFER_BIT)
	now := clock.Time(time.Since(startTime) * 60 / time.Second)
	game.Update(now)
	game.Render(sz, glctx, images)
}

func generateNewLines() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		lines = append([]string{"Skywire"}, lines...)
	}
}

type Game struct {
	lastCalc   clock.Time
	touchCount uint64
	font       *truetype.Font
}

func NewGame() *Game {
	var g Game
	g.reset()
	return &g
}

func (g *Game) reset() {
	var err error
	g.font, err = LoadCustomFont()
	if err != nil {
		log.Fatalf("error parsing font: %v", err)
	}
}

func (g *Game) Update(now clock.Time) {
	for ; g.lastCalc < now; g.lastCalc++ {
		g.calcFrame()
	}
}

func (g *Game) calcFrame() {
	// Calculate game logic here if needed
}

func (g *Game) Render(sz size.Event, glctx gl.Context, images *glutil.Images) {
	for i, line := range lines {
		text := &TextSprite{
			text:            line,
			font:            g.font,
			widthPx:         sz.WidthPx,
			heightPx:        sz.HeightPx,
			textColor:       image.White,
			backgroundColor: image.NewUniform(color.RGBA{0x35, 0x67, 0x99, 0xFF}),
			fontSize:        12,
			xPt:             0,
			yPt:             PxToPt(sz, i*20),
		}
		text.Render(sz)
	}
}

func PxToPt(sz size.Event, sizePx int) geom.Pt {
	return geom.Pt(float32(sizePx) / sz.PixelsPerPt)
}

func LoadCustomFont() (font *truetype.Font, err error) {
	file, err := asset.Open("mononoki-Regular.ttf")
	if err != nil {
		fmt.Printf("error opening font asset: %v\n", err)
		return loadFallbackFont()
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		fmt.Printf("error reading font: %v\n", err)
		return loadFallbackFont()
	}
	font, err = truetype.Parse(raw)
	if err != nil {
		fmt.Printf("error parsing font: %v\n", err)
		return loadFallbackFont()
	}
	return font, nil
}

func loadFallbackFont() (font *truetype.Font, err error) {
	// Default font doesn't work on Darwin
	fmt.Println("using Monospace font")
	return truetype.Parse(mfont.Monospace())
}
