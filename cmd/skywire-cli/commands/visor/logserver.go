package clivisor

import (
	"context"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	dir            = skyenv.PackageConfig().LocalPath // local dir to serve via http
	dmsgDisc       = "http://dmsgd.skywire.skycoin.com"
	dmsgPort       = uint(80)
	pubkey, seckey = cipher.GenerateKeyPair() //nolint
)

func init() {
	RootCmd.AddCommand(logserverCmd)
	logserverCmd.Flags().SortFlags = false
	logserverCmd.Flags().StringVarP(&dir, "dir", "d", dir, "local dir to serve via http")
	logserverCmd.Flags().StringVarP(&dmsgDisc, "disc", "e", dmsgDisc, "dmsg discovery address")
	logserverCmd.Flags().UintVarP(&dmsgPort, "port", "p", dmsgPort, "dmsg port to serve from")
	logserverCmd.Flags().Var(&seckey, "sk", "dmsg secret key")
	logserverCmd.Flags().MarkHidden("sk") //nolint
}

var logserverCmd = &cobra.Command{
	Use:   "logserver",
	Short: "log server",
	Long: `dmsghttp log server

Serves the local folder via dmsghttp`,
	Run: func(cmd *cobra.Command, args []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		log := logging.MustGetLogger("dmsghttp-logserver")
		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		if !skyenv.IsRoot() {
			log.Fatal("Log server is designed to run as root.")
		}
		log.WithField("config filepath", skyenv.SkywirePath+"/"+skyenv.Configjson).Info()

		conf, err := visorconfig.ReadFile(skyenv.SkywirePath + "/" + skyenv.Configjson)
		if err != nil {
			log.WithError(err).Fatal("Failed to read in config.")
		}

		seckey = conf.SK
		pubkey, err := seckey.PubKey()
		if err != nil {
			log.WithError(err).Fatal("bad secret key.")
		}
		c := dmsg.NewClient(pubkey, seckey, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dmsg.DefaultConfig())
		defer func() {
			if err := c.Close(); err != nil {
				log.WithError(err).Error()
			}
		}()
		go c.Serve(context.Background())
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn()
			return
		case <-c.Ready():
		}
		lis, err := c.Listen(uint16(dmsgPort))
		if err != nil {
			log.WithError(err).Fatal()
		}
		go func() {
			<-ctx.Done()
			if err := lis.Close(); err != nil {
				log.WithError(err).Error()
			}
		}()
		srv := &http.Server{
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
			Handler:      http.FileServer(http.Dir(dir)),
		}
		log.WithField("dir", dir).
			WithField("dmsg_addr", lis.Addr().String()).
			Info("Serving...")
		log.Fatal(srv.Serve(lis))
	},
}
