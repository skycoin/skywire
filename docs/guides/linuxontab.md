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
- The full root filesystem for that checkout, for `?disk=full`. The lean image
  has no `curl`. Use the rootfs that matches the kernel in the checkout: a
  mismatched pair boots, then crashes the tab under load.
- `skywire` on this computer, and a Chromium-based browser.

```
git clone -b feature/linux-kernel-integration https://github.com/kilian-ai/linuxontab
curl -o linuxontab/shell/linux-dist/rootfs.ext4 https://next.linuxontab.com/linux-dist/rootfs.ext4
```

The rootfs is about 800 MB.

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

3. Open LinuxOnTab inside the desk. In the browser's developer console for
   that tab, run:

   ```js
   const f = document.createElement('iframe');
   f.id = 'lot';
   f.src = '/lot/wasm.html?disk=full&wisp=vnet:6001';
   f.style.cssText = 'position:fixed;inset:5%;width:90%;height:90%;z-index:99999;border:0';
   document.body.appendChild(f);
   ```

   It must be a frame of the desk: that is how it finds the tab's network. The
   guest reaches a shell in about half a minute.

4. In the guest:

   ```
   curl http://ifconfig.me/ip
   ```

   prints the exit's address, not this computer's. `https://` works too.

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

- **The tab dies during a download.** The rootfs does not match the kernel.
  Use the rootfs published for the checkout you have.

`curl` in the full image is a small Python program. Use `curl URL` or
`curl -o FILE URL`; it does not support every curl option.

## See also

- [browser-visor.md](browser-visor.md): how the tab's visor is put together
- [resolving-proxy.md](resolving-proxy.md): the SOCKS5 exits and resolvers
