package shell

import (
	"context"

	"github.com/0magnet/sh/v3/interp"

	"github.com/0magnet/websh/progressive"
)

func init() {
	applets["imgcat"] = applet{"show pictures in the terminal (-w width, -h height: N cells, Npx, N%)", runImgcat}
}

// runImgcat shows pictures in the text, as iTerm2's imgcat does: an inline
// image a terminal that shows pictures lays at the cursor, and others
// ignore. In websh they scroll with the text and stay in the scrollback.
func runImgcat(_ context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	width, height := "", ""
	var files []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "-w" || a == "-h") && i+1 < len(args):
			if a == "-w" {
				width = args[i+1]
			} else {
				height = args[i+1]
			}
			i++
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		fprintf(hc.Stderr, "usage: imgcat [-w width] [-h height] file...\n")
		return 2
	}
	code := 0
	for _, f := range files {
		b, err := ReadFile(s, hc, f)
		if err != nil {
			code = fail(hc, "imgcat", err)
			continue
		}
		Print(hc.Stdout, progressive.Image(Base(f), b, width, height)+"\n")
	}
	return code
}
