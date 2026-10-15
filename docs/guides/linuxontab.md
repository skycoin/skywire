# LinuxOnTab over the browser visor

[LinuxOnTab](https://github.com/kilian-ai/linuxontab) runs a Linux system in a
browser tab. Run inside the Skywire desk, it can use the tab's own visor as its
network: the guest's connections and DNS lookups go through the visor's Wisp
server and leave through the visor's SOCKS5 exit, so the rest of the internet
sees an exit's address and never this computer's.

```
guest → LinuxOnTab Wisp client → tab visor Wisp server (vnet:6001)
      → skysocks-client → Skywire route → exit → internet
```

## What you need

- A LinuxOnTab checkout. Its `feature/linux-kernel-integration` branch accepts
  `?wisp=vnet:6001`.
- Optionally, the full root filesystem for that checkout, for `?disk=full`.
  The default lean image is small and installs programs on first use. Use a
  rootfs that matches the kernel in the checkout: a mismatched pair boots, then
  crashes the tab under load.
- `skywire` on this computer, and a Chromium-based browser.

```
git clone -b feature/linux-kernel-integration https://github.com/kilian-ai/linuxontab
# only for ?disk=full, about 800 MB:
curl -o linuxontab/shell/linux-dist/rootfs.ext4 https://next.linuxontab.com/linux-dist/rootfs.ext4
```

## Run it

1. Serve the desk with LinuxOnTab beside it. `--lot` serves the folder at
   `/lot/` and makes the desk cross-origin isolated, which LinuxOnTab needs.
   HTTPS is needed too.

   ```
   skywire cli hv serve -a 127.0.0.1:8446 --tls --lot ./linuxontab/shell
   ```

2. Open `https://127.0.0.1:8446/` and accept the certificate warning. The
   first visit fetches the visor module and starts a new visor in the tab. Wait
   until it has picked an exit. In the desk terminal:

   ```
   skywire cli visor state --select proxy --jq '.proxy.Tunnels[0].ExitPK'
   ```

   prints the exit's key once it has one, and `null` before that.

3. Open **linux** from the desk menu, or open the desk at
   `https://127.0.0.1:8446/?lot=1` to start it straight away. It runs in a
   window of the desk, which is how it finds the tab's network, and reaches a
   shell in about half a minute. It boots the lean image, which installs a
   program the first time you run it. `?lot=full` boots the full image instead,
   an 800 MB download.

4. In the guest:

   ```
   curl http://ifconfig.me/ip
   ```

   prints the exit's address, not this computer's. `https://` works too.

5. Mesh sites work by name in the guest too, over `http://`:

   ```
   curl http://rewards.dmsg/
   curl http://aka2cawifaqoqejwrsgqfdhrdmnjqucdw4tldpg3r7hitmttqszmw.skynet/
   ```

   The tab visor answers lookups for `.dmsg` and `.skynet` names itself, with
   an address from SkyDNS's range, and carries the connection over the mesh.
   The first connection to a `.skynet` site can take half a minute while its
   route is set up.

## Hosting it

`--lot` also takes the URL of a site that publishes LinuxOnTab, and proxies it
at `/lot/`. `--lot https://next.linuxontab.com` always serves what that site
runs, so its kernel and images match without a local copy. Its changes and
outages reach the desk at once.

Behind a proxy, give the desk its own browse origins for the mesh browser.
With `--lot` they carry the same isolation headers as the desk, so they must
not be shared with a desk that is not isolated.

```
skywire cli hv serve --addr 127.0.0.1:7997 --lot https://next.linuxontab.com --browse-origin 127.0.0.1:7996 --browse-suffix .linux.theskywirenetwork.net --v-origin https://linux.skywire.dev
```

Then route `*.linux.theskywirenetwork.net` to 127.0.0.1:7996.

Add `--lot-open` to start LinuxOnTab as soon as the desk opens, so a visitor
sees it without knowing the menu. A link such as `?dmsg=` still opens its own
site instead.

## When it does not work

- **`curl: … [Errno -3] Try again`, or `curl` hangs.** The tab's exit is not
  passing traffic. A new tab visor picks its exit by itself and can pick a
  poor one. Choose another in the desk terminal:

  ```
  skywire cli proxy list
  skywire cli proxy start --pk <exit-pk>
  ```

- **The tab reloads by itself when LinuxOnTab opens.** The desk is not
  cross-origin isolated, and LinuxOnTab reloads the top window to get there,
  which restarts the visor. Serve with `--lot`.

- **LinuxOnTab opens a window saying the page is not isolated.** The browser
  did not isolate the page even with `--lot`, and the desk does not start
  LinuxOnTab, since it would reload the page. Use a Chromium-based browser.

- **The tab dies during a download.** The rootfs does not match the kernel.
  Use the rootfs published for the checkout you have.

`curl` in the full image is a small Python program. Use `curl URL` or
`curl -o FILE URL`; it does not support every curl option. Exits differ too:
some relay HTTPS but not plain HTTP. If `https://` works and `http://` does
not, pick another exit as above.

## See also

- [browser-visor.md](browser-visor.md): how the tab's visor is put together
- [resolving-proxy.md](resolving-proxy.md): the SOCKS5 exits and resolvers
