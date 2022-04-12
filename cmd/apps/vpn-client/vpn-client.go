package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	cc "github.com/ivanpirog/coloredcobra"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	serverPKStr string
	localPKStr  string
	localSKStr  string
	passcode    string
	killswitch  bool
)

func init() {
	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&serverPKStr, "srv", "q", "", "PubKey of the server to connect to")
	rootCmd.Flags().StringVarP(&localPKStr, "pk", "p", "", "Local PubKey")
	rootCmd.Flags().StringVarP(&localSKStr, "sk", "s", "", "Local SecKey")
	rootCmd.Flags().StringVarP(&passcode, "passcode", "r", "", "Passcode to authenticate connection")
	rootCmd.Flags().BoolVarP(&killswitch, "killswitch", "k", false, "If set, the Internet won't be restored during reconnection attempts")
}

var rootCmd = &cobra.Command{
	Use:   "vpn-client",
	Short: "Skywire VPN Client",
	Long: `
	┬  ┬┌─┐┌┐┌   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
	└┐┌┘├─┘│││───│  │  │├┤ │││ │
	 └┘ ┴  ┘└┘   └─┘┴─┘┴└─┘┘└┘ ┴ `,
	Run: func(_ *cobra.Command, _ []string) {
		if serverPKStr == "" {
			// TODO(darkrengarius): fix args passage for Windows
			//*serverPKStr = "03e9019b3caa021dbee1c23e6295c6034ab4623aec50802fcfdd19764568e2958d"
			fmt.Println("VPN server pub key is missing")
			os.Exit(1)
		}

		serverPK := cipher.PubKey{}
		if err := serverPK.UnmarshalText([]byte(serverPKStr)); err != nil {
			fmt.Printf("Invalid VPN server pub key: %v\n", err)
			os.Exit(1)
		}

		localPK := cipher.PubKey{}
		if localPKStr != "" {
			if err := localPK.UnmarshalText([]byte(localPKStr)); err != nil {
				fmt.Printf("Invalid local PK: %v\n", err)
				os.Exit(1)
			}
		}

		localSK := cipher.SecKey{}
		if localSKStr != "" {
			if err := localSK.UnmarshalText([]byte(localSKStr)); err != nil {
				fmt.Printf("Invalid local SK: %v\n", err)
				os.Exit(1)
			}
		}

		var directIPsCh, nonDirectIPsCh = make(chan net.IP, 100), make(chan net.IP, 100)
		defer close(directIPsCh)
		defer close(nonDirectIPsCh)

		eventSub := appevent.NewSubscriber()

		parseIP := func(addr string) net.IP {
			ip, ok, err := vpn.ParseIP(addr)
			if err != nil {
				fmt.Printf("Failed to parse IP %s: %v\n", addr, err)
				return nil
			}
			if !ok {
				fmt.Printf("Failed to parse IP %s\n", addr)
				return nil
			}

			return ip
		}

		eventSub.OnTCPDial(func(data appevent.TCPDialData) {
			if ip := parseIP(data.RemoteAddr); ip != nil {
				directIPsCh <- ip
			}
		})

		eventSub.OnTCPClose(func(data appevent.TCPCloseData) {
			if ip := parseIP(data.RemoteAddr); ip != nil {
				nonDirectIPsCh <- ip
			}
		})

		appClient := app.NewClient(eventSub)
		defer appClient.Close()

		fmt.Printf("Connecting to VPN server %s\n", serverPK.String())

		vpnClientCfg := vpn.ClientConfig{
			Passcode:   passcode,
			Killswitch: killswitch,
			ServerPK:   serverPK,
		}

		vpnClient, err := vpn.NewClient(vpnClientCfg, appClient)
		if err != nil {
			fmt.Printf("Error creating VPN client: %v\n", err)
		}

		var directRoutesDone bool
		for !directRoutesDone {
			select {
			case ip := <-directIPsCh:
				if err := vpnClient.AddDirectRoute(ip); err != nil {
					fmt.Printf("Failed to setup direct route to %s: %v\n", ip.String(), err)
				}
			default:
				directRoutesDone = true
			}
		}

		go func() {
			for ip := range directIPsCh {
				if err := vpnClient.AddDirectRoute(ip); err != nil {
					fmt.Printf("Failed to setup direct route to %s: %v\n", ip.String(), err)
				}
			}
		}()

		go func() {
			for ip := range nonDirectIPsCh {
				if err := vpnClient.RemoveDirectRoute(ip); err != nil {
					fmt.Printf("Failed to remove direct route to %s: %v\n", ip.String(), err)
				}
			}
		}()

		if runtime.GOOS != "windows" {
			osSigs := make(chan os.Signal, 2)
			sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
			for _, sig := range sigs {
				signal.Notify(osSigs, sig)
			}

			go func() {
				<-osSigs
				vpnClient.Close()
			}()
		} else {
			ipcClient, err := ipc.StartClient(skyenv.VPNClientName, nil)
			if err != nil {
				fmt.Printf("Error creating ipc server for VPN client: %v\n", err)
				os.Exit(1)
			}
			go vpnClient.ListenIPC(ipcClient)
		}

		if err := vpnClient.Serve(); err != nil {
			fmt.Printf("Failed to serve VPN: %v\n", err)
		}

	},
}

// Execute executes root CLI command.
func Execute() {
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

func main() {
	Execute()
}
