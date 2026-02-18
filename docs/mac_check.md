# macOS Build & Lint Requirements

## Problem

When running `golangci-lint` on macOS for projects that include hardware wallet dependencies, you may encounter two CGO-related errors:

**Missing `hidapi` header:**
```
fatal error: 'hidapi/hidapi.h' file not found
```

**Missing `pkg-config` / `libusb`:**
```
could not import github.com/google/gousb (-: exec: "pkg-config": executable file not found in $PATH)
```

## Solution

Install the required system dependencies via Homebrew:

```bash
brew install hidapi libusb pkg-config
```

Then set the necessary environment variables:

```bash
export CGO_CFLAGS="-I$(brew --prefix hidapi)/include"
export CGO_LDFLAGS="-L$(brew --prefix hidapi)/lib"
export PKG_CONFIG_PATH="$(brew --prefix libusb)/lib/pkgconfig"
```

To make these permanent, add them to your shell profile (`~/.zshrc` or `~/.bash_profile`):

```bash
echo 'export CGO_CFLAGS="-I$(brew --prefix hidapi)/include"' >> ~/.zshrc
echo 'export CGO_LDFLAGS="-L$(brew --prefix hidapi)/lib"' >> ~/.zshrc
echo 'export PKG_CONFIG_PATH="$(brew --prefix libusb)/lib/pkgconfig"' >> ~/.zshrc
source ~/.zshrc
```