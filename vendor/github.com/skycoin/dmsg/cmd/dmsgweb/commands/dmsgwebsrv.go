// Package commands cmd/dmsgweb/commands/dmsgwebsrv.go
package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

const dmsgwebsrvenvname = "DMSGWEBSRV"

var dmsgwebsrvconffile = os.Getenv(dmsgwebsrvenvname)

func init() {
	RootCmd.AddCommand(srvCmd)
	srvCmd.Flags().UintSliceVarP(&localPort, "lport", "l", scriptExecUintSlice("${LOCALPORT[@]:-8086}", dmsgwebsrvconffile), "local application http interface port(s)")
	srvCmd.Flags().UintSliceVarP(&dmsgPort, "dport", "d", scriptExecUintSlice("${DMSGPORT[@]:-80}", dmsgwebsrvconffile), "dmsg port(s) to serve")
	srvCmd.Flags().StringSliceVarP(&wl, "wl", "w", scriptExecStringSlice("${WHITELISTPKS[@]}", dmsgwebsrvconffile), "whitelisted keys for dmsg authenticated routes\r")
	srvCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsg.DiscAddr(false), "dmsg discovery url")
	srvCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", scriptExecInt("${DMSGSESSIONS:-1}", dmsgwebsrvconffile), "dmsg sessions")
	srvCmd.Flags().BoolSliceVarP(&rawTCP, "rt", "c", scriptExecBoolSlice("${RAWTCP[@]:-false}", dmsgwebsrvconffile), "proxy local port as raw TCP")
	if os.Getenv("DMSGWEBSRVSK") != "" {
		sk.Set(os.Getenv("DMSGWEBSRVSK")) //nolint
	}
	if scriptExecString("${DMSGWEBSRVSK}", dmsgwebsrvconffile) != "" {
		sk.Set(scriptExecString("${DMSGWEBSRVSK}", dmsgwebsrvconffile)) //nolint
	}
	pk, _ = sk.PubKey() //nolint
	srvCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	srvCmd.Flags().BoolVarP(&isEnvs, "envs", "z", false, "show example .conf file")

	srvCmd.CompletionOptions.DisableDefaultCmd = true
}

var srvCmd = &cobra.Command{
	Use:   "srv",
	Short: "serve http or raw TCP from local port over dmsg",
	Long: `DMSG web server - serve http or raw TCP interface from local port over dmsg` + func() string {
		if _, err := os.Stat(dmsgwebsrvconffile); err == nil {
			return `
	dmsenv file detected: ` + dmsgwebsrvconffile
		}
		return `
	.conf file may also be specified with
	` + dmsgwebsrvenvname + `=/path/to/dmsgwebsrv.conf skywire dmsg web srv`
	}(),
	Run: func(_ *cobra.Command, _ []string) {
		if isEnvs {
			envfile := srvenvfileLinux
			if runtime.GOOS == "windows" {
				envfileslice, _ := script.Echo(envfile).Slice() //nolint
				for i := range envfileslice {
					efs, _ := script.Echo(envfileslice[i]).Reject("##").Reject("#-").Reject("# ").Replace("#", "#$").String() //nolint
					if efs != "" && efs != "\n" {
						envfileslice[i] = strings.ReplaceAll(efs, "\n", "")
					}
				}
				envfile = strings.Join(envfileslice, "\n")
			}
			fmt.Println(envfile)
			os.Exit(0)
		}

		server()
	},
}

