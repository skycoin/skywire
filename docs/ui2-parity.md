# ui2 parity with the Angular hypervisor UI

The server rendered UI at `/ui2/` (templ and htmx, `pkg/visor/ui2` and
`pkg/visor/hypervisor_handlers_ui2*.go`) is being brought level with the
Angular UI in `static/skywire-manager-src`. Angular stays the default until
this table has no gaps the owner cares about.

Status is **yes** (present), **partial** (some of it), **no** (missing) or
**n/a** (the Angular feature has no backend to call).
A missing visor tab renders a link to the same tab in the Angular UI.

## Home and fleet pages

| Angular route | Feature | ui2 | Notes |
|---|---|---|---|
| `/` start, `/login` | login, first account creation | yes | `ui2/login` |
| `/nodes/list` | visor list with nested sub-hypervisor visors | yes | `ui2/` |
| | search, online, offline, outdated and no-reward chips | yes | |
| | app filter, apps column, compact view | no | |
| | column sorting | yes | in the browser, since labels live there |
| | drag to reorder | no | |
| | edit label, remove visor, remove all offline | yes | labels in the same localStorage entry as Angular |
| | bulk select: restart, reward address, add, start and stop app | yes | |
| | update one or all visors | n/a | Angular calls visors/{pk}/update/..., which the hypervisor does not serve |
| `/nodes/rewards` | list with reward columns | no | |
| `/nodes/services-health` | deployment service health, RSN stats | no | |
| `/nodes/network` | network view from the uptime tracker | no | |
| `/nodes/visualizer` | network graph | partial | cosmos-go WebGL page, linked as tp-viz and route-viz, not reimplemented |
| `/nodes/resources` | CPU, memory, disk and traffic of every visor | no | |
| `/nodes/uptime` | uptime of every visor, version history | no | |
| `/nodes/transports` | transports across the fleet | no | |
| `/settings` | change password | no | |
| | refresh rate, theme, language | partial | theme and language in the top bar, list polls every 10 s |
| | updater config, auto update, reload worker, visor switcher | no | |
| `/settings/labels` | label list | no | |
| `/app/:name/:key` | full page app host | no | |
| `/vpn/...` | VPN client UI | no | |
| `/wallet` | wallet | no | the wallet tab links to the Angular one |
| | language list en, es, de, pt, zh | yes | |

## Visor tabs

| Angular tab | Feature | ui2 | Notes |
|---|---|---|---|
| info | overview, network, dmsg and hypervisor sections | yes | |
| | config view and edit, restart, shut down | yes | |
| | router settings (min hops, existing transports, local routes) | yes | |
| | update visor | n/a | no update route on the hypervisor |
| | label | no | |
| routing | rules list and delete, route groups, routing policies | yes | |
| | route find and calc | yes | |
| | mux route view | partial | links to route-viz |
| transports | list, search, add, delete, delete all | yes | |
| | autoconnect, public, persistent | yes | |
| | transport label, details dialog, sorting | no | |
| bandwidth | per transport snapshot and daily history | no | |
| uptime | uptime bars and service uptimes | yes | |
| apps | list, start, stop, autostart, settings, logs, help | yes | |
| | app catalog and add app | no | |
| | flags form, remove app, filter dialog | no | |
| chat | skychat | partial | iframes the Angular chat tab, so it still needs Angular |
| mail | mailbox, compose, settings | yes | |
| contacts | list, add, edit, delete, import | yes | |
| rewards | address, history, rules | yes | |
| dmsg | sessions, clients, connect all, session count | yes | Angular folds it into info |
| skynet | forwarded ports and forwards | no | |
| web-proxy | resolving proxy settings | no | |
| vpn | VPN client | no | |
| resources | host stats and resource monitor | no | |
| terminal | dmsgpty terminal | yes | embeds the hypervisor pty page |
| wallet | wallet | partial | links to the Angular wallet and embeds its node config page |
| logs | runtime logs with filter and search | yes | |
| settings | skyenv editor | yes | |
| | offline alerts (SMTP and push) | no | |
