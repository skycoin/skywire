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
| `/nodes/services-health` | deployment service health, /health payload, RSN stats | yes | `ui2/services` |
| `/nodes/network` | network view from the uptime tracker | no | |
| `/nodes/visualizer` | network graph | partial | cosmos-go WebGL page, linked as tp-viz and route-viz, not reimplemented |
| `/nodes/resources` | CPU, memory, disk and traffic of every visor | yes | `ui2/resources` |
| `/nodes/uptime` | uptime of every visor | yes | `ui2/uptime` |
| | version history chart | no | |
| `/nodes/transports` | transports across the fleet | no | |
| `/settings` | change password | yes | `ui2/settings` |
| | refresh rate, theme, language | yes | the refresh rate sets the visor list poll |
| | updater config | n/a | only feeds the update action, which has no backend |
| | auto update, reload worker | n/a | the Angular in-browser visor's service worker |
| | visor switcher row | n/a | an Angular navigation option |
| `/settings/labels` | label list, edit, delete | yes | on `ui2/settings` |
| `/app/:name/:key` | full page app host | no | |
| `/vpn/...` | VPN client UI | no | |
| `/wallet` | wallet | yes | the wallet tab frames skycoin-web's own ui2 at `wallet/ui2/` |
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
| bandwidth | per transport snapshot and daily history | yes | paged 50 to a page with search; daily history loads when a row opens |
| uptime | uptime bars and service uptimes | yes | |
| apps | list, start, stop, autostart, settings, logs, help | yes | |
| | app catalog, add app, add another instance | yes | `apps/catalog` |
| | flags form | yes | "Edit flags" in an app's settings |
| | filter dialog, multi select actions | no | |
| chat | direct messages: peers, history, send, reply, delete | yes | polls every 3 s instead of SSE; read receipts not shown |
| | rooms: list, messages, send, create, join, invite, add member | yes | moderation (ban, mute, approve) not ported |
| | voice calls, skychat password | no | |
| mail | mailbox, compose, settings | yes | |
| contacts | list, add, edit, delete, import | yes | |
| rewards | address, history, rules | yes | |
| dmsg | sessions, clients, connect all, session count | yes | Angular folds it into info |
| skynet | forwarded ports and forwards | no | |
| web-proxy | resolving proxy status, on/off, upstream | yes | |
| vpn | VPN client | no | |
| resources | host stats and resource monitor | partial | host, process and runtime figures, no history charts |
| terminal | dmsgpty terminal | yes | embeds the hypervisor pty page |
| wallet | wallet | yes | frames skycoin-web's ui2 when the app runs, else says how to start it |
| logs | runtime logs with filter and search | yes | |
| settings | skyenv editor | yes | |
| | offline alerts (SMTP and push) | no | |
