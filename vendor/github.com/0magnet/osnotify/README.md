# osnotify

Best-effort desktop notifications from a background, headless-capable Go
process. No dependencies — each platform drives a tool that is already there.

The point of this package is that **it treats notification text as data.**
Titles and bodies routinely come from somewhere untrusted (a chat peer, a
remote event), and the usual way these libraries work — pasting that text into
a shell line or into AppleScript source — turns a message into a command.

| Platform | Backend |
|---|---|
| Linux/BSD | `notify-send`, gated on `DISPLAY`/`WAYLAND_DISPLAY` |
| macOS | `osascript` running a **fixed** AppleScript |
| Windows | PowerShell toast, text inserted via `CreateTextNode` (XML-escaped) |
| everything else | no-op |

## Install

```
go get github.com/0magnet/osnotify
```

## Use

```go
if err := osnotify.Notify(osnotify.Notification{
    Title:   "Chat",
    Body:    "alice: hi",
    AppName: "MyApp",
}); err != nil && !errors.Is(err, osnotify.ErrUnavailable) {
    log.Printf("notify: %v", err)
}
```

- `Available() bool` reports whether a notification could be posted at all —
  cached, so a headless process does not re-probe on every message. Use it to
  pick a print-only fallback path.
- `ErrUnavailable` is returned when there is no desktop session. It is a
  sentinel precisely so a headless daemon can ignore it with `errors.Is`
  instead of logging a failure for every notification it sends.
- `AppName` and `Title` are per call, not package globals, so concurrent
  senders cannot relabel each other's notifications. Empty falls back to
  `DefaultAppName()`, the running program's own name.
- Bodies are sanitized: control characters stripped, capped at 200 runes.

## Why the text handling matters

Three concrete rules, each with a test:

1. **Linux.** Arguments are passed as discrete `argv` elements, never a shell
   line, and `--` terminates option parsing:
   ```go
   exec.Command("notify-send", "--app-name="+app, "--", n.Title, n.Body)
   ```
   Without the `--`, a body beginning with `-` is parsed by GLib as flags. That
   is not remote code execution, but it lets whoever wrote the message relabel
   the notification's app identity, set `-u critical -t 0` so it never expires,
   change the icon, or pass a bad flag so the notification is silently
   suppressed.

2. **macOS.** The AppleScript source is a constant. The untrusted text is read
   at runtime out of environment variables via `system attribute`, so it is
   never part of the script and cannot terminate a string literal.

3. **Windows.** Text goes in through `CreateTextNode`, so the toast XML cannot
   be broken out of.

`TestNotify_PassesTextAsData` locks all of this down with an adversarial
payload, and asserts no shell interpreter is ever invoked.

## Scope

This is deliberately small. No sound, no icons, no actions, no js/wasm — if you
want those, [`gen2brain/beeep`](https://github.com/gen2brain/beeep) is larger
and has them. Note that at the time of writing beeep's `notify-send` path
places untrusted title and message as leading positional arguments with no `--`
terminator, which is the injection above.

Extracted from [skywire](https://github.com/skycoin/skywire).
