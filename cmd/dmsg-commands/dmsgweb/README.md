


```
$ go run cmd/dmsgweb/dmsgweb.go --help

	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌─┐┌┐
	 │││││└─┐│ ┬│││├┤ ├┴┐
	─┴┘┴ ┴└─┘└─┘└┴┘└─┘└─┘
DMSG resolving proxy & browser client - access websites and http interfaces over dmsg
.conf file may also be specified with
DMSGWEB=/path/to/dmsgweb.conf skywire dmsg web

Usage:
  dmsgweb

Available Commands:
  completion Generate the autocompletion script for the specified shell
  srv       Serve HTTP or raw TCP from local port over DMSG

Flags:
  -r, --addproxy string    configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)

  -D, --dmsg-disc string   dmsg discovery url
 (default "http://dmsgd.skywire.skycoin.com")
  -z, --envs               show example .conf file

  -f, --filter string      domain suffix to filter
 (default ".dmsg")         
  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ]

  -p, --port uints         port(s) to serve the web application
 (default [8080])          
  -x, --proxy string       connect to dmsg via proxy (i.e. '127.0.0.1:1080')

  -t, --resolve strings    resolve the specified dmsg address:port on the local port & disable proxy

  -c, --rt bools           proxy local port as raw TCP
 (default [false])         
  -e, --sess int           number of dmsg servers to connect to
 (default 1)               
  -s, --sk cipher.SecKey   a random key is generated if unspecified
 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -q, --socks uint         port to serve the socks5 proxy
 (default 4445)            
  -v, --version            version for dmsgweb

```

```
$ go run cmd/dmsgweb/dmsgweb.go srv --help
DMSG web server - serve HTTP or raw TCP interface from local port over DMSG
	.conf file may also be specified with DMSGWEBSRV=/path/to/dmsgwebsrv.conf skywire dmsg web srv

Usage:
  dmsgweb srv [flags]

Flags:
  -D, --dmsg-disc string   DMSG discovery URL
 (default "http://dmsgd.skywire.skycoin.com")
  -d, --dport uints        DMSG port(s) to serve
 (default [80])            
  -e, --dsess int          DMSG sessions
 (default 1)               
  -z, --envs               show example .conf file

  -l, --loglvl string      [ debug | warn | error | fatal | panic | trace | info ]

  -p, --lport uints        local application interface port(s)
 (default [8086])          
  -x, --proxy string       connect to DMSG via proxy (e.g., '127.0.0.1:1080')

  -c, --rt bools           proxy local port as raw TCP, comma separated
 (default [false])         
  -s, --sk cipher.SecKey   a random key is generated if unspecified
 (default 0000000000000000000000000000000000000000000000000000000000000000)
  -w, --wl strings         whitelisted keys for DMSG authenticated routes


```
