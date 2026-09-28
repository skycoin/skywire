# G0 evidence — dropped-module consumer audit (playbook item 0.2)

The mobile module set (`pkg/visor/init_modules_mobile.go`) registers 27 of
the visor's modules as `vinit.DoNothing`. Every piece of shared state those
init functions would have set — `*Visor` fields, callbacks installed on
other objects, close-stack entries, health flags, ready channels — stays at
its zero value on the phone. For each dropped module, every reader of that
state was read and classified. Line numbers are from the tree at the time of
the audit (2026-09-28).

"Already exercised" means the phone's config (`phone-profile.json`) makes
the full build's init return early before setting the state, so Android
phones already run the zero-value path today.

**Result: no reader that is not nil-safe, and nothing that hangs.** Only
`vis`/`hv` and `tm` are ever waited on (visor.go, api_suspend.go); a
DoNothing dependency closes at once; none of the dropped inits closes a
channel another module waits on (`dmsgHTTPReady`, `dmsgTracker.ready`,
`stun.ready` are closed by kept modules).

**Change made from the audit:** `sky_forward_conn` is *kept* (the playbook
listed it as droppable). It carries the client apps' direct 0-hop dial and
the inbound direct/skynet-forward handlers; without it every
vpn/socks/chat dial goes through route setup (+1–7 s) and a peer dialing the
phone waits ~5 s for a fallback. 27 modules are dropped, not 28.

## Group A — dmsg-side modules (dmsg_pty, dmsghttp_logserver, system_survey, dmsg_server, dmsg_server_latency, self_probe, skynet_ports)

Result: every reader nil-safe; no hang (dmsgHTTPReady, dmsgTracker.ready, stun.ready are closed by kept modules; maps/mutexes these inits write are built in NewVisor, visor.go:806-836).

- dmsg_pty (init_dmsg.go:862): sets v.dmsgPty, launcher "pty", v.dmsgWL, close-stack dmsgscp.serve/router.serve. Phone today: no pty key → returns at :866 (already exercised). Readers init_apps.go:92, api_services.go:622, api_visor_cat.go:163 — guarded.
- dmsghttp_logserver (init_dmsg.go:611): sets v.logServer.api/localAPI, services.Register(80), website handler, dmsg+skynet :80 listeners, v.allowed.ports, close-stack. NEW zero path (runs on phones today). Readers api_network.go:156 (guard :158), cxo_user_feeds.go:446 (guard :448), init_uptime.go:42-43 (guards :45/:48), init_stats.go:178 (dropped), services.Get(80)/SelfDial (error when unregistered), ListTCPPorts (plain map) — all safe. Change: the phone no longer answers dmsg/skynet :80 (landing page, /health, /node-info, /stats, /services, /pty): remote health fetches, `cli log` and reward pulls against a phone get "no listener".
- system_survey (init_services.go:52): writes v.survey.*; without reward.txt the phone's survey is already the zero value (reward_push.go:83). Readers api_visor.go:209-217 gated by pushAttempted. SetRewardAddress (api_visor.go:532) still runs a one-shot survey nobody consumes on mobile (optional cleanup).
- dmsg_server (init_dmsg_server.go:59, !mobile): phone config has no dmsg.server → returns at :60-62 (already exercised). Readers api_state_roles.go:46/47 guarded. Not reachable on phone config: a stray dmsg.server.enabled would still carve a transport-port cmux branch nothing accepts (init_transport.go:643/667) — optional hardening.
- dmsg_server_latency (init_dmsg.go:1170): NEW zero path. Reader DMSGServers api_dmsg.go:554-575 — missing key reads 0, sorted as unmeasured. Change: latency 0 on Home (HomeScreen.kt:464 hides it: takeIf{>0}); hourly self-ping traffic stops.
- self_probe (init_selfprobe.go:52): no shared state; NEW (runs today). Change: no 60 s self-dial and no automatic ForceReconnect when the phone's own dmsg listeners go unreachable; the manual DmsgReconnect RPC (api_dmsg_diag.go:53, what Android's NetworkWatcher calls on a network move) stays. One session reaper that could hang up calls is gone.
- skynet_ports (init_skynet_ports.go:33): NEW zero path. Readers stopDmsgForwarder api_network.go:715, isPortRegistered, ListTCPPorts, router.offerAcceptDatagram (datagram_setup.go:112, non-blocking send then drop) — safe. Change: persisted DMSG/UDP forwarded ports are not restored at boot (the phone has none).

NOT-nil-safe / hang findings: none.

## Group B — embedded/browse modules (embedded_dmsgweb, embedded_wisp, dmsg_forward_proxy, embedded_skynetweb, embedded_resolvers, mesh_proxy, embedded_skymail_bridge)

