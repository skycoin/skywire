### Windows installer

Requires windows host + go-msi and its dependencies (wix, .Net 3.5 sp1)

**The path to the wix toolset needs to be added to the environmental variables on the system**

Compile the windows executables into the specified path

```
rm -rf build
mkdir -p build/amd64
go build -o build/amd64/skywire-visor.exe ../../cmd/skywire-visor/skywire-visor.go
go build -o build/amd64/skywire-cli.exe ../../cmd/skywire-cli/skywire-cli.go
go build -o build/amd64/apps/skysocks.exe ../../cmd/apps/skysocks/skysocks.go
go build -o build/amd64/apps/skychat.exe ../../cmd/apps/skychat/chat.go
go build -o build/amd64/apps/skysocks-client.exe ../../cmd/apps/skysocks-client/skysocks-client.go
go build -o build/amd64/apps/vpn-server.exe ../../cmd/apps/vpn-server/vpn-server.go
go build -o build/amd64/apps/vpn-client.exe ../../cmd/apps/vpn-client/vpn-client.go
cp -b skywire.bat build/amd64/skywire.bat
```

create the skywire windows installer

```
 go-msi make --msi skywire.msi --version 0.5.1 --arch amd64 --keep
```

double click the created installer to install skywire and test the installer function.

**Note: by default windows does not install executables into the PATH**

It is required to manually add the directory where the skywire executables are installed to the PATH, as was the case for the wix toolset.
