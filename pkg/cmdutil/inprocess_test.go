// Package cmdutil pkg/cmdutil/inprocess_test.go c0-com-util
package cmdutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func testTree(started chan<- struct{}) *cobra.Command {
	root := &cobra.Command{Use: "t", SilenceUsage: true, SilenceErrors: true}
	var upper bool
	echo := &cobra.Command{Use: "echo", Run: func(c *cobra.Command, args []string) {
		s := strings.Join(args, " ")
		if upper {
			s = strings.ToUpper(s)
		}
		fmt.Fprintln(c.OutOrStdout(), s) //nolint:errcheck
	}}
	echo.Flags().BoolVar(&upper, "upper", false, "")
	root.AddCommand(echo,
		&cobra.Command{Use: "exit", Run: func(*cobra.Command, []string) { Exit(3) }},
		&cobra.Command{Use: "bg", Run: func(c *cobra.Command, _ []string) {
			go Exit(4)
			<-c.Context().Done()
		}},
		&cobra.Command{Use: "boom", Run: func(*cobra.Command, []string) { panic("boom") }},
		&cobra.Command{Use: "err", RunE: func(*cobra.Command, []string) error { return fmt.Errorf("no") }},
		&cobra.Command{Use: "block", Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.OutOrStdout(), "blocking") //nolint:errcheck
			close(started)
			<-c.Context().Done()
		}},
	)
	return root
}

func TestRun(t *testing.T) {
	root := testTree(make(chan struct{}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // ends the bg command, still waiting after its Exit
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(ctx, root, args, strings.NewReader(""), &out, &errb)
		return code, out.String(), errb.String()
	}

	code, out, _ := run("echo", "--upper", "hi")
	require.Equal(t, 0, code)
	require.Equal(t, "HI\n", out)
	code, out, _ = run("echo", "hi")
	require.Equal(t, 0, code)
	require.Equal(t, "hi\n", out, "a flag one run set does not carry into the next")

	code, _, _ = run("exit")
	require.Equal(t, 3, code)
	code, _, _ = run("bg")
	require.Equal(t, 4, code, "Exit from a goroutine the command started ends the run")
	code, _, errOut := run("boom")
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "panic: boom")
	code, _, _ = run("err")
	require.Equal(t, 1, code)
}

// A long running command and another one run side by side, each with its own
// output, as a visor and a cli command do in one shell.
func TestRunOverlapping(t *testing.T) {
	started := make(chan struct{})
	root := testTree(started)
	ctx, cancel := context.WithCancel(context.Background())
	var blockOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- Run(ctx, root, []string{"block"}, strings.NewReader(""), &blockOut, &blockOut) }()
	<-started

	var out bytes.Buffer
	require.Equal(t, 0, Run(context.Background(), root, []string{"echo", "side"}, strings.NewReader(""), &out, &out))
	require.Equal(t, "side\n", out.String())

	cancel()
	select {
	case code := <-done:
		require.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the blocking run did not end")
	}
	require.Equal(t, "blocking\n", blockOut.String())
}

// Output written without a writer at hand reaches the run it belongs to: from
// the command's goroutine, from one it starts, and through the logger.
func TestRunStdio(t *testing.T) {
	root := &cobra.Command{Use: "t", SilenceUsage: true, SilenceErrors: true}
	say := func(_ *cobra.Command, args []string) {
		fmt.Fprintln(Stdout(), "own", args[0]) //nolint:errcheck
		done := make(chan struct{})
		go func() {
			fmt.Fprintln(Stdout(), "child", args[0]) //nolint:errcheck
			close(done)
		}()
		<-done
		logging.MustGetLogger("t").Warn("logged " + args[0])
	}
	// Two commands: one command's flags are package state, so it does not overlap itself.
	root.AddCommand(&cobra.Command{Use: "a", Run: say}, &cobra.Command{Use: "b", Run: say})
	var a, b, aerr, berr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		Run(context.Background(), root, []string{"a", "a"}, strings.NewReader(""), &a, &aerr)
	}()
	go func() {
		defer wg.Done()
		Run(context.Background(), root, []string{"b", "b"}, strings.NewReader(""), &b, &berr)
	}()
	wg.Wait()
	require.Equal(t, "own a\nchild a\n", a.String())
	require.Equal(t, "own b\nchild b\n", b.String())
	require.Contains(t, aerr.String(), "logged a")
	require.NotContains(t, aerr.String(), "logged b")
	require.Eventually(t, func() bool { return active.Load() == 0 }, 5*time.Second, 10*time.Millisecond)
	require.Same(t, Stdout(), io.Writer(os.Stdout), "outside a run it is the process's own")
}
