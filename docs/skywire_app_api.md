# API for Skywire apps

## Type of apps
Skywire apps can be used in two ways.
1. As a Skywire Visor App
2. As an External App

### Skywire Visor App
A skywire visor app is started, stopped, monitored and controlled via the skywire visor. The app is controlled via the interface `Proc` which requires a `PROC_CONFIG` env variable that the app reads to
get the required info to connect to the visor via RPC. Example [app](../example/example-server-app/README.md)

### External App
An external app is used as a stand alone binary which is build to run on it's own and connects to the visor via a RPC connection just like a `skywire visor app`
but we have to manually configure it in the skywire visor. For that we have to register the app via the cli `skywire-cli visor app register -a <app-name>` and receive a Proc Key that
needs to be passed to the External app so that it can create a RPC connection with the skywire visor. Example [app](../example/example-client-app/README.md)

## API

### App client
In order to create a connection between the visor and the skywire/external app we use

For `skywire visor app` use
```
// NewClient creates a new Client, panicking on any error.
func NewClient(eventSubs *appevent.Subscriber) *Client {
	log := logrus.New()

	conf, err := appcommon.ProcConfigFromEnv()
	if err != nil {
		if procAddr == nil && procKey == nil {
			log.WithError(err).Fatal("Failed to obtain proc config.")
		}
		log.WithError(err).Warn("Failed to obtain proc config.")
		conf.ProcKey = appcommon.ProcKey{}
		conf.AppSrvAddr = *procAddr
		conf.ProcKey = *procKey
	}
	client, err := NewClientFromConfig(log, conf, eventSubs)
	if err != nil {
		log.WithError(err).Panic("Failed to create app client.")
	}
	return client
}
```
from `Package app pkg/app/client.go`
The params are
- `eventSubs *appevent.Subscriber`
    If the app needs a event subscriber then it can be created or else it can be passed as `nil`
    ```
    // NewSubscriber returns a new Subscriber struct.
    func NewSubscriber() *Subscriber {
        return &Subscriber{
            chanSize: subChanSize,
            m:        make(map[string]chan *Event),
            closed:   false,
        }
    }
    ```
    from `Package appevent pkg/app/appevent/subscriber.go`
    Event subscriber has methods such as
    - `OnTCPDial`
        OnTCPDial subscribes to the OnTCPDial event channel (if not already).
        And triggers the contained action func on each subsequent event.
    - `OnTCPClose`
        OnTCPClose subscribes to the OnTCPClose event channel (if not already).
        And triggers the contained action func on each subsequent event.
    - `Subscriptions`
        Subscriptions returns a map of all subscribed event types.
    - `Count`
        Count returns the number of subscriptions.
    - `PushEvent`
        PushEvent pushes an event to the relevant subscription channel.
    - `Close`
        Close implements io.Closer

For `external app` use
```
// NewClientFromConfig creates a new client from a given proc config.
func NewClientFromConfig(log logrus.FieldLogger, conf appcommon.ProcConfig, subs *appevent.Subscriber) (*Client, error) {
	conn, closers, err := appevent.DoReqHandshake(conf, subs)
	if err != nil {
		return nil, err
	}

	return &Client{
		log:     log,
		conf:    conf,
		rpcC:    appserver.NewRPCIngressClient(rpc.NewClient(conn), conf.ProcKey),
		lm:      idmanager.New(),
		cm:      idmanager.New(),
		closers: closers,
	}, nil
}
```
from `Package app pkg/app/client.go`
The params are
- `log logrus.FieldLogger`
    Pass a new logger with `log := logrus.New()`
- `conf appcommon.ProcConfig`
    We create a basic Proc Config with
    ```
    	procConfig := appcommon.ProcConfig{
            AppSrvAddr: *procAddr,
            ProcKey:    pKey,
        }
    ```
    and pass it
    - `procAddr`
        This is required and can be read form a flag in the app. This address is set in the visor config under `launcher/server_addr`.
        The app needs this to create a connection to the visor.
    - `procKey *appcommon.ProcKey`
        This is required and can be read form a flag in the app. The app needs to be registered to the visor first so that a RPC gateway will await for a connection from the app.
        The key is also needed for the initial handshake between the app and the visor. We get this with `skywire-cli visor app register -a <app-name>`
- `subs *appevent.Subscriber`
    Same as `eventSubs` from `skywire visor app`


### App client methods
The app client has the following methods.
- `Config`
    Config returns the underlying proc config.
- `SetDetailedStatus`
    SetDetailedStatus sets detailed app status within the visor.
- `SetConnectionDuration`
    SetConnectionDuration sets the detailed app connection duration within the visor.
- `SetError`
    SetError sets app error within the visor.
- `Dial`
    Dial dials the remote visor using `remote`. It accepts the param `appnet.Addr` which contains
    ```
    type Addr struct {
        Net    Type
        PubKey cipher.PubKey
        Port   routing.Port
    }
    ```
    the type of network to use (dmsg or skynet),
    the public key of the remote visor and
    the dmsg or skynet port to connect to on the remote visor.
    It returns a `conn net.Conn` that can be used to read and write to the connected dmsg/skynet app on the remote visor.
- `Listen`
    Listen listens on the specified `port` for the incoming connections.
- `Close`
    Close closes client/server communication entirely. It closes all open
    listeners and connections.

### Logging
- `Skywire visor app`
    For a skywire visor app all info logs should be logged with `fmt.Printf()` which writes to `os.Stdout` and errors with `print()` which writes to `os.Stderr`. This keeps the app logs clean as they are read byt the visor and displayed alongside visor logs.
- `External app`
    Any type of logging can be used.