func server() {
	log := logging.MustGetLogger("dmsgwebsrv")
	if len(localPort) != len(dmsgPort) {
		log.Fatal(fmt.Sprintf("the same number of local ports as dmsg ports must be specified ; local ports: %v ; dmsg ports: %v", len(localPort), len(dmsgPort)))
	}

	seenLocalPort := make(map[uint]bool)
	for _, item := range localPort {
		if seenLocalPort[item] {
			log.Fatal("-lport --l flag cannot contain duplicates")
		}
		seenLocalPort[item] = true
	}

	seenDmsgPort := make(map[uint]bool)
	for _, item := range dmsgPort {
		if seenDmsgPort[item] {
			log.Fatal("-dport --d flag cannot contain duplicates")
		}
		seenDmsgPort[item] = true
	}

	ctx, cancel := cmdutil.SignalContext(context.Background(), log)

	defer cancel()
	pk, err = sk.PubKey()
	if err != nil {
		pk, sk = cipher.GenerateKeyPair()
	}
	log.Infof("dmsg client pk: %v", pk.String())

	if len(wl) > 0 {
		for _, key := range wl {
			var pk0 cipher.PubKey
			err := pk0.Set(key)
			if err == nil {
				wlkeys = append(wlkeys, pk0)
			}
		}
	}
	if len(wlkeys) > 0 {
		if len(wlkeys) == 1 {
			log.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
		} else {
			log.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
		}
	}

	dmsgC := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dmsg.DefaultConfig())
	defer func() {
		if err := dmsgC.Close(); err != nil {
			log.WithError(err).Error()
		}
	}()

	go dmsgC.Serve(context.Background())

	select {
	case <-ctx.Done():
		log.WithError(ctx.Err()).Warn()
		return

	case <-dmsgC.Ready():
	}

	var listN []net.Listener

	for _, dport := range dmsgPort {
		lis, err := dmsgC.Listen(uint16(dport))
		if err != nil {
			log.Fatalf("Error listening on port %d: %v", dport, err)
		}

		listN = append(listN, lis)

		dport := dport
		go func(l net.Listener, port uint) {
			<-ctx.Done()
			if err := l.Close(); err != nil {
				log.Printf("Error closing listener on port %d: %v", port, err)
				log.WithError(err).Error()
			}
		}(lis, dport)
	}

	wg := new(sync.WaitGroup)

	for i, lpt := range localPort {
		wg.Add(1)
		go func(localPort uint, rtcp bool, lis net.Listener) {
			defer wg.Done()
			if rtcp {
				proxyTCPConnections(localPort, lis, log)
			} else {
				proxyHTTPConnections(localPort, lis, log)
			}
		}(lpt, rawTCP[i], listN[i])
	}

	wg.Wait()
}

func proxyHTTPConnections(localPort uint, lis net.Listener, log *logging.Logger) {
	r1 := gin.New()
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())

	authRoute := r1.Group("/")
	if len(wlkeys) > 0 {
		authRoute.Use(whitelistAuth(wlkeys))
	}
	authRoute.Any("/*path", func(c *gin.Context) {
		targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%v%s?%s", localPort, c.Request.URL.Path, c.Request.URL.RawQuery)) //nolint
		proxy := httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL = targetURL
				req.Host = targetURL.Host
				req.Method = c.Request.Method
			},
			Transport: &http.Transport{},
		}
		proxy.ServeHTTP(c.Writer, c.Request)
	})
	serve := &http.Server{
		Handler:           &ginHandler{Router: r1},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("Serving HTTP on dmsg port %v with DMSG listener %s", localPort, lis.Addr().String())
	if err := serve.Serve(lis); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Serve: %v", err)
	}
}

func proxyTCPConnections(localPort uint, lis net.Listener, log *logging.Logger) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			return
		}

		go handleTCPConnection(conn, localPort, log)
	}
}

func handleTCPConnection(dmsgConn net.Conn, localPort uint, log *logging.Logger) {
	defer dmsgConn.Close() //nolint

	localConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		log.Printf("Error connecting to local port %d: %v", localPort, err)
		return
	}
	defer localConn.Close() //nolint

	copyConn := func(dst net.Conn, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err != nil {
			log.Printf("Error during copy: %v", err)
		}
	}

	go copyConn(dmsgConn, localConn)
	go copyConn(localConn, dmsgConn)
}

const srvenvfileLinux = `
#########################################################################
#--	DMSGWEB SRV CONFIG TEMPLATE
#--		Defaults shown
#--		Uncomment to change default value
#--		LOCALPORT and DMSGPORT must contain the same number of elements
#########################################################################

#--	DMSG port to serve
#DMSGPORT=('80')

#--	Local Port to serve over dmsg
#LOCALPORT=('8086')

#--	Number of dmsg servers to connect to (0 unlimits)
#DMSGSESSIONS=1

#--	Set secret key
#DMSGWEBSRVSK=''

#--	Whitelisted keys to access the web interface
#WHITELISTPKS=('')

#-- Proxy as raw TCP
#RAWTCP=('false')
`
