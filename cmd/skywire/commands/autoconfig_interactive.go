package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigui"
)

var (
	autoconfigIPort int
	autoconfigIAddr string
)

func init() {
	c := &cobra.Command{
		Use:     "i",
		Aliases: []string{"interactive"},
		Short:   "Edit the autoconfig settings in a form",
		Long: `Edit every autoconfig setting in a terminal form, or with --port N in a
local web page. Saving runs the same autoconfig command as the flags do.
The web page needs the one-time token printed at start.`,
		Args: cobra.NoArgs,
		Run:  autoconfigInteractiveRun,
	}
	c.Flags().IntVar(&autoconfigIPort, "port", 0, "serve the form as a web page on this port instead of the terminal")
	c.Flags().StringVar(&autoconfigIAddr, "addr", "127.0.0.1", "address the web page binds to")
	autoconfigCmd.AddCommand(c)
}

// applyAutoconfig runs this binary's autoconfig with the given flags, the same
// as typing them, and returns what it printed.
func applyAutoconfig(args []string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	cmd := exec.Command(exe, append([]string{"autoconfig"}, args...)...) //nolint:gosec
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	return buf.String(), err
}

func autoconfigSkyenvPath() string {
	if p := resolveConfig().skyenvPath; p != "" {
		return p
	}
	return defaultSkyenvPath()
}

func autoconfigInteractiveRun(_ *cobra.Command, _ []string) {
	path := autoconfigSkyenvPath()
	if autoconfigIPort != 0 {
		if err := autoconfigServeWeb(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	m := autoconfigui.NewModel(autoconfigcmd.Describe(), path)
	act, err := autoconfigui.RunTUI(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch act {
	case autoconfigui.ActionPrint:
		cmd, err := m.Command()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(cmd)
	case autoconfigui.ActionApply:
		args, _ := m.Args() //nolint:errcheck // validated by the form
		if len(args) == 0 {
			fmt.Println("no changes")
			return
		}
		out, err := applyAutoconfig(args)
		fmt.Print(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func autoconfigServeWeb(path string) error {
	token, err := autoconfigui.NewToken()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	w := autoconfigui.NewWeb(token, path, applyAutoconfig)
	addr := autoconfigIAddr + ":" + strconv.Itoa(autoconfigIPort)
	return w.Serve(ctx, addr, func(url string) {
		fmt.Printf("autoconfig form for %s\nopen %s\n", path, url)
	})
}