Result: every reader nil-safe; no hang (only vis/hv and tm are waited on; launch's deps on the three resolver modules are DoNothing and close at once; the other four have no dependents).

- embedded_dmsgweb (embedded_dmsgweb.go:479): sets v.embeddedDmsgWeb, launcher entry "dmsgweb", close-stack dmsg_web_identity. Phone today: no dmsg_web section → returns at :480-483 (zero path already exercised). Readers: init_apps.go:109, api_proxies.go:37/77/174/207, embedded_proxystatus.go:242/364, embedded_proxystatus_skywire.go:192, api_config_fields.go:811, launcher GetApp callers — all guarded. Runtime toggle still builds lazily. Divergence: a persisted dmsg_web.enable no longer autostarts after restart (status Enabled=true Running=false).
- embedded_wisp (embedded_wisp.go:181): returns at :182-184 whenever GOOS != js — every native visor. v.embeddedWisp has no readers.
- dmsg_forward_proxy (forward_proxy.go:42): no shared state; returns at :43-45 on the phone config.
- embedded_skynetweb (embedded_skynetweb.go:574): v.embeddedSkynetWeb + launcher entry "skynetweb". Phone today returns at :575-578. Readers: init_apps.go:121, api_proxies.go:38/116/182/222/318, embedded_proxystatus.go:237/412, embedded_proxystatus_skywire.go:188, api_config_fields.go:811 — all guarded.
- embedded_resolvers (embedded_resolvers.go:56): resolvers empty → returns at :57-59. Only reader init_apps.go:137 ranges a nil slice.
- mesh_proxy (meshproxy.go:685): RUNS on phones today (browse_origin enabled by default by config gen). No shared state: one goroutine serving 127.0.0.1:8461 (real-origin browse proxy + status pages). Only pointer to it is browseOriginInjectJS (hypervisor_handlers_browse.go, !mobile). Change: 8461 no longer listens; nothing in the mobile build or the Android app uses it. browse_origin.enable=true stays as dead config (optional: --no-browse-origin in genArgs).
- embedded_skymail_bridge (embedded_skymail_bridge.go:185): returns at :186-189 on the phone config. Readers api_proxies.go:39/140 guarded.

Side note: api_proxies.go calls newEmbeddedDmsgWeb/newEmbeddedSkynetWeb/newEmbeddedSkymailBridge directly and meshproxy.go exports ServeMeshProxy, so those packages stay linked; the no-op modules remove little code.

NOT-nil-safe / hang findings: none.

## Group C — infra/services (embedded_tps, embedded_route_setup, ui_server, node_health, skymail, sky_forward_conn, coin_nodes)

Result: every reader nil-safe; no hang. The only blocking waits are the modules' own (skyFwd on tpM.Ready, skymail on dmsgC.Ready, embTPS/embRouteSetup on their private dmsg clients).

- embedded_tps (init_transport.go:742): v.embeddedTPS + private dmsg client. Phone today: no tps_sk → returns :744-746 (already exercised). Readers api_tps.go:28/43/69/85, api_dmsg.go:679 (guard :692), api_state.go:179, init_services.go:1299/1327/1347/1376/1436 — guarded.
- embedded_route_setup (init_router.go:439): phone today returns :441-443 (no route_setup_sk). initRouter assigns the interface only when non-nil (init_router.go:192-195), so embeddedRSN stays a true nil interface; NewSetupNodeDialerFull handles it (wrappers.go:134/180) — the phone's behaviour today. Readers api_dmsg_diag.go:18/32/59, api_dmsg.go:678, api_state.go:180, api_tps.go:155/165 — guarded.
- ui_server (init_services.go:1408): no Visor fields; phone today returns :1410-1413 (ui_server.enable false). v.ui is RPC-managed and independent.
- node_health (init_router.go:530): v.nodeHealthTracker — NEW zero path (exists on phones today). Readers api_tps.go:119/128 fall back to config order; :137/:145 return "node health tracker not initialized". RPC-only (GetTPSHealth/GetRSNHealth); no HTTP or Android route.
- skymail (skymail.go:165): v.mail.* — NEW (mailbox runs on phones today: skymailEnabled defaults true). Every Mail* goes through mailbox() (skymail.go:289) → errMailboxOff when rt nil; MailStatus handles nil. Change: port 25 not served; MailStatus Enabled:true Running:false; no HTTP/Android route.
- sky_forward_conn (init_services.go:341) — runs on phones today, no config gate. Sets skynet listeners 57/59, v.skynetFwdMux + tpM.SetSkynetForwardHandler, v.appDirectMux + tpM.SetAppDirectHandler, the skynet networker's SetAppDirectMux (skywire_networker.go:372), mux policy hooks. Readers all nil-safe (managed_transport.go:650/655/1605/1608, manager.go:852-857, skywire_networker.go:268/542, api_state_diag.go:95-99, api_routing.go:475, dmsg_over_skynet.go:30, embedded_skynetweb.go:324-326, skynet_relay_dial.go:54-58/230-233). BUT behaviour for the phone's apps: (a) outbound vpn/socks/chat dials lose the direct 0-hop shortcut over an existing non-dmsg transport and always go through RSN route setup (+1-7 s); (b) dmsg relay-carrier dials fall back to route setup (≤30 s); (c) inbound direct/skynet-forward packets are dropped — a peer dialing the phone's skychat waits ~5 s before falling back, the phone stops being a 1-hop relay. → DECISION: sky_forward_conn is KEPT in the mobile graph (the playbook's drop list was wrong about it; it is part of the client apps' data path).
- coin_nodes (init_coinnode.go:108): phone today returns :109-111 (no coin_nodes). No readers outside the file.

NOT-nil-safe / hang findings: none. Behaviour finding acted on: keep sky_forward_conn.

## Group D — registration/telemetry (uptime_tracker, public_visor, transportable, stats, registration_cxo, ar_bind_cxo, sd_reg_cxo)

Result: every reader nil-safe; no hang. Every field these set is built eagerly in NewVisor or nil-checked by each reader.

- uptime_tracker (init_services.go:61): isServicesHealthy/isUptimeTrackerHealthy (built in NewVisor, init "connecting"). Runs on phones today. Reader Health() api_visor.go:249-256 nil-checked. Change: uptime_tracker_health stays "connecting"; the TPD UpdateVisorUptime heartbeat stops.
- public_visor (init_transport.go:958): always does a synchronous SD DELETE of type=visor (≤5 s); updater only when is_public (phone: false → returns :964, updater already nil). Reader init_transport.go:621-626 checks nil. Change: no boot-time SD DELETE (up to ~5 s less before the API comes up); setting is_public becomes a no-op on mobile.
- transportable (init_transport.go:1034): health flags + a dmsg self-transport 1 min after boot then every 5 min. Runs today. Reader api_visor.go:261-262. Change: transportability_health stays "connecting".
- stats (init_stats.go:40): tpM.SetTPDLeafPublisher, v.systemCXOPub, v.tplistCXOPub, v.statsTracker, tpdLeafPubReason, tpdAnnounce counters, gated CXO feeds, lsAPI.SetStatsReader. Runs fully on phones today (no stats section) — the zero path is the same one stats.disabled visors take. Readers: transport/manager.go:540/594/708/1860 (nil → HTTP DeleteTransports/RegisterTransportsV3), init_transport.go:1194 (guarded; HTTP reconcile runs), cxo_user_feeds.go:237-251/346/364, api_state.go:177, api_local_transport_stats.go:30, api_local_uptime_stats.go:59, api_transport.go:513, logserver/stats.go, cxo_feed_allowlist.go:86/103 — all guarded. initCXOUserFeeds reads nothing from stats.
- registration_cxo (init_registration_cxo.go:49): v.regCXOPub, dmsgC.SetEntryPublishHook, SetCXOKeepaliveHealthyFunc. Runs today. Readers cxo_user_feeds.go:376, dmsg/entity_common.go:1008/1489 — guarded (nil func → base interval).
- ar_bind_cxo (init_ar_bind_cxo.go:44): arClient.SetBindPublishHook. Reader addrresolver/client.go:197 guarded. HTTP/UDP binds unchanged → stcpr/sudph reachability unchanged.
- sd_reg_cxo (init_sd_reg_cxo.go:197): v.sdRegCXOPub + sdEntryMirror.setPublisher. The mirror stays inert (PutEntry/DelEntry nil-check, flushLocked returns when pub nil); on the phone it carries nothing anyway (client apps only, is_public false).

NOT-nil-safe / hang findings: none.
Cosmetic (non-blocking): `visor state --select cxo` says "stats module has not completed init"; ListCXOFeeds lists the "stats" system feed nothing serves.
Behaviour changes: services_health set only by reconcileTPD (autoconnect off) — "connecting" until the first TPD query; transports registered with TPD over HTTP every 90 s + debounced nudges (no bandwidth/latency telemetry in TPD); no uptime heartbeat → a phone with zero transports drops out of TPD /uptimes; dmsg-discovery keepalive back to 5 min (from 30 min while CXO was healthy; 60 min TTL unaffected); local-transport-stats/local-uptime-stats empty (not used by the app); four fewer CXO publishers/listeners and 30 s announce loops, no stats.db or 1-min sampler.

