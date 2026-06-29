@Echo Off
:: skywire-autoconfig.bat — Windows config setup, the rough equivalent of the
:: Linux package post_install (skywire autoconfig). It is run in TWO places:
::
::   1. By the MSI as a deferred CustomAction at INSTALL time (Product.wxs),
::      so a fresh install has skywire.conf + skywire-config.json immediately,
::      without the operator having to launch first. This is the Windows
::      analogue of the deb/arch postinstall.
::   2. By skywire.bat at LAUNCH, so manual / direct launches still self-configure.
::
:: It is idempotent: the env-file template is written only if missing (never
:: clobbering operator edits), and the visor config is (re)generated through
:: `skywire autoconfig`, which runs `config gen -r` — retaining the existing
:: secret key + hypervisor PKs — so re-running it (install, upgrade, or every
:: launch) converges on a current config without losing identity.
::
:: IMPORTANT: at MSI-install time INSTALLDIR is NOT yet on PATH (the PATH
:: environment component is applied separately), so this script calls the
:: installed skywire.exe by ABSOLUTE path (%~dp0skywire.exe), never bare
:: "skywire". %~dp0 is the directory this script lives in = the install dir.

cd /d "%~dp0"

:: Windows-side equivalent of Linux's /etc/skywire.conf. Lives under
:: %ProgramData% (typically C:\ProgramData\Skywire\) so operator edits survive
:: MSI upgrades — the MSI installs into Program Files but doesn't touch
:: ProgramData. `defaultSkyenvPath` in cmd/skywire/commands/autoconfig.go
:: points at the same path; the SKYENV var here makes cli + autoconfig pick
:: the file up regardless of CWD (and is inherited by the visor when
:: skywire.bat calls this script before launching).
set "SKYENV=%ProgramData%\Skywire\skywire.conf"
if not exist "%ProgramData%\Skywire\" (
    mkdir "%ProgramData%\Skywire" >nul 2>&1
)

:: Generate the env-file template ONLY if missing. -p forces package-mode
:: defaults (paths under C:\Program Files\Skywire), -q renders the template,
:: -Q writes it to the file. Mirrors the deb/arch
:: `[[ ! -f /etc/skywire.conf ]] && skywire cli config gen -pqQ ...` pattern.
if not exist "%SKYENV%" (
    "%~dp0skywire.exe" cli config gen -pqQ "%SKYENV%"
)

:: (Re)generate the visor config through the SINGLE entry point — `skywire
:: autoconfig` — never `cli config gen` directly (matches the Arch/AUR
:: post_install). autoconfig runs `config gen -r` (regenerate, retaining the
:: existing secret key + hypervisor PK list). `-r` is safe whether or not a
:: config already exists, so this runs UNCONDITIONALLY — fresh install, upgrade,
:: and launch alike — with no install/upgrade marker to track. It reads %SKYENV%
:: for PKGENV / HYPERVISORPKS / DISABLEPUBLICAUTOCONN / etc.; PKGENV=true (written
:: into the template above) resolves the config to
:: C:\Program Files\Skywire\skywire-config.json — the exact path the shortcut
:: launches with. autoconfig calls its own binary by absolute path internally, so
:: it works even though the install dir is not yet on PATH at MSI-install time.
:: --no-vpnserver persists VPNSERVER=false (replaces the old `--disableapps
:: vpn-server`). Deployment service URLs come from the embedded dmsg-only services
:: config (the single source of truth), so no -S / -D files are needed.
"%~dp0skywire.exe" autoconfig --no-vpnserver
