# Publish a site

Any visor can serve a website or other service to the rest of the network.
Visitors reach it by your visor's public key, over `.skynet` or `.dmsg`, with
no domain, no open port and no certificate on your side. The service itself
only needs to listen on localhost.

Find the name visitors will use:

```
skywire cli visor pk                  # your key, hex
skywire cli pk dnslabel <hex-pk>      # the same key in base32
```

Give out the base32 form. It works everywhere, including SkyDNS and HTTPS in
a browser. [mesh-addresses.md](mesh-addresses.md) explains both forms.

## A named site

```
skywire cli serve vhost add blog --to 127.0.0.1:8080
```

`http://blog.<base32-pk>.skynet/` now serves whatever listens on
`127.0.0.1:8080`, and so does the same name under `.dmsg`. The visor's own
landing page stays on the bare name. Add more sites the same way, each with
its own name.

To also answer for a real domain name, give it as an alias. Visitors then use
`http://blog.example.com.<base32-pk>.skynet/`:

```
skywire cli serve vhost add blog --to 127.0.0.1:8080 --alias blog.example.com
```

## Many sites behind one web server

If a web server such as Caddy or nginx already serves your sites by host
name, send it every name:

```
skywire cli serve vhost add '*' --to 127.0.0.1:80 --preserve-host
```

The `*` site takes every name no other vhost has, and `--preserve-host` sends
the name the visitor asked for, so the web server picks the site. A Caddy site
block for `status.skywire.dev` then also answers
`http://status.skywire.dev.<base32-pk>.skynet/`.

The landing page links each site the web server serves. With Caddy on the same
machine the visor reads them from Caddy's admin API, at its default address
`127.0.0.1:2019`. For another web server, name them with `--site`, which can
be repeated:

```
skywire cli serve vhost add '*' --to 127.0.0.1:80 --preserve-host --site example.com --site blog.example.com
```

## A port

```
skywire cli serve add 8092 --to 127.0.0.1:8092 --label status
```

Visitors open `http://<base32-pk>.skynet:8092/`. A port can carry any TCP
service, not only HTTP, and `--to` can name another machine on your network,
such as `192.168.1.20:5432`. `--label` lists it on your visor's landing page.

`serve add 80 --to …` sets the website of the bare name itself.

## The visor's own page

The landing page, `/health` and the visor's other pages are always at
`http://<base32-pk>.skynet/.skywire/`, even when the bare name shows a
website. They can also have a name or a port of their own:

```
skywire cli serve vhost add landing --visor-pages   # http://landing.<base32-pk>.skynet/
skywire cli serve add 8000 --visor-pages            # http://<base32-pk>.skynet:8000/
```

`http://<base32-pk>.skynet/.skywire/sites` lists the visor's sites as JSON.

## Who can reach it

Everyone, unless you say otherwise:

- `--whitelist <pk>,<pk>` on `serve add` or `serve vhost add` admits only
  those visors.
- `--inject-pk` on `serve add` passes the visitor's verified key to your
  service as a header, so it can decide per visitor. See
  [skynet-website-auth.md](skynet-website-auth.md).

## HTTPS

Nothing to set up on your side. A visitor whose proxy has the
[local CA](resolving-proxy.md#https) can open `https://<base32-pk>.skynet/`.
Their proxy ends the TLS and sends the request to your port 80 over the mesh,
which is already encrypted end to end.

## Check it

Test from another visor. On that machine:

```
skywire cli dmsg curl dmsg://<hex-pk>:8092/
curl -x socks5h://127.0.0.1:4445 http://blog.<base32-pk>.skynet/
```

## Manage it

```
skywire cli serve                 # ports
skywire cli serve vhost           # named sites
skywire cli serve rm 8092
skywire cli serve vhost rm blog
```

Both tables live in the visor and survive a restart and a regenerate.

## A static folder

To serve a folder of files without a web server:

```
skywire cli util serve -a 127.0.0.1:8080 ./site
```

It runs in the foreground. For a site that stays up, use a web server or a
service manager.
