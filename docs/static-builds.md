### Static Build

You can statically compile all Skywire binaries. Install musl-tools with a package manager of your choice.

musl ports for Mac are not supported. 

To compile and install the binaries run:

```bash
# Static Build.
$ make build-static # installs all dependencies, build binaries and skywire apps

# Install statically compiled skywire-visor, skywire-cli and app CLI execs.
$ make install-static
```