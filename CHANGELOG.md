# Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

updates may be generated with `scripts/changelog.sh <PR#lowest> <PR#highest>`

## Unreleased

Develop after v1.3.94. Headlines: **routes through a folded dmsg-server visor work again** — every dmsg client built on the seeded discovery answered a lookup for such a key from its server-only seed and never asked the live discovery, so the route setup nodes failed id reservation for every hop through one of the seven folded servers and opened their circuit breakers (#4923); **a peer that re-dials after a restart keeps its new transport** instead of having it reset by the old connection's close (#4925); **`route settings`** shows and sets the router knobs, transport preference included (#4920); **`visor state --select diag`** carries the transport open/close event ring with close reasons (#4919); **sudph transports are kept alive** and autoconnect tries stcpr and squicr before sudph (#4921); **`proxy start --route`** pins a session to exactly the supplied routes (#4924); **`loadtest serve`** certifies transfers by hash so the mux campaign's rows are verified (#4917). The live route-multiplexing campaign and its intended default policy are in `docs/design/`; measurements land in `bench/`.

The same day, measured on the live rig and fixed: **a v1.3.94 client culls every dmsg-over-QUIC session about every two minutes** because the liveness ping had no QUIC branch (#4926), on top of the ALPN collision (#4916) — both are reasons for a point release; **the QUIC transport ran on quic-go's 768 KB default receive window**, which caps a 150 ms path at 5 MB/s (#4929); **a pinned proxy exit is never rotated** by the visor's auto-exit loop, which had been moving operators' proxies to random exits and persisting the change (#4931); **`proxy start` watches the app it started** rather than the default one (#4933); **`visor state --select diag` carries mux leg events with reasons** and the last close per transport (#4930, #4928); **a transport's write no longer blocks its reads** (#4932); **a setup node releases a half-open probe** when the request that took it ends, instead of refusing every route through that hop for 30 minutes (#4935). The campaign's reference measurements are in `bench/2026-09-16/4e052d65b/`.

Also that day the DE exit visor turned out to have been OOM-killed seven times in one day: #4252's startup GC/compaction copied the live set of an 18 GB `cxo-stats/cxds.db` inside one bbolt write transaction, whose dirtied pages all stay in memory until commit, so the copy never finished and the file never shrank (#4943 copies in 32 MiB batches). And a two-minute blackout on a pinned two-hop session — the exit's reorder frontier stuck at one sequence for 81 s while the SACKs it kept sending went unanswered — got the sender-side witness it lacked: `visor state --select mux_route_groups` and `proxy mux info` now carry a `recovery` object (retransmit window, SACK feedback, reorder frontier, wedge counters) and the leg events gained `reorder_wedge` / `reorder_wedge_cleared` (#4944).

Then the mux sets themselves, run through the default proxy instance: `--tunnels N` had never dialed a route group (#4945), and once it did the candidate-route race handed every tunnel the same first hop (#4946, with #4947 recording each diversify dial's route choice on the group). The three-leg and five-leg groups exposed a spurious-retransmit storm — the exit re-sent 37,207 frames for 20,304 sent — because the ack-delay estimate the retransmit threshold floors on never samples a retransmitted frame; a DSACK now feeds the late original's delay into it (#4948).

-   fix(router): a diversify dial ranks by carrier class first, path latency second  [#4991](https://github.com/skycoin/skywire/pull/4991)
-   feat(skysocks,router): a dead active tunnel is replaced from the standby pool in the same tick, and tunnel switches are events  [#4991](https://github.com/skycoin/skywire/pull/4991)
-   ci(release): a release build fails on a dirty tree, the version is stamped from the tag, and a PR cannot add a large blob  [#4990](https://github.com/skycoin/skywire/pull/4990)
-   feat(cli): the loadtest sink accepts an upload as acked, resumable chunks  [#4988](https://github.com/skycoin/skywire/pull/4988)
-   feat(skysocks,router): sibling tunnels beyond the active set are dialed and held in standby  [#4986](https://github.com/skycoin/skywire/pull/4986)
-   fix(router): a sibling route is ranked by its full path latency, and LAN neighbours are not diversity  [#4983](https://github.com/skycoin/skywire/pull/4983)
-   feat(router,cli): a sibling tunnel is dialed on the best-ranked unused route; two tunnels by default  [#4981](https://github.com/skycoin/skywire/pull/4981)
-   fix(skysocks,cli): the loadtest sink hashes an object once; the split path answers CONNECT optimistically  [#4982](https://github.com/skycoin/skywire/pull/4982)
-   fix(skysocks): the first stream's exit handshake is pipelined like a chunk's  [#4979](https://github.com/skycoin/skywire/pull/4979)
-   fix(skysocks): a lone stream picks the tunnel with the lowest RTT per open stream, so concurrent streams spread  [#4978](https://github.com/skycoin/skywire/pull/4978)
-   fix(visor,router): an app's route groups close when the app stops  [#4975](https://github.com/skycoin/skywire/pull/4975)
-   fix(router,skysocks): the send window follows the feedback delay over a first-hop baseline, and a lone stream takes the lowest-latency tunnel  [#4972](https://github.com/skycoin/skywire/pull/4972)
-   fix(skysocks): chunks on a closed tunnel fail at once and refetch on a live one  [#4974](https://github.com/skycoin/skywire/pull/4974)
-   fix(dmsg): the standalone socks client carries connections over dmsg and takes the server flags  [#4973](https://github.com/skycoin/skywire/pull/4973)
-   fix(skysocks): an exit-open timeout is logged and counted, and the tunnel sits out the next picks  [#4971](https://github.com/skycoin/skywire/pull/4971)
-   fix(router): ECF and RACK use the end-to-end feedback delay per leg; the latency band judges on windowed min-RTT  [#4970](https://github.com/skycoin/skywire/pull/4970)
-   fix(router): an idle leg is not a stalled leg; a park mirrored from the peer is an event  [#4969](https://github.com/skycoin/skywire/pull/4969)
-   fix(router): an adaptive park holds for 30 s; the latency band no longer re-admits a leg the bottleneck controller just parked  [#4968](https://github.com/skycoin/skywire/pull/4968)
-   fix(visor,cli): proxy mux --rg selects a tunnel by its own port  [#4967](https://github.com/skycoin/skywire/pull/4967)
-   fix(skysocks): a range chunk costs one round trip; parallel fetches start at once and admit on fetch completion  [#4966](https://github.com/skycoin/skywire/pull/4966)
-   fix(skysocks): the tunnel meter learns only from busy windows, and a stale idle tunnel is probed again  [#4965](https://github.com/skycoin/skywire/pull/4965)
-   fix(router): the receive-side stall detector judges legs by payload, and every park emits an event  [#4964](https://github.com/skycoin/skywire/pull/4964)
-   feat(skysocks): a new stream goes to the tunnel with the most proven bandwidth for it  [#4963](https://github.com/skycoin/skywire/pull/4963)
-   fix(router): break the window-refresh/SACK lock inversion; a lone leg never parks; the window ramps faster  [#4962](https://github.com/skycoin/skywire/pull/4962)
-   feat(router,visor): mux FEC is opt-in (routing.mux_fec), off by default  [#4961](https://github.com/skycoin/skywire/pull/4961)
-   fix(router): the send window is sized over the leg's feedback delay, not the ping RTT  [#4960](https://github.com/skycoin/skywire/pull/4960)
-   feat(skysocks-client,cli): --range-port picks the plaintext port the range-splitter splits  [#4959](https://github.com/skycoin/skywire/pull/4959)
-   fix(router): the send window bounds every scheduler mode and grows four times a second  [#4958](https://github.com/skycoin/skywire/pull/4958)
-   feat(cli): the load-test sink serves byte ranges  [#4957](https://github.com/skycoin/skywire/pull/4957)
-   feat(router): a per-leg send window from SACK-proven delivery, and the writer parks at it  [#4956](https://github.com/skycoin/skywire/pull/4956)
-   fix(router): the hook race's direct route honors a diversify dial's first-hop exclusions  [#4955](https://github.com/skycoin/skywire/pull/4955)
-   fix(router): a hole is judged against the delay of the leg it rode  [#4954](https://github.com/skycoin/skywire/pull/4954)
-   fix(router): a standby write grows the slot table, so a pinned leg's promotion sticks  [#4953](https://github.com/skycoin/skywire/pull/4953)
-   fix(router): the local-route memo ignores a dial's transport exclusions; the dial trail covers every route source  [#4952](https://github.com/skycoin/skywire/pull/4952)
-   feat(router): the recovery block names which path asked for each retransmit  [#4951](https://github.com/skycoin/skywire/pull/4951)
-   fix(router): an operator-pinned mux leg is active on add, and promotions are events  [#4950](https://github.com/skycoin/skywire/pull/4950)
-   fix(router): the RSN-oracle path honors a diversify dial's first-hop exclusions  [#4949](https://github.com/skycoin/skywire/pull/4949)
-   fix(router): a DSACK feeds the original frame's delay into the ack-delay estimate  [#4948](https://github.com/skycoin/skywire/pull/4948)
-   feat(router): a diversify dial leaves its route-choice trail on the group  [#4947](https://github.com/skycoin/skywire/pull/4947)
-   fix(router): the candidate-route race honors the multi-tunnel first-hop exclusions  [#4946](https://github.com/skycoin/skywire/pull/4946)
-   fix(skysocks-client): --tunnels N dials every tunnel through a route group  [#4945](https://github.com/skycoin/skywire/pull/4945)
-   feat(router,visor): route-group loss-recovery diagnostics in visor state  [#4944](https://github.com/skycoin/skywire/pull/4944)
-   fix(cxds): commit the startup compaction copy in bounded batches  [#4943](https://github.com/skycoin/skywire/pull/4943)
-   fix(cli): proxy test's restore messages ignored --json  [#4942](https://github.com/skycoin/skywire/pull/4942)
-   fix(visor,execwasm): the android lane could not build, and the phone carried a desk it never serves  [#4941](https://github.com/skycoin/skywire/pull/4941)
-   ci: cancel superseded workflow runs instead of queueing them  [#4940](https://github.com/skycoin/skywire/pull/4940)
-   chore(lint): two more discarded returns errcheck's check-blank rejects  [#4939](https://github.com/skycoin/skywire/pull/4939)
-   bench: the mux campaign's reference sets, the unattended runners, and the changelog for #4910–#4936  [#4938](https://github.com/skycoin/skywire/pull/4938)
-   chore(lint): clear the seven findings that fail the CI lint lane on every PR  [#4937](https://github.com/skycoin/skywire/pull/4937)
-   feat(transport,visor): vstream read-buffer occupancy in diag, and streams outlived their transport  [#4936](https://github.com/skycoin/skywire/pull/4936)
-   fix(setupmetrics): release a half-open probe when the request that took it ends  [#4935](https://github.com/skycoin/skywire/pull/4935)
-   fix(skysocks-client): per-invocation config, so one client cannot rewrite another's flags  [#4934](https://github.com/skycoin/skywire/pull/4934)
-   fix(cli): proxy start watches the app it started, and proxy test restores the client it borrows  [#4933](https://github.com/skycoin/skywire/pull/4933)
-   fix(transport): WritePacket held transportMx across the underlying write, so every read queued behind it  [#4932](https://github.com/skycoin/skywire/pull/4932)
-   fix(visor): a pinned proxy exit is never rotated, and starting the client keeps its other args  [#4931](https://github.com/skycoin/skywire/pull/4931)
-   feat(router): visor state diag carries mux leg events with reasons  [#4930](https://github.com/skycoin/skywire/pull/4930)
-   fix(transport,dmsg): QUIC ran on the 768 KB default receive window  [#4929](https://github.com/skycoin/skywire/pull/4929)
-   fix(transport): the event ring is flooded on a public visor, so keep the last close per transport  [#4928](https://github.com/skycoin/skywire/pull/4928)
-   fix(transport): the UDP demux read loop leaked a timer per deadline change  [#4927](https://github.com/skycoin/skywire/pull/4927)
-   fix(dmsg): the liveness ping had no QUIC branch, so every QUIC session was culled  [#4926](https://github.com/skycoin/skywire/pull/4926)
-   fix(transport): a re-dialed peer's new connection survived the old one's close  [#4925](https://github.com/skycoin/skywire/pull/4925)
-   fix(cli,skysocks-client): proxy start --route dials through a route group so the pins can land  [#4924](https://github.com/skycoin/skywire/pull/4924)
-   fix(dmsg): a seeded server-only entry no longer hides a folded visor's client half  [#4923](https://github.com/skycoin/skywire/pull/4923)
-   ci: autoconfig tests follow the legacy-UI removal, and go.sum is tidy  [#4922](https://github.com/skycoin/skywire/pull/4922)
-   fix(transport,visor): sudph keepalive, and autoconnect tries stcpr and squicr before sudph  [#4921](https://github.com/skycoin/skywire/pull/4921)
-   feat(cli): route settings shows and sets the router knobs, transport preference included  [#4920](https://github.com/skycoin/skywire/pull/4920)
-   feat(transport): visor state diag carries transport open/close events with reasons  [#4919](https://github.com/skycoin/skywire/pull/4919)
-   fix(router): a direct route took whichever transport the walk found first  [#4918](https://github.com/skycoin/skywire/pull/4918)
-   feat(cli): loadtest serve certifies a finite transfer, and takes uploads  [#4917](https://github.com/skycoin/skywire/pull/4917)
-   fix(dmsg): every QUIC session died at 30 s and was redialed forever  [#4916](https://github.com/skycoin/skywire/pull/4916)
-   docs(design): the intended default routing policy, in the operator's words  [#4915](https://github.com/skycoin/skywire/pull/4915)
-   fix(genvisor): the js config marshaller still wrote mux_routes, which #4803 removed  [#4914](https://github.com/skycoin/skywire/pull/4914)
-   docs(design): route multiplexing — the live campaign to finish it  [#4913](https://github.com/skycoin/skywire/pull/4913)
-   ci(release): check out before unpacking the musl toolchain  [#4912](https://github.com/skycoin/skywire/pull/4912)
-   ci(release): the linux jobs lost their C compiler to checkout; rebuild a tag's assets by dispatch  [#4911](https://github.com/skycoin/skywire/pull/4911)
-   docs(changelog): #4908, #4909, and the 2026-09-15 history rewrite  [#4910](https://github.com/skycoin/skywire/pull/4910)


## 1.3.94

397 PRs on top of v1.3.93 — the largest release in this series. Headlines: **dmsg now runs over skynet**, so a visor with transports reaches the overlay through its peers instead of only through dmsg servers; **the desk replaces the Angular dashboard** as the hypervisor UI, with the same wasm module serving as the browser visor; **routing policy owns the route count** instead of a visor-global setting; and a long run of observability work makes `visor state` answer questions that previously needed log-grepping.

-   **dmsg over skynet (#4690–#4763, #4838–#4859).** A visor's dmsg session can ride a skywire route to another visor's relay acceptor instead of a TCP socket to a dmsg server, and any visor can relay — no configuration, no dmsg-server role. Relay hubs are nominated automatically, preferring peers already reachable by a direct transport; a co-resident visor+dmsg-server counts as a hub. Browser visors, which can hold no inbound transport, reach the overlay entirely this way. `DMSGSERVER` also folds a dmsg server onto the visor's own key, and 7 of the 9 deployment servers now run that way.
-   **The desk is the hypervisor UI (#4484, #4690–#4764).** One wasm module is both the served desk and the tab visor; the Angular dashboard is still there, in a tab, and `LEGACYHVUI=true` puts it back at the root. Pairing binds a browser visor to a hypervisor by PK, and the host's pty is available as the desk's terminal.
-   **Routing policy owns the route count (#4300s–#4400s).** `mux_routes` is gone as a visor-global setting: how many routes a stream uses is an output of the active policy, per dial, not a number configured once per visor. A visor can also refuse to be an intermediate hop.
-   **Observability (#4840s–#4860s).** `visor state` reports the dmsg server, relay and transit roles, the clients attached to an in-process dmsg server, the embedded wasm module's provenance, and — new here — whether that module is older than the binary serving it. `status.skysocks` renders the full route including a direct leg's transport type, id and RTT.
-   **Two ports for the hypervisor UI (#4904–#4907).** The Angular dashboard is at the root of `hypervisor.addr` (:8000) and the desk — the wasm-visor hypervisor UI — at `hypervisor.desk_addr` (:8010): one handler on two listeners, the same API and login on each. `legacy_ui`, `LEGACYHVUI` and `hv enable --legacy` are gone; `HVDESKADDR` / `config gen --hvdeskaddr` set the desk address, and `hv status` prints both. A desk tab pairs without a dashboard login, and its browser opens on a start page of the desk's own pages.
-   **History rewritten on 2026-09-15, again.** The six superseded copies of the embedded command module (`pkg/wasmhv/execwasm/blob/skywire.wasm.gz`, 232 MB) were stripped from the commits after `v1.3.94-alpha1`; every commit after that tag has a new SHA, no tag moved, and the tree at the tip is unchanged. A clone that predates it must be reset onto `develop`, not merged.


-   chore: re-embed the command module at 4c82aa80c  [#4909](https://github.com/skycoin/skywire/pull/4909)
-   chore(release): changelog through #4907, and hv status says where the UI is  [#4908](https://github.com/skycoin/skywire/pull/4908)
-   fix(desk): a tab could not be paired until the operator logged into the dashboard  [#4907](https://github.com/skycoin/skywire/pull/4907)
-   fix(hypervisor): the desk's default port is :8010, not :8002  [#4906](https://github.com/skycoin/skywire/pull/4906)
-   chore: re-embed the command module at bba82df2d  [#4905](https://github.com/skycoin/skywire/pull/4905)
-   feat(hypervisor): the dashboard and the desk each on their own port; a start page for the browser  [#4904](https://github.com/skycoin/skywire/pull/4904)
-   docs: regenerate the command reference  [#4903](https://github.com/skycoin/skywire/pull/4903)
-   chore: re-embed the command module at fdabefc5a  [#4902](https://github.com/skycoin/skywire/pull/4902)
-   fix(visor): a TPD heartbeat that fails once is not yet a warning  [#4901](https://github.com/skycoin/skywire/pull/4901)
-   fix(desk): tabs in a window's title bar could not be clicked; address bar showed the rewrite  [#4900](https://github.com/skycoin/skywire/pull/4900)
-   feat(cli): `hv input` — trusted pointer input, so frame bugs can be tested  [#4899](https://github.com/skycoin/skywire/pull/4899)
-   chore: re-embed the command module at ad96405e7  [#4898](https://github.com/skycoin/skywire/pull/4898)
-   fix: four things a frame was swallowing, plus documented ports, a call that would not compile, and a flag that was never declared  [#4897](https://github.com/skycoin/skywire/pull/4897)
-   chore: re-embed the command module at 2c5587d9a  [#4896](https://github.com/skycoin/skywire/pull/4896)
-   fix(desk): the pairing docs never opened, and the docs never ran, on :8000  [#4895](https://github.com/skycoin/skywire/pull/4895)
-   fix(docs+desk): make the served docs navigable and accurate, and fix three desk rough edges  [#4894](https://github.com/skycoin/skywire/pull/4894)
-   docs: stop documenting a DHT, a `cli sshd` and paths that no longer exist  [#4893](https://github.com/skycoin/skywire/pull/4893)
-   fix(visor): a browser visor panicked on every tpviz refresh  [#4892](https://github.com/skycoin/skywire/pull/4892)
-   fix(transport): no inbound stcpr could complete on a visor sharing its transport port  [#4891](https://github.com/skycoin/skywire/pull/4891)
-   chore(release): changelog through #4889, and re-embed the command module  [#4890](https://github.com/skycoin/skywire/pull/4890)
-   fix(config): the resolving proxies could not survive a conf round-trip  [#4889](https://github.com/skycoin/skywire/pull/4889)
-   fix(test): the windows failures were POSIX assumptions, plus one flaky budget  [#4888](https://github.com/skycoin/skywire/pull/4888)
-   fix(visor): a browser visor dialled QUIC and probed STUN, neither of which it has  [#4887](https://github.com/skycoin/skywire/pull/4887)
-   ci: stop failing the build on a byte diff that is not staleness  [#4886](https://github.com/skycoin/skywire/pull/4886)
-   fix(ui): the network visualizer called the API at the wrong origin  [#4885](https://github.com/skycoin/skywire/pull/4885)
-   fix(desk): the docs server ran nowhere anyone could see it  [#4884](https://github.com/skycoin/skywire/pull/4884)
-   fix(cli): label_test.go had no build tag, breaking vet on every PR  [#4883](https://github.com/skycoin/skywire/pull/4883)
-   fix(desk): the docs-site desk was never cross-origin isolated  [#4882](https://github.com/skycoin/skywire/pull/4882)
-   fix(doc): none of the served docs' links worked  [#4881](https://github.com/skycoin/skywire/pull/4881)
-   docs(config): BROWSESUFFIX worked but was not in the conf template  [#4880](https://github.com/skycoin/skywire/pull/4880)
-   fix(desk): the page had no favicon  [#4879](https://github.com/skycoin/skywire/pull/4879)
-   fix(desk): a desk served from a subpath sent netscrape outside its worker scope  [#4878](https://github.com/skycoin/skywire/pull/4878)
-   fix(docs): /playground/ published a desk that cannot load its own assets  [#4877](https://github.com/skycoin/skywire/pull/4877)
-   docs: the recovery paths around pairing, found by running the flow  [#4876](https://github.com/skycoin/skywire/pull/4876)
-   fix(docs): the desk IS the site root, with the docs served inside it  [#4875](https://github.com/skycoin/skywire/pull/4875)
-   feat(wasm): encrypt the tab visor's key at rest, and drop the stale one  [#4874](https://github.com/skycoin/skywire/pull/4874)
-   feat(desk): a visor whose hypervisor is gone comes up roaming instead of stranded  [#4873](https://github.com/skycoin/skywire/pull/4873)
-   build(wasm): commit the js/wasm command module so a source build is a real build  [#4872](https://github.com/skycoin/skywire/pull/4872)
-   ci: a check that embedded browser scripts actually load  [#4871](https://github.com/skycoin/skywire/pull/4871)
-   fix(desk): a stray copy of an insert at the top of desk-boot.js broke every desk  [#4870](https://github.com/skycoin/skywire/pull/4870)
-   fix(desk): a host restart read as "the operator stopped the visor"  [#4869](https://github.com/skycoin/skywire/pull/4869)
-   docs(changelog): 1.3.94, and backfill the three releases that were skipped  [#4868](https://github.com/skycoin/skywire/pull/4868)
-   build(deps): go get -u across host, js/wasm, windows and darwin  [#4867](https://github.com/skycoin/skywire/pull/4867)
-   fix(gotop): unreadable selected row, flickering overlay, and a banner nobody asked for  [#4866](https://github.com/skycoin/skywire/pull/4866)
-   fix(desk): restart could re-run superseded code, and a failed boot had no recovery  [#4865](https://github.com/skycoin/skywire/pull/4865)
-   fix(desk): a visor that went away left the desk pointing at a dead port  [#4864](https://github.com/skycoin/skywire/pull/4864)
-   ci: published binaries embedded a js/wasm module with no recorded revision  [#4863](https://github.com/skycoin/skywire/pull/4863)
-   fix(ui): the empty-node-list message threw whenever the list was empty  [#4862](https://github.com/skycoin/skywire/pull/4862)
-   fix(visor): the hypervisor transport upgrade only ever tried stcpr and sudph  [#4861](https://github.com/skycoin/skywire/pull/4861)
-   feat(config): HVAUTH makes the hypervisor password gate declarative  [#4860](https://github.com/skycoin/skywire/pull/4860)
-   fix(dmsg): the session path kept dialing stale server addresses from the config  [#4859](https://github.com/skycoin/skywire/pull/4859)
-   fix(wasm): the revision read broke the GOOS=js build of the command module  [#4858](https://github.com/skycoin/skywire/pull/4858)
-   fix(transport): the direct path took any transport, not the preferred one  [#4857](https://github.com/skycoin/skywire/pull/4857)
-   fix(proxystatus): a direct session showed no transport type, id or RTT  [#4856](https://github.com/skycoin/skywire/pull/4856)
-   docs: a glossary of the concepts in the code, not just the words in the docs  [#4855](https://github.com/skycoin/skywire/pull/4855)
-   feat(visor): declare additional resolving proxies beyond the primary pair  [#4854](https://github.com/skycoin/skywire/pull/4854)
-   docs: write down the c<layer>-<domain>-<system> file descriptors  [#4853](https://github.com/skycoin/skywire/pull/4853)
-   fix(proxystatus): a direct session drew no exit on status.skysocks  [#4852](https://github.com/skycoin/skywire/pull/4852)
-   chore: remove the deprecated skybian_build_version, dead end to end  [#4851](https://github.com/skycoin/skywire/pull/4851)
-   fix(cli): drop the resolver "web" column, which could never have a value  [#4850](https://github.com/skycoin/skywire/pull/4850)
-   fix(visor): Overview.Hypervisors and ConnectedHypervisor were never populated  [#4849](https://github.com/skycoin/skywire/pull/4849)
-   feat(visor): --log-json writes queryable JSON lines beside the text log  [#4848](https://github.com/skycoin/skywire/pull/4848)
-   feat(visor): report the embedded wasm module's provenance in visor state  [#4847](https://github.com/skycoin/skywire/pull/4847)
-   fix(visor): wasm-serve logged to a logger the visor never captured  [#4846](https://github.com/skycoin/skywire/pull/4846)
-   feat(wasm): say when the embedded command module is older than the binary  [#4845](https://github.com/skycoin/skywire/pull/4845)
-   feat(desk): the PWA install offer belongs in Applications, not under the panel  [#4844](https://github.com/skycoin/skywire/pull/4844)
-   fix(deployment): geoip over plaintext http is blocked on an HTTPS page  [#4843](https://github.com/skycoin/skywire/pull/4843)
-   fix(cli): pty exec reported "interrupted" on runs that succeeded  [#4842](https://github.com/skycoin/skywire/pull/4842)
-   fix(packaging): the shipped unit ran production visors at debug log level  [#4841](https://github.com/skycoin/skywire/pull/4841)
-   fix(dmsg): a folded dmsg server advertises "0.0.1", hiding which build it runs  [#4840](https://github.com/skycoin/skywire/pull/4840)
-   feat(proxy): report the direct path, so a working proxy stops reading as broken  [#4839](https://github.com/skycoin/skywire/pull/4839)
-   fix(transport): the dmsg WS handler was lost to a ClientFactory value copy  [#4838](https://github.com/skycoin/skywire/pull/4838)
-   fix(dmsg): a pinned rendezvous must not require a session the guest can't hold  [#4837](https://github.com/skycoin/skywire/pull/4837)
-   fix(dmsg): a folded dmsg server advertises wss but nothing terminates the TLS  [#4836](https://github.com/skycoin/skywire/pull/4836)
-   fix(dmsg): a public-IP probe must not close a session it only borrowed  [#4835](https://github.com/skycoin/skywire/pull/4835)
-   fix(dmsg): one dial per server, so a pinned rendezvous cannot evict a live session  [#4834](https://github.com/skycoin/skywire/pull/4834)
-   fix(dmsg): a folded dmsg server serves no wss — browsers lost 7 of 9 servers  [#4833](https://github.com/skycoin/skywire/pull/4833)
-   fix(lint): Signalling -> Signaling in the pty keepalive comment  [#4832](https://github.com/skycoin/skywire/pull/4832)
-   fix(pty): join the exec-stream keepalive before the handler returns  [#4831](https://github.com/skycoin/skywire/pull/4831)
-   test(dmsg): bind relay sockets on a short path, and skip them on Windows  [#4830](https://github.com/skycoin/skywire/pull/4830)
-   feat(dmsg): let a relay deliver over a terminal skynet leg to the destination  [#4829](https://github.com/skycoin/skywire/pull/4829)
-   feat(dmsg): DMSGWEBSK — run the embedded resolver under a fixed key  [#4828](https://github.com/skycoin/skywire/pull/4828)
-   fix(dmsg): don't charge a host's own attached service to the relay budget  [#4827](https://github.com/skycoin/skywire/pull/4827)
-   feat(dmsg): env knobs for the local relay, so the loopback acceptor is usable  [#4826](https://github.com/skycoin/skywire/pull/4826)
-   feat(dmsg): serve the local relay acceptor by default  [#4825](https://github.com/skycoin/skywire/pull/4825)
-   fix(lint): clear the 18 findings gating the linux, darwin and windows lanes  [#4824](https://github.com/skycoin/skywire/pull/4824)
-   feat(visor): report the clients connected to the in-process dmsg server  [#4823](https://github.com/skycoin/skywire/pull/4823)
-   Revert "flags: wash the help-screen cloud in blue and knock the rain through it"  [#4822](https://github.com/skycoin/skywire/pull/4822)
-   flags: wash the help-screen cloud in blue and knock the rain through it  [#4821](https://github.com/skycoin/skywire/pull/4821)
-   test(cxo): stop the bounded-retention tests failing on a loaded runner  [#4820](https://github.com/skycoin/skywire/pull/4820)
-   fix(dmsg): an attached client treated its own success as "nothing to connect to"  [#4819](https://github.com/skycoin/skywire/pull/4819)
-   flags: the help-screen cloud reads as a cloud — color, size, placement, less blanking  [#4818](https://github.com/skycoin/skywire/pull/4818)
-   chore(ui): re-embed the manager bundle after the mux_routes removal  [#4817](https://github.com/skycoin/skywire/pull/4817)
-   fix(cxo): a stale tpd-stats leaf was served forever instead of resynced  [#4816](https://github.com/skycoin/skywire/pull/4816)
-   fix(cli): --jq and --shape were silently ignored by the discovery commands  [#4815](https://github.com/skycoin/skywire/pull/4815)
-   vendor: take 16.8 MB of demo wasm out of the tree  [#4814](https://github.com/skycoin/skywire/pull/4814)
-   fix(dmsg): min_hops is a property of traffic, not of session carriers  [#4813](https://github.com/skycoin/skywire/pull/4813)
-   fix(dmsg): give each attached peer a share of the relay budget  [#4812](https://github.com/skycoin/skywire/pull/4812)
-   fix(dmsg): stop silently dropping direct_only and relay_max_streams  [#4811](https://github.com/skycoin/skywire/pull/4811)
-   fix(dmsg-disc): a folded visor's client entry must not inherit the server TTL  [#4809](https://github.com/skycoin/skywire/pull/4809)
-   feat(dmsg): run the resolving proxy under its own key, attached in-process  [#4808](https://github.com/skycoin/skywire/pull/4808)
-   feat(dmsg): let a standalone client attach to a local visor's relay  [#4807](https://github.com/skycoin/skywire/pull/4807)
-   flags: fill an unset secret-key flag from the skyenv file  [#4806](https://github.com/skycoin/skywire/pull/4806)
-   fix(cli): `tp disc --pk <key>` reported the local visor instead of the key  [#4805](https://github.com/skycoin/skywire/pull/4805)
-   flags: wrap the help screen to 80 columns and color every line of it  [#4804](https://github.com/skycoin/skywire/pull/4804)
-   feat(routing): drop the visor-global mux_routes; route count is a policy output  [#4803](https://github.com/skycoin/skywire/pull/4803)
-   fix(cxosub): the feed cycle goroutine must close the channel it owns  [#4802](https://github.com/skycoin/skywire/pull/4802)
-   fix(visor): the transport discovery must not be a boot prerequisite  [#4801](https://github.com/skycoin/skywire/pull/4801)
-   fix(dmsg-disc): read Entry.Timestamp in either unit it is written in  [#4800](https://github.com/skycoin/skywire/pull/4800)
-   fix(dmsg,transport): stop discarding the reason a dial failed  [#4798](https://github.com/skycoin/skywire/pull/4798)
-   fix(dmsg-disc): stop offering dmsg servers that have stopped registering  [#4797](https://github.com/skycoin/skywire/pull/4797)
-   feat(autoconfig): offer the in-visor dmsg server and legacy HV UI in the install-page form  [#4796](https://github.com/skycoin/skywire/pull/4796)
-   feat(visor): report the dmsg server, relay and transit roles in visor state  [#4795](https://github.com/skycoin/skywire/pull/4795)
-   perf(tpd): make the metrics feed cheaper to encode  [#4794](https://github.com/skycoin/skywire/pull/4794)
-   fix(config): make a supplied secret key actually pin the visor's identity  [#4793](https://github.com/skycoin/skywire/pull/4793)
-   feat(config): add DMSGSERVER, for a dmsg server on the visor's own key  [#4792](https://github.com/skycoin/skywire/pull/4792)
-   feat(dmsg): nominate relays without requiring any configuration  [#4791](https://github.com/skycoin/skywire/pull/4791)
-   feat(dmsg): nominate co-resident visor+dmsg-server hubs as relays  [#4790](https://github.com/skycoin/skywire/pull/4790)
-   feat(dmsg): raise the visor relay stream cap and make it configurable  [#4789](https://github.com/skycoin/skywire/pull/4789)
-   feat(routing): let a visor refuse to be an intermediate hop  [#4788](https://github.com/skycoin/skywire/pull/4788)
-   feat(dmsg): let one key register as both a client and a dmsg server  [#4787](https://github.com/skycoin/skywire/pull/4787)
-   perf(logging): render log lines without fmt, cutting 4% of dmsg-server CPU  [#4786](https://github.com/skycoin/skywire/pull/4786)
-   feat(dmsg): serve the in-process dmsg server on the visor's transport port  [#4785](https://github.com/skycoin/skywire/pull/4785)
-   perf(sd): build the service-discovery router once, not per request  [#4784](https://github.com/skycoin/skywire/pull/4784)
-   fix(logging): stop an unset log_level meaning debug, and give each service its own level  [#4783](https://github.com/skycoin/skywire/pull/4783)
-   perf(logging): stop formatting every log entry twice in the capture hooks  [#4782](https://github.com/skycoin/skywire/pull/4782)
-   perf(logging): stop paying for log lines nobody reads  [#4781](https://github.com/skycoin/skywire/pull/4781)
-   perf(dmsg): stop tracing every stream twice at debug level  [#4780](https://github.com/skycoin/skywire/pull/4780)
-   perf(services): put the request log under log_level and behind the outcome  [#4779](https://github.com/skycoin/skywire/pull/4779)
-   perf(tpd): stop re-registering unchanged transports on every visor heartbeat  [#4778](https://github.com/skycoin/skywire/pull/4778)
-   perf(treestore): pace nudge-driven cleanup sweeps by their own cost  [#4777](https://github.com/skycoin/skywire/pull/4777)
-   fix(cxds): let the sparse-file compaction run on stores the old scan cleared  [#4776](https://github.com/skycoin/skywire/pull/4776)
-   perf(geoip): memory-map the embedded database instead of inflating it on the heap  [#4775](https://github.com/skycoin/skywire/pull/4775)
-   perf(tpd): index bandwidth days with one SCAN instead of per-key probing  [#4774](https://github.com/skycoin/skywire/pull/4774)
-   fix(dmsg): stop the server peer-mesh flap  [#4773](https://github.com/skycoin/skywire/pull/4773)
-   perf(services): gzip only responses over 1 KiB  [#4772](https://github.com/skycoin/skywire/pull/4772)
-   perf(treestore): back off silence-only reconnects on a quiet feed  [#4771](https://github.com/skycoin/skywire/pull/4771)
-   perf(tpd): stream the metrics feed JSON into gzip per record  [#4770](https://github.com/skycoin/skywire/pull/4770)
-   fix(cxds): compact a sparse store, not only a dead-heavy one  [#4769](https://github.com/skycoin/skywire/pull/4769)
-   perf(dmsg): pooled relay copy buffers; per-session lines at debug  [#4768](https://github.com/skycoin/skywire/pull/4768)
-   perf(geoip): open the embedded database lazily, once per process  [#4767](https://github.com/skycoin/skywire/pull/4767)
-   fix(visor): stop hammering the address resolver: lazy own-key refresh, dead-target backoff  [#4766](https://github.com/skycoin/skywire/pull/4766)
-   fix(ar): stop compressing the tiny per-visor answers; per-request logs at debug  [#4765](https://github.com/skycoin/skywire/pull/4765)
-   fix(skyenvfile): close the source file before renaming over it (Windows)  [#4764](https://github.com/skycoin/skywire/pull/4764)
-   chore(lint): clear the golangci-lint findings left by the convergence PRs  [#4763](https://github.com/skycoin/skywire/pull/4763)
-   refactor(wasm): drop the on-disk module fallback and the js-wasm release asset; fix a hypervisor startup deadlock  [#4762](https://github.com/skycoin/skywire/pull/4762)
-   refactor(wasm): remove the legacy hv-boot page, SharedWorker core and the wasm-visor/dmsg-wasm commands  [#4761](https://github.com/skycoin/skywire/pull/4761)
-   refactor(wasm): remove the embedded wasm-visor blobs, variants and legacy module routes  [#4760](https://github.com/skycoin/skywire/pull/4760)
-   feat(wasm): visualizer and wallet cipher from the root js module  [#4759](https://github.com/skycoin/skywire/pull/4759)
-   chore(wasm): re-embed wasm-visor blob (develop d882c00dd)  [#4758](https://github.com/skycoin/skywire/pull/4758)
-   feat(wasm): the desk host as a mode of the root js module; one URL per desk page  [#4757](https://github.com/skycoin/skywire/pull/4757)
-   feat(wasm): embed the js/wasm command module in the native binary (two-stage build)  [#4756](https://github.com/skycoin/skywire/pull/4756)
-   feat(ci): publish the js/wasm command module beside the binary; default hv serve to it  [#4755](https://github.com/skycoin/skywire/pull/4755)
-   feat(router): count where multi-hop routes came from in visor state diag  [#4754](https://github.com/skycoin/skywire/pull/4754)
-   feat(hypervisor): opt-in legacy Angular UI at the web UI root  [#4753](https://github.com/skycoin/skywire/pull/4753)
-   feat(visor): attached visors' transports as a local route graph on the hypervisor  [#4751](https://github.com/skycoin/skywire/pull/4751)
-   docs: hv add/pair persistence, offline-hypervisor backoff, DMSGSERVERCONF  [#4749](https://github.com/skycoin/skywire/pull/4749)
-   fix(visor): stop redialing an unpublished hypervisor every 5 s  [#4748](https://github.com/skycoin/skywire/pull/4748)
-   feat(visor): run a standalone dmsg server in-process from its config file  [#4747](https://github.com/skycoin/skywire/pull/4747)
-   fix(visor): mirror paired hypervisors into skywire.conf so autoconfig keeps them  [#4746](https://github.com/skycoin/skywire/pull/4746)
-   chore(wasm): re-embed wasm-visor blob (#4742)  [#4743](https://github.com/skycoin/skywire/pull/4743)
-   fix(desk): files and viewer panes no longer deadlock the page  [#4742](https://github.com/skycoin/skywire/pull/4742)
-   chore(wasm): re-embed wasm-visor blob (#4740)  [#4741](https://github.com/skycoin/skywire/pull/4741)
-   feat(desk): PWA for the hypervisor-served desk; browser visor relay-only while attached, normal dmsg client otherwise  [#4740](https://github.com/skycoin/skywire/pull/4740)
-   chore(wasm): re-embed wasm-visor blob (#4738)  [#4739](https://github.com/skycoin/skywire/pull/4739)
-   fix(desk): same-origin/loopback fetches stay local; proxy dial timeout; "+" for terminal tabs  [#4738](https://github.com/skycoin/skywire/pull/4738)
-   chore(wasm): re-embed wasm-visor blob (#4736)  [#4737](https://github.com/skycoin/skywire/pull/4737)
-   feat(desk): netscrape's proxy setting is one [scheme://]host:port; BrowseClearnetRequest gains proxy  [#4736](https://github.com/skycoin/skywire/pull/4736)
-   fix(config): a regen keeps the hypervisor auth gate as the existing config has it  [#4735](https://github.com/skycoin/skywire/pull/4735)
-   chore(wasm): re-embed wasm-visor blob (#4733)  [#4734](https://github.com/skycoin/skywire/pull/4734)
-   feat(desk): pair and identity windows; config identity; removing a hypervisor drops its live trust  [#4733](https://github.com/skycoin/skywire/pull/4733)
-   feat(visor): hypervisor pairing (pending keys, hv pair, one-time code); auth on at first run  [#4732](https://github.com/skycoin/skywire/pull/4732)
-   feat(dmsg): browser visor is an unpublished dmsg client; relay-only sessions  [#4731](https://github.com/skycoin/skywire/pull/4731)
-   feat(config): source-driven cascade route setup is the default for browser visors  [#4730](https://github.com/skycoin/skywire/pull/4730)
-   chore(wasm): re-embed wasm-visor blob (#4725–#4728)  [#4729](https://github.com/skycoin/skywire/pull/4729)
-   fix(cli): pty, dmsg curl and reward dial the visor RPC through vnet  [#4728](https://github.com/skycoin/skywire/pull/4728)
-   fix(transport): VStream segmentation, per-transport stream ids, no silent drops; visor state diag section  [#4727](https://github.com/skycoin/skywire/pull/4727)
-   feat(visor): GoroutineDump RPC; visor goroutines falls back to it  [#4726](https://github.com/skycoin/skywire/pull/4726)
-   fix(transport): a transport dialed while a handler was registered never got it  [#4725](https://github.com/skycoin/skywire/pull/4725)
-   fix(visor): reload the desk when the served skywire.wasm is rebuilt  [#4724](https://github.com/skycoin/skywire/pull/4724)
-   fix(visor): US spelling in proxy-verify comments (misspell lint)  [#4723](https://github.com/skycoin/skywire/pull/4723)
-   fix(dmsg): remember relay forward failures during the server rollout  [#4722](https://github.com/skycoin/skywire/pull/4722)
-   fix(visor): route-setup hook skips transport types this visor has no client for  [#4721](https://github.com/skycoin/skywire/pull/4721)
-   fix(visor): proxy exit verification rejected every working exit  [#4720](https://github.com/skycoin/skywire/pull/4720)
-   fix(router): a sole leg that delivers payload is not a black hole  [#4719](https://github.com/skycoin/skywire/pull/4719)
-   fix(dmsg): a relay forwards via the server that last reached the destination first  [#4718](https://github.com/skycoin/skywire/pull/4718)
-   chore(wasm): re-embed the wasm-visor blob (picks up #4711)  [#4717](https://github.com/skycoin/skywire/pull/4717)
-   fix(dmsg): retry a lapsed relay backoff on the next nomination  [#4716](https://github.com/skycoin/skywire/pull/4716)
-   fix(dmsg): a relay dial that times out at boot retries in 30 s, not 5 min  [#4715](https://github.com/skycoin/skywire/pull/4715)
-   fix(dmsg): a relay session is tried before the lookup, the cached route and the servers  [#4714](https://github.com/skycoin/skywire/pull/4714)
-   feat(dmsg): show relay nominees and relay clients; restart the pass on a nomination  [#4713](https://github.com/skycoin/skywire/pull/4713)
-   fix(visor): the route-setup hook still minted dmsg-type transports in a tab  [#4712](https://github.com/skycoin/skywire/pull/4712)
-   wasm-visor: let the desk place the browser tab strip  [#4711](https://github.com/skycoin/skywire/pull/4711)
-   fix(dmsg): a relay nomination fanned the client out across every server  [#4710](https://github.com/skycoin/skywire/pull/4710)
-   fix(visor): nominate relays from persistent transports only  [#4709](https://github.com/skycoin/skywire/pull/4709)
-   feat(dmsg): nominate relays from live transports, no configuration  [#4708](https://github.com/skycoin/skywire/pull/4708)
-   fix(transport): never mint a dmsg-type transport as a fallback in the browser  [#4707](https://github.com/skycoin/skywire/pull/4707)
-   fix(visor): proxy auto-exit loop must not re-point an operator-started client  [#4706](https://github.com/skycoin/skywire/pull/4706)
-   feat(visor): admit managed visors to the dmsg relay  [#4705](https://github.com/skycoin/skywire/pull/4705)
-   feat(dmsg): skynet carrier and per-visor relay acceptor (#4484 stage 3)  [#4704](https://github.com/skycoin/skywire/pull/4704)
-   feat(dmsg): let a client session carry stream requests for another key  [#4703](https://github.com/skycoin/skywire/pull/4703)
-   chore: drop a stale desk pseudo-version from go.sum  [#4702](https://github.com/skycoin/skywire/pull/4702)
-   fix(router): segment route group writes larger than one packet  [#4701](https://github.com/skycoin/skywire/pull/4701)
-   fix(config): carry LOGLVL through the in-process autoconfig re-entry  [#4700](https://github.com/skycoin/skywire/pull/4700)
-   chore(wasm): re-embed the wasm-visor blob (picks up the headless exec of #4698)  [#4699](https://github.com/skycoin/skywire/pull/4699)
-   feat(wasm-visor): headless exec for the desk — run a shell command over CDP and get text back  [#4698](https://github.com/skycoin/skywire/pull/4698)
-   feat(desk-boot): boot the attached visor at the log level given by ?loglvl=  [#4697](https://github.com/skycoin/skywire/pull/4697)
-   fix(visor): identify the peer of a route-group conn on the skynet hypervisor RPC mirror  [#4696](https://github.com/skycoin/skywire/pull/4696)
-   fix(visor): upgrade a dmsg hypervisor RPC conn to skynet when a direct transport appears  [#4695](https://github.com/skycoin/skywire/pull/4695)
-   fix(router): route over any existing transport; no stcpr/sudph in a browser  [#4694](https://github.com/skycoin/skywire/pull/4694)
-   fix(autoconfig): pass the SKYENV edits to config gen as explicit flags  [#4693](https://github.com/skycoin/skywire/pull/4693)
-   chore(wasm): re-embed the wasm-visor blob (#4691 console-app fix)  [#4692](https://github.com/skycoin/skywire/pull/4692)
-   fix(wasm-visor): openConsole must open websh, not the host pty  [#4691](https://github.com/skycoin/skywire/pull/4691)
-   feat(hv): same-origin transport for the desk the hypervisor serves  [#4690](https://github.com/skycoin/skywire/pull/4690)
-   chore(wasm): re-embed the wasm-visor blob (picks up the one-desk module changes)  [#4689](https://github.com/skycoin/skywire/pull/4689)
-   feat(desk): one desk — the native hypervisor serves the wasm desk at its root  [#4688](https://github.com/skycoin/skywire/pull/4688)
-   chore(wasm): re-embed the wasm-visor blob (picks up netscrape's proxy setting)  [#4685](https://github.com/skycoin/skywire/pull/4685)
-   feat(desk): netscrape browses the mesh and clearnet natively, with a proxy setting  [#4684](https://github.com/skycoin/skywire/pull/4684)
-   fix(desk): tabbed native terminal rendered blank  [#4683](https://github.com/skycoin/skywire/pull/4683)
-   chore(wasm): re-embed the wasm-visor blob (picks up the header tabs)  [#4682](https://github.com/skycoin/skywire/pull/4682)
-   feat(desk): tabs in the window title bar, reorderable, and a tabbed native terminal  [#4681](https://github.com/skycoin/skywire/pull/4681)
-   fix(skychat): report the transport an outbound message actually used  [#4680](https://github.com/skycoin/skywire/pull/4680)
-   docs(skychat): verified two-instance standalone walkthrough; retire the stale duplicate  [#4679](https://github.com/skycoin/skywire/pull/4679)
-   build(deps): update Go dependencies for every build target, not just the host  [#4678](https://github.com/skycoin/skywire/pull/4678)
-   fix(desk): the terminal-window block landed outside skywireDeskBoot  [#4677](https://github.com/skycoin/skywire/pull/4677)
-   chore(wasm): re-embed the wasm-visor blob (picks up #4675)  [#4676](https://github.com/skycoin/skywire/pull/4676)
-   fix(desk): native hypervisor desk renders its dashboard natively and gets a terminal window  [#4675](https://github.com/skycoin/skywire/pull/4675)
-   fix(lint): check the io.Copy error in the dmsghttp eviction test  [#4674](https://github.com/skycoin/skywire/pull/4674)
-   fix(skychat): mint a message id on every send natively too  [#4673](https://github.com/skycoin/skywire/pull/4673)
-   fix(lint): clear the six findings failing the lint lanes  [#4672](https://github.com/skycoin/skywire/pull/4672)
-   fix(dmsghttp): evict a pooled idle stream on a timer, not only on the next request  [#4671](https://github.com/skycoin/skywire/pull/4671)
-   fix(dmsg): the idle-session reaper re-dialed every server it reaped  [#4670](https://github.com/skycoin/skywire/pull/4670)
-   fix(desk): give netscrape an absolute dashboard URL  [#4669](https://github.com/skycoin/skywire/pull/4669)
-   chore(wasm): re-embed the desk-host blob  [#4668](https://github.com/skycoin/skywire/pull/4668)
-   feat(hv): render the hypervisor UI as a netscrape tab on the native desk  [#4667](https://github.com/skycoin/skywire/pull/4667)
-   perf(hv serve): serve the wasm blobs gzipped instead of inflating them  [#4666](https://github.com/skycoin/skywire/pull/4666)
-   fix(transport): don't offer webrtc where the runtime has no peer connection  [#4665](https://github.com/skycoin/skywire/pull/4665)
-   fix(visor): proxy-exit verification passed on the interstitial it minted itself  [#4664](https://github.com/skycoin/skywire/pull/4664)
-   chore(deps): bump 0magnet/desk and 0magnet/bottle  [#4663](https://github.com/skycoin/skywire/pull/4663)
-   feat(visor): expose per-session dmsg stream counts in visor state  [#4661](https://github.com/skycoin/skywire/pull/4661)
-   fix(visor): embedded setup nodes connected to every dmsg server, forever  [#4660](https://github.com/skycoin/skywire/pull/4660)
-   perf(visor): default GOGC to 50 on wasm, where a heap peak is permanent  [#4659](https://github.com/skycoin/skywire/pull/4659)
-   fix(desk): address the hypervisor tab as vnet:<port>, not as the served form  [#4658](https://github.com/skycoin/skywire/pull/4658)
-   fix(visor): an unconfigured log level meant debug, not info  [#4657](https://github.com/skycoin/skywire/pull/4657)
-   docs(config): correct two config fields whose docs describe behaviour that does not exist  [#4656](https://github.com/skycoin/skywire/pull/4656)
-   docs(specs): correct five wire formats that were documented wrong  [#4655](https://github.com/skycoin/skywire/pull/4655)
-   feat(tabshot): evallat — time CDP Runtime.evaluate round-trips  [#4654](https://github.com/skycoin/skywire/pull/4654)
-   chore(wasm): re-embed the desk-host blob with the netscrape host-element fix  [#4653](https://github.com/skycoin/skywire/pull/4653)
-   fix(desk): give netscrape its own child element to own  [#4652](https://github.com/skycoin/skywire/pull/4652)
-   chore(wasm): re-embed the desk-host blob with the desk-side flex-root fix  [#4651](https://github.com/skycoin/skywire/pull/4651)
-   fix(desk): apply the netscrape flex root at the DESK call site too  [#4650](https://github.com/skycoin/skywire/pull/4650)
-   chore(wasm): re-embed the desk-host blob with the netscrape flex-root fix  [#4649](https://github.com/skycoin/skywire/pull/4649)
-   fix(desk): give netscrape a flex root so the page it loads is visible  [#4648](https://github.com/skycoin/skywire/pull/4648)
-   feat(desk): run the wasm visor in a Worker, not on the page main thread  [#4647](https://github.com/skycoin/skywire/pull/4647)
-   docs(design): ML-DSA post-quantum signatures — negotiation and rollout  [#4646](https://github.com/skycoin/skywire/pull/4646)
-   perf(wasm): stop paying for the CA bundle and a JSON decode at every browser boot  [#4645](https://github.com/skycoin/skywire/pull/4645)
-   perf(noise): size the DH keypair pool for the browser  [#4644](https://github.com/skycoin/skywire/pull/4644)
-   fix(sn): serve /stats aggregate publicly, gate only the topology half  [#4643](https://github.com/skycoin/skywire/pull/4643)
-   feat(desk): bridge the hypervisor /ws into vnet so the desk can drive the HOST visor  [#4642](https://github.com/skycoin/skywire/pull/4642)
-   fix(wasm-visor): drop the Go/TinyGo picker from the UI  [#4641](https://github.com/skycoin/skywire/pull/4641)
-   fix(cxo): sweep superseded Roots on the treestore subscriber  [#4640](https://github.com/skycoin/skywire/pull/4640)
-   fix: config update against dmsg-only, AR log flood, and setup-node /stats exposure  [#4639](https://github.com/skycoin/skywire/pull/4639)
-   fix(router): stop planning mux legs the destination is guaranteed to refuse  [#4638](https://github.com/skycoin/skywire/pull/4638)
-   fix(router): make the local-route memo actually hit, and stop the BFS allocating per graph edge  [#4637](https://github.com/skycoin/skywire/pull/4637)
-   fix(transport): close the client InitClient replaces, and do not install a nil one  [#4636](https://github.com/skycoin/skywire/pull/4636)
-   perf(cxo): run the per-second stat loops only where something reads them  [#4635](https://github.com/skycoin/skywire/pull/4635)
-   refactor(transport): drop the HTTP reconcile backstop on CXO visors  [#4634](https://github.com/skycoin/skywire/pull/4634)
-   perf(transport): stop re-deriving over HTTP what CXO already reconciles  [#4633](https://github.com/skycoin/skywire/pull/4633)
-   fix(transport): stop cleanupTransports busy-waiting on a canceled context  [#4632](https://github.com/skycoin/skywire/pull/4632)
-   fix(mobile): stop embedding the browser wasm-visor blobs in the android lib  [#4631](https://github.com/skycoin/skywire/pull/4631)
-   perf(visor): let real inbound traffic answer the self-probe question  [#4630](https://github.com/skycoin/skywire/pull/4630)
-   fix(deps): drop the stale bottle go.sum pair left by the version bump  [#4629](https://github.com/skycoin/skywire/pull/4629)
-   fix(cli): drop the jsoncontract allowlist entry for the deleted visor top  [#4628](https://github.com/skycoin/skywire/pull/4628)
-   perf(cipher): use the pubkey verify cache on the gob path too  [#4627](https://github.com/skycoin/skywire/pull/4627)
-   chore: drop the dead visor top command and a stale lint exclusion  [#4626](https://github.com/skycoin/skywire/pull/4626)
-   fix(ci): stamp the buildinfo package that still exists  [#4625](https://github.com/skycoin/skywire/pull/4625)
-   refactor(desk): run the in-tab skywire CLI on bottle's process layer  [#4624](https://github.com/skycoin/skywire/pull/4624)
-   fix(build): stamp the buildinfo package that exists  [#4623](https://github.com/skycoin/skywire/pull/4623)
-   test(cli): declare hv/bidi.go as streaming for the --json contract  [#4622](https://github.com/skycoin/skywire/pull/4622)
-   fix(hv): refuse --driver alongside a CDP target, and regenerate hv docs  [#4621](https://github.com/skycoin/skywire/pull/4621)
-   fix(hv): the BiDi driver broke the js/wasm root build  [#4620](https://github.com/skycoin/skywire/pull/4620)
-   fix(hv): --driver advertised its type as "hv drive"  [#4619](https://github.com/skycoin/skywire/pull/4619)
-   feat(hv): drive Waterfox/Firefox over WebDriver BiDi, not just CDP  [#4618](https://github.com/skycoin/skywire/pull/4618)
-   chore(deps): update the 0magnet modules  [#4617](https://github.com/skycoin/skywire/pull/4617)
-   fix(visorconfig): the flush test asserted POSIX permissions on Windows  [#4616](https://github.com/skycoin/skywire/pull/4616)
-   fix(desk): release an exited command's wasm instance instead of pinning it forever  [#4615](https://github.com/skycoin/skywire/pull/4615)
-   fix(desk): stop booting two idle Go/wasm runtimes nobody asked for  [#4614](https://github.com/skycoin/skywire/pull/4614)
-   fix(tpviz): gzip the tp-viz handler  [#4613](https://github.com/skycoin/skywire/pull/4613)
-   fix(deployment): gzip the routers that were left out  [#4612](https://github.com/skycoin/skywire/pull/4612)
-   fix(visorconfig): flush no longer erases config keys the binary doesn't know  [#4611](https://github.com/skycoin/skywire/pull/4611)
-   fix(transport): gzip the tp-list CXO leaf  [#4610](https://github.com/skycoin/skywire/pull/4610)
-   fix(hypervisor): send the node list only the transport fields it renders  [#4609](https://github.com/skycoin/skywire/pull/4609)
-   fix(hypervisor): gzip the /api surface  [#4608](https://github.com/skycoin/skywire/pull/4608)
-   feat(hypervisor): serve the /api surface over a websocket at /ws  [#4607](https://github.com/skycoin/skywire/pull/4607)
-   fix(ui): stop the top bar's tab strip widening the whole page  [#4606](https://github.com/skycoin/skywire/pull/4606)
-   feat(visor): let a visor-hosted wasm_serve serve the desk, not just the legacy page  [#4605](https://github.com/skycoin/skywire/pull/4605)
-   fix(tpviz): route every REST call through API_BASE so an embedding host can retarget it  [#4604](https://github.com/skycoin/skywire/pull/4604)
-   fix(hypervisor): tpviz write endpoints were reachable unauthenticated, cross-origin  [#4603](https://github.com/skycoin/skywire/pull/4603)
-   fix(serviceuptime): assert New's cost in transactions, not milliseconds  [#4602](https://github.com/skycoin/skywire/pull/4602)
-   fix(dmsg): a failed client entry publish was never retried  [#4601](https://github.com/skycoin/skywire/pull/4601)
-   fix(ci): give cmd/wasm-visor a host main so the CodeQL build step passes  [#4600](https://github.com/skycoin/skywire/pull/4600)
-   fix(security): four one-line fixes from a source review  [#4599](https://github.com/skycoin/skywire/pull/4599)
-   fix(pty): direct-TCP host panicked after serving one session  [#4597](https://github.com/skycoin/skywire/pull/4597)
-   fix(cxo): Preview.Get returned the remote copy on a local hit  [#4596](https://github.com/skycoin/skywire/pull/4596)
-   fix(tpd): stamp the snapshot behind /all-transports/stats and per-key-stats  [#4595](https://github.com/skycoin/skywire/pull/4595)
-   feat(cxo): Preview-backed point reads, and an address-resolver lookup that holds nothing  [#4594](https://github.com/skycoin/skywire/pull/4594)
-   docs: stop the runtime config API claiming persistence it does not have  [#4593](https://github.com/skycoin/skywire/pull/4593)
-   Revert "fix(autoconfig): retain existing hypervisors across config regen"  [#4591](https://github.com/skycoin/skywire/pull/4591)
-   fix(autoconfig): retain existing hypervisors across config regen  [#4590](https://github.com/skycoin/skywire/pull/4590)
-   feat(sd): aggregate service registrations over CXO  [#4588](https://github.com/skycoin/skywire/pull/4588)
-   chore(deps): update 0magnet dependencies  [#4587](https://github.com/skycoin/skywire/pull/4587)
-   feat(ar): publish a CXO bindings feed keyed by peer public key  [#4586](https://github.com/skycoin/skywire/pull/4586)
-   feat(visor): publish service-discovery registrations over CXO  [#4585](https://github.com/skycoin/skywire/pull/4585)
-   fix(cxoaggregate): log a successful subscribe at Info  [#4583](https://github.com/skycoin/skywire/pull/4583)
-   fix(desk): let hypervisor UI windows receive clicks  [#4582](https://github.com/skycoin/skywire/pull/4582)
-   test: fix a cross-test data race in skysocks, and make the cxo churn test say why it failed  [#4581](https://github.com/skycoin/skywire/pull/4581)
-   test(dmsgcurl): back off between download retries  [#4580](https://github.com/skycoin/skywire/pull/4580)
-   refactor: move six general-purpose packages out to their own modules  [#4579](https://github.com/skycoin/skywire/pull/4579)
-   refactor(tpd): move the CXO aggregators onto the shared core  [#4577](https://github.com/skycoin/skywire/pull/4577)
-   refactor(ar): move the bind aggregator onto the shared CXO core  [#4575](https://github.com/skycoin/skywire/pull/4575)
-   refactor(dmsgd): move the registration aggregator onto the shared CXO core  [#4574](https://github.com/skycoin/skywire/pull/4574)
-   refactor(cxo): shared fan-in aggregator core with a required identity binding  [#4573](https://github.com/skycoin/skywire/pull/4573)
-   fix(dmsgd): bind the registration aggregator to the service key  [#4571](https://github.com/skycoin/skywire/pull/4571)
-   fix(visor): surface the registration-CXO feed gating in visor state  [#4570](https://github.com/skycoin/skywire/pull/4570)
-   refactor: use the 0magnet modules for calvin, spectrogram and router7  [#4568](https://github.com/skycoin/skywire/pull/4568)
-   refactor(got): move pkg/got out to github.com/0magnet/got  [#4567](https://github.com/skycoin/skywire/pull/4567)
-   fix(visor): resolve the DMSG-D CXO peer from dmsg.discovery too  [#4566](https://github.com/skycoin/skywire/pull/4566)
-   feat(dmsg): pluggable entry resolvers; drop the vestigial DHT lookup hook  [#4565](https://github.com/skycoin/skywire/pull/4565)
-   feat(dmsg): make "do not register" explicit instead of a silently-lying writer  [#4564](https://github.com/skycoin/skywire/pull/4564)
-   feat(dmsg): opt-in all-servers sweep for unregistered, unseeded peers  [#4563](https://github.com/skycoin/skywire/pull/4563)
-   docs(dmsgclient): decision matrix for picking a constructor  [#4561](https://github.com/skycoin/skywire/pull/4561)
-   chore(deps): update 0magnet dependencies  [#4560](https://github.com/skycoin/skywire/pull/4560)
-   fix(rewards): seed the deployment-service PKs so stats fetches resolve  [#4559](https://github.com/skycoin/skywire/pull/4559)
-   fix(wasm-visor): stop the proxy bootstrap re-dial storm  [#4558](https://github.com/skycoin/skywire/pull/4558)
-   test(router): assert the gap is open, not that time has passed  [#4556](https://github.com/skycoin/skywire/pull/4556)
-   fix(router): RemoteThroughput returned garbage for a zero-width window  [#4555](https://github.com/skycoin/skywire/pull/4555)
-   feat(rewards/server): route setup node panel on /stats  [#4554](https://github.com/skycoin/skywire/pull/4554)
-   fix(router): --direct actually bypasses the route finder  [#4553](https://github.com/skycoin/skywire/pull/4553)
-   feat(cli/pv): -V shows each public visor's version  [#4551](https://github.com/skycoin/skywire/pull/4551)
-   fix(autoconnect): uncap direct carriers so stcpr is tried against every public visor  [#4550](https://github.com/skycoin/skywire/pull/4550)
-   fix(transport/webrtc): close the signaling stream once the DataChannel opens  [#4549](https://github.com/skycoin/skywire/pull/4549)
-   fix(doc): clear the lint debt that is failing every Test run on develop  [#4548](https://github.com/skycoin/skywire/pull/4548)
-   perf(transport/webrtc): hardcode mDNS off, drop the env override  [#4546](https://github.com/skycoin/skywire/pull/4546)
-   feat(rewards): three more panels on the statistics page  [#4545](https://github.com/skycoin/skywire/pull/4545)
-   perf(transport/webrtc): disable pion mDNS and default interceptors  [#4544](https://github.com/skycoin/skywire/pull/4544)
-   feat(rewards): source the statistics pages from CXO, add the stats/daily feed  [#4543](https://github.com/skycoin/skywire/pull/4543)
-   fix(tpd): stop silently returning partial transport counts  [#4542](https://github.com/skycoin/skywire/pull/4542)
-   feat(rewards): check every service is reachable through every dmsg server  [#4541](https://github.com/skycoin/skywire/pull/4541)
-   fix(dmsg): name the destination in the failed-lookup log line  [#4540](https://github.com/skycoin/skywire/pull/4540)
-   feat(rewards): show the dmsg substrate in the stats panel  [#4539](https://github.com/skycoin/skywire/pull/4539)
-   fix(rewards): trim the partial final slot, and never omit a panel silently  [#4536](https://github.com/skycoin/skywire/pull/4536)
-   feat(rewards): render the stats page as a terminal UI exported to HTML  [#4535](https://github.com/skycoin/skywire/pull/4535)
-   fix(rewards-server): time-series charts; unbreak bandwidth-history and visor-bandwidth  [#4534](https://github.com/skycoin/skywire/pull/4534)
-   feat(desk): open the docs in the browser, addressed as vnet:<port>  [#4532](https://github.com/skycoin/skywire/pull/4532)
-   fix(rewards): stop the stats summary dying on a 24 MB TPD fetch  [#4531](https://github.com/skycoin/skywire/pull/4531)
-   fix(cli): stop tp viz publishing a discovery entry it never needs  [#4530](https://github.com/skycoin/skywire/pull/4530)
-   fix(cxo): clear the two lint issues and the CodeQL alert failing develop  [#4528](https://github.com/skycoin/skywire/pull/4528)
-   feat(doc): serve the docs from the binary that they document  [#4527](https://github.com/skycoin/skywire/pull/4527)
-   feat(cxo): publish TPD's network aggregates as a small, fast feed  [#4526](https://github.com/skycoin/skywire/pull/4526)
-   fix(cxo): wait for the first sync instead of returning a cold-cache miss  [#4525](https://github.com/skycoin/skywire/pull/4525)
-   feat(desk): render same-origin pages natively, and never nest the shell  [#4524](https://github.com/skycoin/skywire/pull/4524)
-   fix: correct four misspellings failing the CI lint job  [#4523](https://github.com/skycoin/skywire/pull/4523)
-   fix(desk): retire /dashboard — the framed root already serves it  [#4522](https://github.com/skycoin/skywire/pull/4522)
-   fix(dmsg-server): advertise the full build string in the discovery entry  [#4521](https://github.com/skycoin/skywire/pull/4521)
-   fix(cxo): give tpd-metrics the large-feed first-sync timeout  [#4520](https://github.com/skycoin/skywire/pull/4520)
-   feat(hv serve): address wasm variants by path so the surface can be static files  [#4519](https://github.com/skycoin/skywire/pull/4519)
-   fix(tpd): size the metrics split from the compressed total, not the raw  [#4516](https://github.com/skycoin/skywire/pull/4516)
-   feat(tpd): publish transport metrics as immutable per-day leaves  [#4515](https://github.com/skycoin/skywire/pull/4515)
-   fix(dmsg): serialize the client entry read-modify-write against the discovery  [#4511](https://github.com/skycoin/skywire/pull/4511)
-   fix(tpd): gzip + chunk the metrics CXO feed instead of dropping bandwidth  [#4509](https://github.com/skycoin/skywire/pull/4509)
-   fix(tpd): the metrics CXO feed never published — one oversized window killed it  [#4508](https://github.com/skycoin/skywire/pull/4508)
-   chore: drop superseded 0magnet pseudo-versions from go.sum  [#4507](https://github.com/skycoin/skywire/pull/4507)
-   fix(dmsg-server): metrics ticker no longer crash-loops the server  [#4506](https://github.com/skycoin/skywire/pull/4506)
-   fix(dmsg-disc): default the official-server allowlist to the deployment seed list  [#4505](https://github.com/skycoin/skywire/pull/4505)
-   fix(dmsg): re-dial the server a client just lost before taking a new one  [#4503](https://github.com/skycoin/skywire/pull/4503)
-   fix(rewards): tp-viz reuses the server's dmsg client instead of a second one  [#4502](https://github.com/skycoin/skywire/pull/4502)
-   fix(hv serve): remove the server-side tpviz API and its dmsg identity  [#4501](https://github.com/skycoin/skywire/pull/4501)
-   fix(desk): /desk must redirect RELATIVE or it escapes the vnet prefix  [#4499](https://github.com/skycoin/skywire/pull/4499)
-   fix(hv serve): the desk is the root here too, not just on a visor  [#4498](https://github.com/skycoin/skywire/pull/4498)
-   feat(desk): browser chrome gains favicons, page titles and shortcuts  [#4496](https://github.com/skycoin/skywire/pull/4496)
-   feat(desk): browser tabs are named after their host  [#4495](https://github.com/skycoin/skywire/pull/4495)
-   feat(desk): terminals are tabs of one window, not a window each  [#4494](https://github.com/skycoin/skywire/pull/4494)
-   feat(desk): the hypervisor UI is a browser tab, and tabs are clickable  [#4493](https://github.com/skycoin/skywire/pull/4493)
-   feat(desk): desk chrome from the library, explicit vnet addressing  [#4492](https://github.com/skycoin/skywire/pull/4492)
-   feat(visor): the desk IS the hypervisor UI; restore desk chrome without the JS engine  [#4491](https://github.com/skycoin/skywire/pull/4491)
-   docs: site root becomes the desk surface (playground drawer removed)  [#4490](https://github.com/skycoin/skywire/pull/4490)
-   fix(skysocks): rolling idle deadline for chunk bodies — slow routes wasted 2.4x  [#4489](https://github.com/skycoin/skywire/pull/4489)
-   docs: playground drawer styles must survive instant navigation  [#4488](https://github.com/skycoin/skywire/pull/4488)
-   docs(glossary): reflect the Go browser / retired JS engine (#4477)  [#4487](https://github.com/skycoin/skywire/pull/4487)
-   fix(transport): Set*Handler must not hold handlerMu across tm.mx — boot deadlock  [#4486](https://github.com/skycoin/skywire/pull/4486)
-   docs: site accuracy audit + playground integrated as a persistent drawer  [#4485](https://github.com/skycoin/skywire/pull/4485)
-   fix(router): delayed ack for clean in-order delivery — idle routes churned retransmits  [#4483](https://github.com/skycoin/skywire/pull/4483)
-   feat(wasm-visor): VPN default-egress mode — un-pinned clearnet rides the tunnel  [#4482](https://github.com/skycoin/skywire/pull/4482)
-   feat(wasm-visor): vpn dial retry + auto-selection from service discovery  [#4481](https://github.com/skycoin/skywire/pull/4481)
-   chore(wasmbin): refresh the committed wasm-visor blob (b62153713)  [#4480](https://github.com/skycoin/skywire/pull/4480)
-   feat(wasm-visor): true VPN client — netstack tunnel instance + JS API  [#4479](https://github.com/skycoin/skywire/pull/4479)
-   feat(vpn): gVisor netstack TUN — the js/wasm VPN client data plane  [#4478](https://github.com/skycoin/skywire/pull/4478)
-   wasmhv: compile the Go browser into the visor binary; retire the JS engine  [#4477](https://github.com/skycoin/skywire/pull/4477)
-   fix(appdisc): never advertise a js/wasm visor as a clearnet exit  [#4476](https://github.com/skycoin/skywire/pull/4476)
-   fix(dmsg): per-destination dial-failure backoff — wedged dst pegged a browser core  [#4475](https://github.com/skycoin/skywire/pull/4475)
-   fix(skysocks): count send progress as tunnel liveness — uploads retired at hard-dead window  [#4474](https://github.com/skycoin/skywire/pull/4474)
-   fix(router): RACK threshold tracks measured ack delay, not just idle RTT  [#4473](https://github.com/skycoin/skywire/pull/4473)
-   fix(skysocks): raise yamux ConnectionWriteTimeout — saturated uploads reset at ~20s  [#4472](https://github.com/skycoin/skywire/pull/4472)
-   feat(cmdutil): js goroutine-dump hook — SIGQUIT parity for wasm instances  [#4471](https://github.com/skycoin/skywire/pull/4471)
-   fix(router): judge proactive HoL overdueness by the seq's own send leg  [#4470](https://github.com/skycoin/skywire/pull/4470)
-   Experimental Go/wasm browser (SkywireGoBrowser) in the wasm visor  [#4469](https://github.com/skycoin/skywire/pull/4469)
-   chore(wasmbin): refresh the committed wasm-visor blob (ccb71bbd1)  [#4468](https://github.com/skycoin/skywire/pull/4468)
-   fix(skysocks): guard the rescue copier against zero-progress reads; add CDP profiler tool  [#4467](https://github.com/skycoin/skywire/pull/4467)
-   chore(wasmbin): refresh the committed wasm-visor blob (05322a2dc)  [#4466](https://github.com/skycoin/skywire/pull/4466)
-   fix(desk): self-heal the dashboard frame when the icon font loses the boot load-lottery  [#4465](https://github.com/skycoin/skywire/pull/4465)
-   chore(wasmbin): refresh the committed wasm-visor blob (8e39092f9)  [#4464](https://github.com/skycoin/skywire/pull/4464)
-   feat(hypervisor): serve the converged desk shell at /desk  [#4463](https://github.com/skycoin/skywire/pull/4463)
-   chore(wasmbin): refresh the committed wasm-visor blob (d250633e0)  [#4462](https://github.com/skycoin/skywire/pull/4462)
-   fix(visor): bound the status page's layer collectors with a time budget  [#4461](https://github.com/skycoin/skywire/pull/4461)
-   chore(wasmbin): refresh the committed wasm-visor blob (354466bd9)  [#4460](https://github.com/skycoin/skywire/pull/4460)
-   fix(visor): launcher waits on the embedded resolving-proxy modules  [#4459](https://github.com/skycoin/skywire/pull/4459)
-   Update 0magnet dependencies to current commits  [#4458](https://github.com/skycoin/skywire/pull/4458)
-   chore(wasmbin): refresh the committed wasm-visor blob (03589df23)  [#4457](https://github.com/skycoin/skywire/pull/4457)
-   feat(proxystatus): status.dmsg + status.skynet pages for the resolving proxies  [#4456](https://github.com/skycoin/skywire/pull/4456)
-   chore(wasmbin): refresh the committed wasm-visor blob (a2b2a4f1e)  [#4455](https://github.com/skycoin/skywire/pull/4455)
-   fix(visor): stop killing the shared hypervisor conn on one slow RPC  [#4454](https://github.com/skycoin/skywire/pull/4454)
-   chore(wasmbin): refresh the committed wasm-visor blob (a281bf8ef)  [#4453](https://github.com/skycoin/skywire/pull/4453)
-   fix(router): retx-buffer tags use transport UUIDs, not leg indices  [#4452](https://github.com/skycoin/skywire/pull/4452)
-   chore(wasmbin): refresh the committed wasm-visor blob (f7f67841c)  [#4451](https://github.com/skycoin/skywire/pull/4451)
-   feat(router): manual direction pin for the unidirectional mux (wire-coordinated)  [#4450](https://github.com/skycoin/skywire/pull/4450)
-   chore(wasmbin): refresh the committed wasm-visor blob (23cc27d40)  [#4449](https://github.com/skycoin/skywire/pull/4449)
-   chore(wasmbin): refresh the committed wasm-visor blob (3cfa33308)  [#4448](https://github.com/skycoin/skywire/pull/4448)
-   fix(cli): proxy mux info no longer drops per-leg hop chains from its output  [#4447](https://github.com/skycoin/skywire/pull/4447)
-   fix(router): narrow the demote retx flush to the demoted legs' sequences  [#4446](https://github.com/skycoin/skywire/pull/4446)
-   fix(visor): hv ls falls back to the summary cache on dead cached conns  [#4445](https://github.com/skycoin/skywire/pull/4445)
-   chore(wasmbin): refresh the committed wasm-visor blob (4076a282f)  [#4444](https://github.com/skycoin/skywire/pull/4444)
-   fix(skysocks): range-split degrades to sequential streaming instead of truncating  [#4443](https://github.com/skycoin/skywire/pull/4443)
-   chore(wasmbin): refresh the committed wasm-visor blob (d61ed3831)  [#4442](https://github.com/skycoin/skywire/pull/4442)
-   build(deps): condense dependabot lockfile bumps + go dep sweep  [#4441](https://github.com/skycoin/skywire/pull/4441)
-   chore(wasmbin): refresh the committed wasm-visor blob (23df0699b)  [#4440](https://github.com/skycoin/skywire/pull/4440)
-   feat(router): per-leg dup/repair recv decomposition + status-page overhead cell  [#4439](https://github.com/skycoin/skywire/pull/4439)
-   fix(router): age-gate proactive HoL retransmits with holGapThreshold  [#4438](https://github.com/skycoin/skywire/pull/4438)
-   chore(wasmbin): refresh the committed wasm-visor blob (675ed6691)  [#4437](https://github.com/skycoin/skywire/pull/4437)
-   fix(config): autostarted skysocks-client reconnects in-process  [#4436](https://github.com/skycoin/skywire/pull/4436)
-   feat(desk): stream the in-tab visor log to the /ctl/log harness bridge  [#4435](https://github.com/skycoin/skywire/pull/4435)
-   chore(wasmbin): refresh the committed wasm-visor blob (208f4af87)  [#4434](https://github.com/skycoin/skywire/pull/4434)
-   fix(skysocks): count inbound bytes as tunnel liveness, not only pongs  [#4433](https://github.com/skycoin/skywire/pull/4433)
-   chore(wasmbin): refresh the committed wasm-visor blob (31da86c31)  [#4432](https://github.com/skycoin/skywire/pull/4432)
-   fix(desk): a crashed visor is not an operator stop — restart on reload  [#4431](https://github.com/skycoin/skywire/pull/4431)
-   fix(webrtc): browser peer connections must not leak or panic the runtime  [#4430](https://github.com/skycoin/skywire/pull/4430)
-   chore(wasmbin): refresh the committed wasm-visor blob (0bc1e5d44)  [#4429](https://github.com/skycoin/skywire/pull/4429)
-   feat(visor): end-to-end proxy-exit verification with rotation; router-line ring  [#4428](https://github.com/skycoin/skywire/pull/4428)
-   chore(wasmbin): refresh the committed wasm-visor blob (e02def441)  [#4427](https://github.com/skycoin/skywire/pull/4427)
-   fix(desk): SOCKS-over-vnet handshake pump stands down; repin squashed deps  [#4426](https://github.com/skycoin/skywire/pull/4426)
-   fix(visor): gate the WS autoconnect phase on page protocol; blob refresh (3f151ee68)  [#4425](https://github.com/skycoin/skywire/pull/4425)
-   fix(dmsg): carrier converge dials the wanted carrier only — no fallback churn  [#4424](https://github.com/skycoin/skywire/pull/4424)
-   chore(wasmbin): refresh the committed wasm-visor blob (62b1a354e)  [#4423](https://github.com/skycoin/skywire/pull/4423)
-   ci: latest toolchains; fold the bundle module in; release a dead instance's vnet ports  [#4422](https://github.com/skycoin/skywire/pull/4422)
-   chore(policy): refresh the committed routing-policy bundle.wasm (tinygo 0.41.1)  [#4421](https://github.com/skycoin/skywire/pull/4421)
-   fix(ci): FEC comparative — CI-only skip when the shape never emerges  [#4420](https://github.com/skycoin/skywire/pull/4420)
-   fix(ci): skip the FEC comparative on compute-starved runners, via calibration  [#4419](https://github.com/skycoin/skywire/pull/4419)
-   fix(ci): bound the FEC aggregation assertion at 3x single-leg  [#4418](https://github.com/skycoin/skywire/pull/4418)
-   fix(ci): the three Test-workflow failures on loaded runners  [#4417](https://github.com/skycoin/skywire/pull/4417)
-   chore(wasmbin): refresh the committed wasm-visor blob (71a94b86b)  [#4416](https://github.com/skycoin/skywire/pull/4416)
-   feat(desk): tabbed browser + terminal, proxy chain on by default for browser visors  [#4415](https://github.com/skycoin/skywire/pull/4415)
-   feat(visor): swtr from the browser — WT autoconnect phase, AR-resolved browser WT dial, wt-first dmsg carriers  [#4414](https://github.com/skycoin/skywire/pull/4414)
-   fix(router): --direct on-demand transport uses the host-aware type order  [#4413](https://github.com/skycoin/skywire/pull/4413)
-   fix(visor+ui): dmsg-observed public IP in the Overview; arch-labeled hypervisor badge  [#4412](https://github.com/skycoin/skywire/pull/4412)
-   chore(wasmbin): refresh the committed wasm-visor blob (a77351c08)  [#4409](https://github.com/skycoin/skywire/pull/4409)
-   desk: native hypervisor-UI rendering (vnet service worker), reload persistence fixes, CI round-4  [#4408](https://github.com/skycoin/skywire/pull/4408)
-   Tidy go.sum after the termanim bump  [#4407](https://github.com/skycoin/skywire/pull/4407)
-   Refresh the committed wasm-visor blob  [#4406](https://github.com/skycoin/skywire/pull/4406)
-   Converged desk page, Ctrl+C into wasm commands, native-parity terminal colors, CI fixes  [#4405](https://github.com/skycoin/skywire/pull/4405)
-   The whole skywire binary in the browser: playground on the docs site, autoconfig + proxy in the tab  [#4404](https://github.com/skycoin/skywire/pull/4404)
-   refactor: promote third_party trees to 0magnet forks imported by module path  [#4402](https://github.com/skycoin/skywire/pull/4402)
-   docs: CLI/network operations guides + front-page help screenshot and --tui demo  [#4401](https://github.com/skycoin/skywire/pull/4401)
-   feat(dmsg): per-carrier server probe, carrier-preserving conf pull, fix quic-erasing Protocol stomp  [#4400](https://github.com/skycoin/skywire/pull/4400)
-   fix(router): stop SACK retransmit storms — per-seq retx backoff + RTT-floored RACK ceiling  [#4399](https://github.com/skycoin/skywire/pull/4399)
-   perf(cipher): memoize secp256k1 pubkey validation to cut the wasm parse burst  [#4398](https://github.com/skycoin/skywire/pull/4398)
-   feat(wasm-visor): prefer proxy exits that are direct transport peers (faster first proxied load)  [#4397](https://github.com/skycoin/skywire/pull/4397)
-   feat(wasm-visor): status.skysocks — local proxy-status page, never gated by the interstitial  [#4396](https://github.com/skycoin/skywire/pull/4396)
-   fix(wasm-visor): memoize local-route BFS, widen skysocks dial margin, recover wedged shared-worker boot  [#4394](https://github.com/skycoin/skywire/pull/4394)
-   fix(wasm-visor): stop skysocks-lite pegging on zombie exits; honest CLI-RPC bridge liveness  [#4393](https://github.com/skycoin/skywire/pull/4393)
-   perf(tpd): cut transient allocations in buildTransportMetrics  [#4392](https://github.com/skycoin/skywire/pull/4392)
-   fix(wasmhv): don't strand the visor on the page main thread (UI freeze)  [#4391](https://github.com/skycoin/skywire/pull/4391)
-   perf(rf): landmark (transit-node) routing — compose far routes via hubs  [#4390](https://github.com/skycoin/skywire/pull/4390)

## 1.3.93

251 PRs on top of v1.3.92. Headlines: **route policy gains hard transport exclusions and FEC-aware striping**, `status.skysocks` becomes a real diagnostic surface rather than a list, and `visor state` grows server-side projection plus a `--watch` NDJSON stream so a chart can be piped straight out of it.

-   **Routing — exclusions and stripe caps.** `route_exclude_transport_types` hard-excludes a transport type from route selection, and applies on the mux K-candidate path as well as the direct one. Per-leg FEC-block striping is capped so a stalled leg stays recoverable instead of taking the block with it. `route calc --capacity` reports the disjoint/multiplexable ceiling for a destination.
-   **status.skysocks as a diagnostic.** Per-stream bytes, rate and RTT; a GPU route-graph view; per-layer status ownership, with the page moving to skysocks-client where it belongs.
-   **visor state projection + watch.** `--select` builds only the requested subtree server-side (a `mux` projection skips the ~307 KB transports build), and `--watch <interval>` emits one snapshot per tick as NDJSON — a clean pipe for a chart or a monitor.
-   **HTTPS range-splitting (opt-in).** TLS-terminating range splits for HTTPS, behind an opt-in MITM root.

-   fix(config): default routing policy to none, not adaptive  [#4388](https://github.com/skycoin/skywire/pull/4388)
-   chore(wasm): rebuild embedded wasm-visor blob (picks up #4386)  [#4387](https://github.com/skycoin/skywire/pull/4387)
-   fix(router): byte-sort route-BFS keys, kill the pk.String() alloc churn  [#4386](https://github.com/skycoin/skywire/pull/4386)
-   perf(rf): memoize weighted route results per graph (kill repeated BFS)  [#4385](https://github.com/skycoin/skywire/pull/4385)
-   chore(wasm): rebuild embedded wasm-visor blob (picks up #4382 + #4383)  [#4384](https://github.com/skycoin/skywire/pull/4384)
-   fix(cxo): pin the transport feed so route-calc doesn't re-handshake CXO  [#4383](https://github.com/skycoin/skywire/pull/4383)
-   fix(router): build local-route lookups once per CXO snapshot, not per dial  [#4382](https://github.com/skycoin/skywire/pull/4382)
-   chore(wasm): rebuild embedded wasm-visor blob (picks up #4379 accept-backoff)  [#4381](https://github.com/skycoin/skywire/pull/4381)
-   perf(rf): shared background-refreshed route graph (stop per-request graph builds)  [#4380](https://github.com/skycoin/skywire/pull/4380)
-   fix(appnet): back off serveRouteGroup on repeated accept failures (stop CPU spin)  [#4379](https://github.com/skycoin/skywire/pull/4379)
-   perf(rf): parent-pointer BFS paths in the route finder (kill the path-copy GC storm)  [#4378](https://github.com/skycoin/skywire/pull/4378)
-   fix(router): park (not remove) data-stalled mux legs under a stuck frontier  [#4377](https://github.com/skycoin/skywire/pull/4377)
-   fix(router): hold >=2 active download legs and apply the mux floor live  [#4376](https://github.com/skycoin/skywire/pull/4376)
-   fix(skysocks): keep a range-split chunk retrying through a tunnel rotation  [#4375](https://github.com/skycoin/skywire/pull/4375)
-   fix(router): return mux route groups in a stable order  [#4374](https://github.com/skycoin/skywire/pull/4374)
-   feat(router): apply route_exclude_transport_types to the mux K-candidate path too  [#4373](https://github.com/skycoin/skywire/pull/4373)
-   feat(router): cap per-leg FEC-block striping so a stalled leg stays FEC-recoverable  [#4372](https://github.com/skycoin/skywire/pull/4372)
-   fix(cli): surface direct/payload/directional in proxy mux-info json  [#4371](https://github.com/skycoin/skywire/pull/4371)
-   perf(webrtc): raise SCTP receive window off 1 MiB default + surface dead channels  [#4370](https://github.com/skycoin/skywire/pull/4370)
-   docs(router): correct stale FEC-negotiation comment (no flag gate)  [#4369](https://github.com/skycoin/skywire/pull/4369)
-   feat(router): route_exclude_transport_types — hard-exclude transport types from routes  [#4368](https://github.com/skycoin/skywire/pull/4368)
-   fix(status.skysocks): stable leg order so the tree/graph stop reshuffling  [#4367](https://github.com/skycoin/skywire/pull/4367)
-   fix(status.skysocks): render the standby route fan — real source/exit + hop-less legs  [#4366](https://github.com/skycoin/skywire/pull/4366)
-   fix(cli): bare 'ut' online query fetches days=1, not the 30-day window  [#4365](https://github.com/skycoin/skywire/pull/4365)
-   fix(cli): read CXO-backed discovery URLs straight through, not via stale disk cache  [#4364](https://github.com/skycoin/skywire/pull/4364)
-   fix(status.skysocks): route graph is opt-in and pauses when hidden (CPU)  [#4363](https://github.com/skycoin/skywire/pull/4363)
-   fix(cli): route calc --capacity via two-visor transport intersection (TPD-free)  [#4362](https://github.com/skycoin/skywire/pull/4362)
-   feat(cli): route calc --capacity — report the disjoint/multiplexable route ceiling  [#4361](https://github.com/skycoin/skywire/pull/4361)
-   fix(skysocks-client): create HTTPS range-split MITM root at startup, not on dial  [#4360](https://github.com/skycoin/skywire/pull/4360)
-   feat(skysocks): TLS-terminating HTTPS range-splitting (opt-in MITM root)  [#4359](https://github.com/skycoin/skywire/pull/4359)
-   fix(interstitial): auto-reload after 'Route up' instead of stalling until manual reload  [#4358](https://github.com/skycoin/skywire/pull/4358)
-   fix(lint): errcheck check-blank in wasm-visor staleness test  [#4357](https://github.com/skycoin/skywire/pull/4357)
-   test(wasmhv): fail CI when the committed wasm-visor blob is stale vs its source  [#4356](https://github.com/skycoin/skywire/pull/4356)
-   chore(wasm-visor): rebuild the embedded blob to include RPC-over-dmsg (#4330)  [#4355](https://github.com/skycoin/skywire/pull/4355)
-   fix(router): confine upload to the primary leg when there is no direct leg  [#4354](https://github.com/skycoin/skywire/pull/4354)
-   fix(router): acceptor defaults its active set to all-standby, mirror-promoted  [#4353](https://github.com/skycoin/skywire/pull/4353)
-   fix(proxystatus): uint32 hash so routegraph builds on 32-bit (unblocks develop-latest binary)  [#4352](https://github.com/skycoin/skywire/pull/4352)
-   fix(router): acceptor honors the initiator's mirrored active set (stop re-admitting parked legs)  [#4351](https://github.com/skycoin/skywire/pull/4351)
-   fix(router): periodic leg-state resync so the mirror self-corrects lost park signals  [#4350](https://github.com/skycoin/skywire/pull/4350)
-   fix(router): mirror native-controller leg parks to the peer (CapLegState)  [#4349](https://github.com/skycoin/skywire/pull/4349)
-   fix(router): keep >=1 active reverse leg so download confines instead of spraying standby  [#4348](https://github.com/skycoin/skywire/pull/4348)
-   fix(router): forward SelfHealTarget through policy.Hook so wasm presets re-cap the running pool  [#4347](https://github.com/skycoin/skywire/pull/4347)
-   fix(skysocks): tolerate reorder-wedge in tunnel liveness, stop false route-group teardown  [#4346](https://github.com/skycoin/skywire/pull/4346)
-   fix(proxystatus): seed route-graph layout, unify tree/graph/log, add zoom-fit controls  [#4345](https://github.com/skycoin/skywire/pull/4345)
-   fix(test): repair develop Test lane (2 real races + flaky-test deflakes)  [#4344](https://github.com/skycoin/skywire/pull/4344)
-   docs(rfc): bottleneck-relative disjointness + logical-route multiplexing (shared-pool #4336)  [#4343](https://github.com/skycoin/skywire/pull/4343)
-   feat(visor-state): server-side projection + --watch NDJSON stream; range-split as state  [#4342](https://github.com/skycoin/skywire/pull/4342)
-   fix(lint): clear the golangci-lint debt blocking every open PR  [#4341](https://github.com/skycoin/skywire/pull/4341)
-   tpviz: latency-space view — visors positioned by measured RTT, with a spherical Voronoi  [#4340](https://github.com/skycoin/skywire/pull/4340)
-   fix(router): confine unidir download to the active reverse leg, never the direct leg  [#4339](https://github.com/skycoin/skywire/pull/4339)
-   feat(proxystatus): GPU route-graph view on status.skysocks  [#4338](https://github.com/skycoin/skywire/pull/4338)
-   fix(router): adaptive keeps an active download-leg floor on unidirectional mux  [#4337](https://github.com/skycoin/skywire/pull/4337)
-   RFC + phase-1: shared warm-route pool for multiplexed routing  [#4336](https://github.com/skycoin/skywire/pull/4336)
-   fix(router): make extra --tunnels leave over disjoint first-hop transports  [#4335](https://github.com/skycoin/skywire/pull/4335)
-   feat(proxystatus): per-stream bytes, rate, and rtt on status.skysocks  [#4334](https://github.com/skycoin/skywire/pull/4334)
-   fix(router): runtime mux standby/width retune re-caps a running route group  [#4333](https://github.com/skycoin/skywire/pull/4333)
-   fix(genvisor): emit hypervisor_autoconnect in the hand-rolled JSON marshaler  [#4332](https://github.com/skycoin/skywire/pull/4332)
-   fix(policy): rebuild+gate preset bundle, wire adaptive mux tunables into the wasm guest (#4325)  [#4331](https://github.com/skycoin/skywire/pull/4331)
-   feat(wasm-visor): serve the visor RPC gateway over dmsg (cli --rpc dmsg://<pk>)  [#4330](https://github.com/skycoin/skywire/pull/4330)
-   feat(visor): surface unidirectional mux direction + flip state in visor state  [#4329](https://github.com/skycoin/skywire/pull/4329)
-   feat(wasm-visor): answer `cli visor state` from the wasm-visor RPC gateway  [#4328](https://github.com/skycoin/skywire/pull/4328)
-   ci: split lint from tests so a lint finding can't blank the suite (#4326)  [#4327](https://github.com/skycoin/skywire/pull/4327)
-   proxystatus: restore per-leg route-tree summaries (fixes #4313 regression); supersedes #4318  [#4323](https://github.com/skycoin/skywire/pull/4323)
-   feat(router): shared-bottleneck detection for mux legs (RFC 8382)  [#4322](https://github.com/skycoin/skywire/pull/4322)
-   feat(router): add OTIAS and STMS mux packet schedulers  [#4321](https://github.com/skycoin/skywire/pull/4321)
-   feat(skysocks): transparent HTTP GET→range-split in the socks5 proxy  [#4320](https://github.com/skycoin/skywire/pull/4320)
-   fix(router): bound unidirectional download fan-out to the mirrored active set  [#4319](https://github.com/skycoin/skywire/pull/4319)
-   feat(proxystatus): two-level route tree — stream (tunnels) over packet (legs)  [#4313](https://github.com/skycoin/skywire/pull/4313)
-   fix(lint): clear the golangci-lint debt in the FEC code  [#4312](https://github.com/skycoin/skywire/pull/4312)
-   fix(router): unidirectional DIRECTION governs send-selection, not the standby flag  [#4311](https://github.com/skycoin/skywire/pull/4311)
-   fix(router): don't reap the unidirectional light-direction leg as a black-hole  [#4310](https://github.com/skycoin/skywire/pull/4310)
-   make: resolve a Go toolchain TinyGo accepts, instead of failing on the newest  [#4309](https://github.com/skycoin/skywire/pull/4309)
-   fix(router): --direct actually bypasses the routing policy  [#4308](https://github.com/skycoin/skywire/pull/4308)
-   fix(dmsgd): coalesce clients-by-server leaf re-encode (was ~1.6 cores in gzip)  [#4307](https://github.com/skycoin/skywire/pull/4307)
-   Replace the vendored WinBox.js with the Go port  [#4306](https://github.com/skycoin/skywire/pull/4306)
-   feat(router): load-driven flip for unidirectional mux (upload-heavy → mux)  [#4305](https://github.com/skycoin/skywire/pull/4305)
-   feat(router): unidirectional per-leg send selection (CapUniDir)  [#4304](https://github.com/skycoin/skywire/pull/4304)
-   feat(router): per-leg unique-payload counter for confound-free per-direction telemetry  [#4303](https://github.com/skycoin/skywire/pull/4303)
-   fix(router): signal a leg's BORN-standby state to the peer (CapLegState gap)  [#4302](https://github.com/skycoin/skywire/pull/4302)
-   feat(router): signal mux leg standby/active to the peer (CapLegState)  [#4300](https://github.com/skycoin/skywire/pull/4300)
-   feat(router): RACK-TLP tail-loss probe + DSACK reorder-window adaptation  [#4299](https://github.com/skycoin/skywire/pull/4299)
-   fix(router): route + forward FEC RepairPacket (was dropped on every hop)  [#4297](https://github.com/skycoin/skywire/pull/4297)
-   fix(wasm-visor): fast re-warm skysocks-lite proxy on tab wake  [#4296](https://github.com/skycoin/skywire/pull/4296)
-   feat(router): RACK-derived retransmit threshold (replace fixed 750ms) [#4287]  [#4295](https://github.com/skycoin/skywire/pull/4295)
-   feat(ui): mux route tree view on the node Routing tab  [#4294](https://github.com/skycoin/skywire/pull/4294)
-   fix(wasmhv): raise nested-browser window on iframe body click  [#4293](https://github.com/skycoin/skywire/pull/4293)
-   feat(router): FEC telemetry — repair bytes + reconstructs in visor state  [#4292](https://github.com/skycoin/skywire/pull/4292)
-   docs: name the three escapes from the in-order-stream wall  [#4286](https://github.com/skycoin/skywire/pull/4286)
-   docs(router): reorder buffer no longer releases or skips a gap  [#4285](https://github.com/skycoin/skywire/pull/4285)
-   fix(router): compile fix — thread symLen through RepairPacket wiring  [#4284](https://github.com/skycoin/skywire/pull/4284)
-   fix(visor): report /health transport stats for all transport types  [#4283](https://github.com/skycoin/skywire/pull/4283)
-   feat(router): forward error correction for the packet mux  [#4282](https://github.com/skycoin/skywire/pull/4282)
-   feat(proxy): add --direct flag to proxy start  [#4281](https://github.com/skycoin/skywire/pull/4281)
-   fix(router): reap and replace a black-holing sole mux leg  [#4280](https://github.com/skycoin/skywire/pull/4280)
-   feat(wasm-visor): optional Go/wasm transport worker for browse origins  [#4279](https://github.com/skycoin/skywire/pull/4279)
-   feat(router): FEC coding core (systematic Cauchy-Reed-Solomon over GF(2^8)) for #4270  [#4278](https://github.com/skycoin/skywire/pull/4278)
-   chore: make component tags agree within each package  [#4276](https://github.com/skycoin/skywire/pull/4276)
-   fix(router): goodput-gate the latency-band demotion of active mux legs  [#4275](https://github.com/skycoin/skywire/pull/4275)
-   fix(lint): gofmt struct field alignment in route_group.go  [#4274](https://github.com/skycoin/skywire/pull/4274)
-   fix(router): latency-band reads transport-RTT fallback, not raw pong EWMA  [#4273](https://github.com/skycoin/skywire/pull/4273)
-   fix(router): fast-cluster latency band + mux data-plane observability  [#4272](https://github.com/skycoin/skywire/pull/4272)
-   ci: setup-java v5->v6 + refresh go module dependencies  [#4268](https://github.com/skycoin/skywire/pull/4268)
-   fix(log): stop truncating PKs in logs/CLI and demote teardown frame WARN  [#4267](https://github.com/skycoin/skywire/pull/4267)
-   pty: stream exec output over HTTP instead of buffering to a 16MiB cap  [#4266](https://github.com/skycoin/skywire/pull/4266)
-   fix(router): park (not remove) stalled mux legs in manual mode  [#4265](https://github.com/skycoin/skywire/pull/4265)
-   fix(router): dynamically re-elect the mux primary leg when it is out of band  [#4264](https://github.com/skycoin/skywire/pull/4264)
-   fix(router): tighten mux active-latency band in capacity (aggregation) mode  [#4263](https://github.com/skycoin/skywire/pull/4263)
-   fix(router): shed a goodput-black-hole mux leg even when the frontier is healthy  [#4262](https://github.com/skycoin/skywire/pull/4262)
-   fix(cli): proxy switch — gate on leg-alive not non-standby; render switch op  [#4261](https://github.com/skycoin/skywire/pull/4261)
-   feat(cli): proxy switch — seamless in-flight route switch for a proxy session  [#4258](https://github.com/skycoin/skywire/pull/4258)
-   wasmhv: take the real-origin substrate from a library  [#4257](https://github.com/skycoin/skywire/pull/4257)
-   fix(router): demote gross-latency-outlier mux legs to standby in all modes  [#4256](https://github.com/skycoin/skywire/pull/4256)
-   feat(router): proactive head-of-line retransmit for mux route groups  [#4255](https://github.com/skycoin/skywire/pull/4255)
-   fix(router): stop ECF over-feeding a congesting mux leg (BDP trap + cold-start dump)  [#4254](https://github.com/skycoin/skywire/pull/4254)
-   fix(router): reject same-LAN mux legs at mux-set (unblocks single-connection aggregation)  [#4253](https://github.com/skycoin/skywire/pull/4253)
-   fix(cxds): startup GC + compaction to reclaim unbounded cxds.db growth  [#4252](https://github.com/skycoin/skywire/pull/4252)
-   feat(dmsgscp): idle (no-progress) transfer deadline, not a fixed total timeout  [#4251](https://github.com/skycoin/skywire/pull/4251)
-   feat(router): default the mux scheduler to ECF (completion-aware) to stop the multi-leg collapse  [#4250](https://github.com/skycoin/skywire/pull/4250)
-   fix(ui): readable dark-theme text (routing sub-tabs, chat open-full, form labels) + rebuilt wasm blob  [#4249](https://github.com/skycoin/skywire/pull/4249)
-   feat(router): latency-band admission keeps the mux active set homogeneous  [#4248](https://github.com/skycoin/skywire/pull/4248)
-   fix(router): replace a sole black-holing mux leg instead of wedging at zero  [#4247](https://github.com/skycoin/skywire/pull/4247)
-   fix(router): retire a manually-removed mux leg on the far endpoint (CloseLegRetired)  [#4246](https://github.com/skycoin/skywire/pull/4246)
-   fix(skysocks): SO_REUSEADDR/PORT on the SOCKS listener so reconnect can't leak the port  [#4245](https://github.com/skycoin/skywire/pull/4245)
-   feat(router): allow removing the primary mux leg (re-home) for exact route control  [#4244](https://github.com/skycoin/skywire/pull/4244)
-   feat(cli): proxy start --route pins explicit route(s) at session start  [#4243](https://github.com/skycoin/skywire/pull/4243)
-   fix(visor): bound service-health probes so one dead endpoint can't stall `visor state`  [#4241](https://github.com/skycoin/skywire/pull/4241)
-   feat(router): ECF predictive mux scheduler (proxy mux mode ecf)  [#4240](https://github.com/skycoin/skywire/pull/4240)
-   fix(router): size reorder buffer to the BDP + drop-not-skip at the OOM cap  [#4239](https://github.com/skycoin/skywire/pull/4239)
-   fix(router): reorder never skips a gap — fixes proxied-stream corruption (bad record mac)  [#4238](https://github.com/skycoin/skywire/pull/4238)
-   fix(lint): clear develop golangci-lint debt  [#4237](https://github.com/skycoin/skywire/pull/4237)
-   fix(cli): proxy mux cap/width confirmation text  [#4236](https://github.com/skycoin/skywire/pull/4236)
-   fix(lint): rf/store test compile break + datagram/gosec/gofmt  [#4235](https://github.com/skycoin/skywire/pull/4235)
-   feat(router): live mux control (mode/cap/width) + goodput weighted-ramp  [#4234](https://github.com/skycoin/skywire/pull/4234)
-   fix(router): timer-driven reorder flush so a stalled leg degrades gracefully  [#4233](https://github.com/skycoin/skywire/pull/4233)
-   feat(proxy): split status.skysocks goodput up/down + per-direction share bars + direct-leg rtt fix  [#4232](https://github.com/skycoin/skywire/pull/4232)
-   feat(cli): tp disc per-visor stats (-s --pk), bare -p = local pk, --type keys-by-type  [#4231](https://github.com/skycoin/skywire/pull/4231)
-   fix(router): new aux legs enter warm standby on add, engine promotes them paced  [#4230](https://github.com/skycoin/skywire/pull/4230)
-   feat(proxy): per-leg goodput (bytes/sec) on status.skysocks + proxy tree + mux JSON  [#4229](https://github.com/skycoin/skywire/pull/4229)
-   fix(router): initiator now offers per-frame noise on its first handshake  [#4228](https://github.com/skycoin/skywire/pull/4228)
-   feat(visor): widen visor state — mux distribution/reorder/agg-bytes + transport byte totals  [#4227](https://github.com/skycoin/skywire/pull/4227)
-   feat(visor): expose per_frame_noise on route groups in visor state / mux info  [#4226](https://github.com/skycoin/skywire/pull/4226)
-   fix(router): per-frame noise handshake writes a KK message only on our turn  [#4225](https://github.com/skycoin/skywire/pull/4225)
-   fix(ci): publish-binary upserts the rolling release (no delete-before-create)  [#4224](https://github.com/skycoin/skywire/pull/4224)
-   chore: keep go.mod at minimal required go (1.26.4)  [#4223](https://github.com/skycoin/skywire/pull/4223)
-   fix(skysocks): break the IPC read loop on error instead of spinning  [#4222](https://github.com/skycoin/skywire/pull/4222)
-   chore(ci): latest Go (1.27.0) + golangci-lint (v2.13.1)  [#4221](https://github.com/skycoin/skywire/pull/4221)
-   feat(router): per-frame noise on by default (drop the env gate)  [#4220](https://github.com/skycoin/skywire/pull/4220)
-   feat(router): per-frame noise inverse-mux (single-stream aggregation), env-gated OFF  [#4219](https://github.com/skycoin/skywire/pull/4219)
-   docs: RFC — single-stream mux aggregation via per-frame noise  [#4218](https://github.com/skycoin/skywire/pull/4218)
-   fix(router): don't hold r.mx during the accept send in IntroduceRules  [#4217](https://github.com/skycoin/skywire/pull/4217)
-   feat(skysocks): re-dial dead multi-tunnels + reliable sequential diversification  [#4216](https://github.com/skycoin/skywire/pull/4216)
-   feat(cli): expose --tunnels on 'proxy start' to drive multi-tunnel aggregation  [#4215](https://github.com/skycoin/skywire/pull/4215)
-   feat(router): auto-diversify multi-tunnel dials over disjoint first-hop transports  [#4214](https://github.com/skycoin/skywire/pull/4214)
-   feat(skysocks): multi-session client with least-loaded connection striping (aggregation foundation)  [#4213](https://github.com/skycoin/skywire/pull/4213)
-   docs: RFC for mesh bandwidth aggregation via connection-striped independent flows  [#4212](https://github.com/skycoin/skywire/pull/4212)
-   perf(skysocks): raise the yamux stream window from 256KB to 16MB for the mesh BDP  [#4211](https://github.com/skycoin/skywire/pull/4211)
-   fix(yamux): return error instead of spinning when a stream's session is shut down mid-read  [#4210](https://github.com/skycoin/skywire/pull/4210)
-   fix(router): establish mux legs asynchronously so a dial serves on its primary immediately  [#4209](https://github.com/skycoin/skywire/pull/4209)
-   fix(router): swap out a sustained-bad primary leg instead of riding it forever  [#4208](https://github.com/skycoin/skywire/pull/4208)
-   feat(router): instant warm-standby failover so a leg death never dead-ends a connection  [#4207](https://github.com/skycoin/skywire/pull/4207)
-   fix(router): cap the adaptive active mux at adaptCap even under load  [#4206](https://github.com/skycoin/skywire/pull/4206)
-   feat(proxystatus): color the exit red and each hop level distinctly in the route tree  [#4205](https://github.com/skycoin/skywire/pull/4205)
-   fix(skysocks): serve status.skysocks in-process even when disconnected from the exit  [#4204](https://github.com/skycoin/skywire/pull/4204)
-   fix(proxystatus): fixed-width leg metrics so the tree doesn't shift on updates  [#4203](https://github.com/skycoin/skywire/pull/4203)
-   feat(router): truly uncap the mux standby pool, filled in the background  [#4202](https://github.com/skycoin/skywire/pull/4202)
-   fix(router): adaptive tick evicts low-throughput legs, not just dead ones  [#4201](https://github.com/skycoin/skywire/pull/4201)
-   fix(router): adaptive tick converges the mux to the fastest N legs in one tick  [#4200](https://github.com/skycoin/skywire/pull/4200)
-   fix(proxystatus): center the full-bleed route tree on the page  [#4199](https://github.com/skycoin/skywire/pull/4199)
-   fix(proxyinterstitial): always serve status.skysocks in-process, never the interstitial  [#4198](https://github.com/skycoin/skywire/pull/4198)
-   fix(proxystatus): full-bleed the route tree so a wide route set shows whole  [#4197](https://github.com/skycoin/skywire/pull/4197)
-   fix(cli): move the network transport summary onto tp disc -s  [#4196](https://github.com/skycoin/skywire/pull/4196)
-   feat(sd): shard services CXO feed to one leaf per type  [#4195](https://github.com/skycoin/skywire/pull/4195)
-   feat(tpd): server-side transport summary endpoint (tp -s without fetching all)  [#4194](https://github.com/skycoin/skywire/pull/4194)
-   feat(routing): uncap the adaptive warm-standby pool (adaptStandbyMax 2 -> 60)  [#4193](https://github.com/skycoin/skywire/pull/4193)
-   feat(proxystatus): status.skysocks layout pass (header bw meters, label-tree header, right-anchored root, resizable black log pane)  [#4192](https://github.com/skycoin/skywire/pull/4192)
-   feat(dmsgd): batch clients-by-server CXO feed to one leaf per server  [#4191](https://github.com/skycoin/skywire/pull/4191)
-   feat(telemetry): sharded compact-binary CXO telemetry leaves (fillable busy-hub Root)  [#4190](https://github.com/skycoin/skywire/pull/4190)
-   Revert tp-list feed consolidation (#4184): busy-hub combined-Root fill undercounts TPD  [#4189](https://github.com/skycoin/skywire/pull/4189)
-   fix(bitree): restore bilateral route tree (left summary / right hops) + connect glyphs  [#4188](https://github.com/skycoin/skywire/pull/4188)
-   fix(routing): symmetric bidirectional adaptive mux so the warm-standby pool fills  [#4187](https://github.com/skycoin/skywire/pull/4187)
-   feat(dmsgdisc): resolve dmsg entries over CXO (clients-by-server feed) with HTTP fallback  [#4186](https://github.com/skycoin/skywire/pull/4186)
-   chore(pkg): move transport-discovery under pkg/deployment/tpd (batch 3)  [#4185](https://github.com/skycoin/skywire/pull/4185)
-   feat(cxo): consolidate tp-list onto the telemetry feed (one visor↔TPD connection)  [#4184](https://github.com/skycoin/skywire/pull/4184)
-   feat(proxystatus): anchor tree root, live log pane, bw meters, stream detail; interstitial fall-through  [#4183](https://github.com/skycoin/skywire/pull/4183)
-   fix(autoconnect): remove pre-transport dmsg reachability probe  [#4182](https://github.com/skycoin/skywire/pull/4182)
-   chore(pkg): group deployment-service servers under pkg/deployment (batch 2)  [#4181](https://github.com/skycoin/skywire/pull/4181)
-   chore(pkg): group stray packages under their parents (batch 1)  [#4180](https://github.com/skycoin/skywire/pull/4180)
-   fix(cxo): progress-reset fill stall timer + hard total ceiling  [#4179](https://github.com/skycoin/skywire/pull/4179)
-   fix(wasm): rebuild committed wasmgo blob from a clean tree  [#4178](https://github.com/skycoin/skywire/pull/4178)
-   feat(router): exempt control-plane ports from the routing policy by default  [#4177](https://github.com/skycoin/skywire/pull/4177)
-   fix(cli): honor --json/--jq/--shape on rg, skychat alias/pair list, visor reward  [#4176](https://github.com/skycoin/skywire/pull/4176)
-   feat(ar): bind address-resolver registration over CXO (dual-write, additive)  [#4175](https://github.com/skycoin/skywire/pull/4175)
-   Revert "consolidate tp-list onto the telemetry feed (#4171)"  [#4174](https://github.com/skycoin/skywire/pull/4174)
-   fix(skyenv): move AR-bind/SD-reg CXO ports off collision (68->71, 70->72)  [#4173](https://github.com/skycoin/skywire/pull/4173)
-   chore(skyenv): allocate CXO ports for AR-bind (68) and SD-registration (70)  [#4172](https://github.com/skycoin/skywire/pull/4172)
-   feat(cxo): consolidate tp-list onto the telemetry feed (one visor↔TPD conn)  [#4171](https://github.com/skycoin/skywire/pull/4171)
-   fix(tpd): cache expired-transport SCAN off the CXO metrics publisher hot path  [#4170](https://github.com/skycoin/skywire/pull/4170)
-   fix(rewards): read survey public key from public_key, not pk  [#4169](https://github.com/skycoin/skywire/pull/4169)
-   fix(tpd): bind CXO aggregator node identity to TPD's key so gated visors accept it  [#4168](https://github.com/skycoin/skywire/pull/4168)
-   build(deps): vendor latest skycoin develop  [#4167](https://github.com/skycoin/skywire/pull/4167)
-   fix(lint): clear accumulated errcheck failures on develop  [#4166](https://github.com/skycoin/skywire/pull/4166)
-   feat(proxy): bilateral route-group tree on status page + `proxy tree` CLI  [#4165](https://github.com/skycoin/skywire/pull/4165)
-   fix(cxo): make TPD uptime feed fillable (one gzipped leaf per window)  [#4164](https://github.com/skycoin/skywire/pull/4164)
-   build(deps): bump 0magnet/xterm-go to latest  [#4163](https://github.com/skycoin/skywire/pull/4163)
-   release: compress linux/darwin archives with xz -9e (smaller downloads)  [#4162](https://github.com/skycoin/skywire/pull/4162)
-   feat(cxo): gate service-consumed CXO feeds with a subscriber allowlist  [#4161](https://github.com/skycoin/skywire/pull/4161)
-   build(deps): update Go deps + skycoin to latest develop  [#4160](https://github.com/skycoin/skywire/pull/4160)
-   fix(stats): change-gate the per-transport current-leaf mirror to stop idle Root churn  [#4159](https://github.com/skycoin/skywire/pull/4159)
-   feat(proxystatus): per-app event/log ring; populate status Events+Logs; add `proxy log`  [#4158](https://github.com/skycoin/skywire/pull/4158)
-   fix(stats): prune persisted dead-transport current leaves; introspect feed live/dead  [#4157](https://github.com/skycoin/skywire/pull/4157)
-   fix(tpd): tp-list CXO aggregator never accepted inbound feeds (node listener bind collision)  [#4156](https://github.com/skycoin/skywire/pull/4156)
-   feat(visor): surface the dedicated tp-list CXO feed in visor state  [#4155](https://github.com/skycoin/skywire/pull/4155)
-   chore(dev): dev-visor-loop uses go install, runs from GOBIN  [#4154](https://github.com/skycoin/skywire/pull/4154)
-   fix(lint): misspell modelling -> modeling  [#4153](https://github.com/skycoin/skywire/pull/4153)
-   cxo: publish tp-list on its own dedicated feed so TPD fills it completely  [#4152](https://github.com/skycoin/skywire/pull/4152)
-   fix(lint): clear develop lint failures (misspell, errcheck check-blank)  [#4151](https://github.com/skycoin/skywire/pull/4151)
-   route calc: add --source tps (authoritative src+dst transports via setup node)  [#4150](https://github.com/skycoin/skywire/pull/4150)
-   fix(cxo): cure publisher missing-object freeze from batch-rollback cache desync  [#4149](https://github.com/skycoin/skywire/pull/4149)
-   fix(gitignore): anchor dist/ so develop-latest arm builds aren't stamped +dirty  [#4148](https://github.com/skycoin/skywire/pull/4148)
-   feat(dmsg): optional in-process dmsg server sharing the visor key (dmsg.server, default off)  [#4147](https://github.com/skycoin/skywire/pull/4147)
-   feat(visor): make hypervisor-transport autoconnect configurable (transport.hypervisor_autoconnect, default on)  [#4146](https://github.com/skycoin/skywire/pull/4146)
-   tui: browse the command tree and its help interactively  [#4145](https://github.com/skycoin/skywire/pull/4145)
-   help: put the rain behind every command's help, not just the root's  [#4144](https://github.com/skycoin/skywire/pull/4144)
-   status.skysocks route tree: tp-tree-style header/legend + active/standby colors; tp tree fetches days=1  [#4143](https://github.com/skycoin/skywire/pull/4143)
-   help: stop dimming a rectangle around the help text  [#4142](https://github.com/skycoin/skywire/pull/4142)
-   fix(stats): sync only live transports' current leaf to the TPD feed  [#4141](https://github.com/skycoin/skywire/pull/4141)
-   fix(router): record full forward hops for every mux leg's telemetry  [#4140](https://github.com/skycoin/skywire/pull/4140)
-   feat(proxystatus): page-level route tree + scroll-preserving live-swap; drop direct/multihop words  [#4139](https://github.com/skycoin/skywire/pull/4139)
-   feat(routing-policy): size the adaptive forward mux on upload (bidirectional sizing)  [#4138](https://github.com/skycoin/skywire/pull/4138)
-   feat(proxystatus): fold per-leg mux table + full routes into one route tree  [#4137](https://github.com/skycoin/skywire/pull/4137)
-   feat(proxystatus): status.skysocks live-update UX pass (selection-safe copy + polish)  [#4136](https://github.com/skycoin/skywire/pull/4136)
-   fix(lint): clear accumulated develop lint debt (misspell/gosec/gofmt)  [#4135](https://github.com/skycoin/skywire/pull/4135)
-   help: print the help screen over a still frame of the code rain  [#4134](https://github.com/skycoin/skywire/pull/4134)
-   feat(status): make status.skysocks live-update a WebSocket control channel  [#4133](https://github.com/skycoin/skywire/pull/4133)
-   feat(skynetca): permit .skysocks in the resolver CA name constraints  [#4132](https://github.com/skycoin/skywire/pull/4132)
-   fix(router): buffer aux mux legs racing responder route-group init (#80)  [#4131](https://github.com/skycoin/skywire/pull/4131)
-   fix(build): update dmsgweb/skynetweb ServeSOCKS5 test callers for new arg  [#4130](https://github.com/skycoin/skywire/pull/4130)
-   fix(proxystatus): label a 1-hop leg 'direct' regardless of route-group orientation  [#4129](https://github.com/skycoin/skywire/pull/4129)
-   fix(skysocks): keep status.skysocks reachable when the exit route is down  [#4128](https://github.com/skycoin/skywire/pull/4128)
-   test(transport): regression guard for outbound re-register nudge (#4122)  [#4127](https://github.com/skycoin/skywire/pull/4127)
-   feat(proxystatus): live-push status.skysocks via SSE (drop full-page meta refresh)  [#4126](https://github.com/skycoin/skywire/pull/4126)
-   fix(proxystatus): restore rich per-leg mux telemetry on status.skysocks  [#4125](https://github.com/skycoin/skywire/pull/4125)
-   fix(lint): misspell signalled -> signaled  [#4124](https://github.com/skycoin/skywire/pull/4124)
-   refactor(cmdutil): dedup Execute/exampleJSON/commaSplit into shared helpers  [#4123](https://github.com/skycoin/skywire/pull/4123)
-   fix(transport): nudge re-register on outbound dial + expand wasm visor-state introspection  [#4122](https://github.com/skycoin/skywire/pull/4122)
-   test(router): raise pkg/router coverage 38.5% -> 41.8%  [#4121](https://github.com/skycoin/skywire/pull/4121)
-   test(transport): raise transport-layer coverage (manager helpers, AR binds, handshake, setup RPC)  [#4120](https://github.com/skycoin/skywire/pull/4120)
-   test(skysocks,skyroute): cover SOCKS5 sniff/whitelist and mux pool branches  [#4119](https://github.com/skycoin/skywire/pull/4119)

## 1.3.92

422 PRs on top of v1.3.91 — the release where routing policy became a first-class subsystem. Headlines: **congestion-control presets** land as policy rather than hard-coded behaviour, the **browser wasm-visor** gets a long run of stability work, and **CXO publish health** becomes visible in `visor state`.

-   **Routing policy presets.** Experimental `coupled` (MPTCP-style coupled congestion control) and `ledbat` (delay-based scavenger) presets, alongside the policy engine that selects between them per dial.
-   **Per-leg route reporting.** `proxystatus` gains the full per-leg route — every hop with full public keys, per-hop transport type and latency, direct-vs-multihop, and a receive bar.
-   **CXO health in visor state.** Per-feed publish health under `.cxo`, and the TPD aggregator learns to gunzip CXO leaf bodies reader-first.
-   **Browser wasm-visor.** 29 PRs of stability work on the in-page visor.

-   ci(release): GOPROXY=proxy.golang.org,direct (dead-repo dep resilience)  [#4117](https://github.com/skycoin/skywire/pull/4117)
-   fix(router): retry route-ID reservation on a mid-call stream reset  [#4116](https://github.com/skycoin/skywire/pull/4116)
-   ci(release): fail-fast: false on the linux arch matrix  [#4115](https://github.com/skycoin/skywire/pull/4115)
-   fix(lint): clear golangci-lint failures blocking develop CI  [#4114](https://github.com/skycoin/skywire/pull/4114)
-   feat(routing-policy): add experimental 'coupled' preset (MPTCP-style coupled congestion control)  [#4113](https://github.com/skycoin/skywire/pull/4113)
-   feat(routing-policy): experimental 'ledbat' delay-based scavenger preset  [#4112](https://github.com/skycoin/skywire/pull/4112)
-   fix(router): guarantee mux legs are fully disjoint and loop-free  [#4111](https://github.com/skycoin/skywire/pull/4111)
-   fix(ci): publish-binary — literal env prefixes (CGO_ENABLED=0: command not found)  [#4110](https://github.com/skycoin/skywire/pull/4110)
-   fix(policy): adaptive reverse pool 32→3 (source drifted from intended); rebuild bundle.wasm  [#4109](https://github.com/skycoin/skywire/pull/4109)
-   fix(router): control-plane ports stay single-route (no warm-standby mux)  [#4108](https://github.com/skycoin/skywire/pull/4108)
-   feat(router,proxystatus): full per-leg route — every hop, full PKs, per-hop type+latency  [#4107](https://github.com/skycoin/skywire/pull/4107)
-   feat(router,proxystatus): per-leg route latency + direct/multihop + recv bar  [#4106](https://github.com/skycoin/skywire/pull/4106)
-   feat(tpd): aggregator gunzips CXO leaf bodies (reader-first for gzip)  [#4105](https://github.com/skycoin/skywire/pull/4105)
-   fix(stats,cxo): publish only current data to TPD, keep history bbolt-only  [#4104](https://github.com/skycoin/skywire/pull/4104)
-   feat(cxo,visor): surface per-feed CXO publish health in `visor state` (.cxo)  [#4103](https://github.com/skycoin/skywire/pull/4103)
-   diag(cxo): capture publishRoot terminal freeze error type  [#4102](https://github.com/skycoin/skywire/pull/4102)
-   fix(tpd): a reporter with no prior transports is not a reconcile error  [#4101](https://github.com/skycoin/skywire/pull/4101)
-   fix(router): a direct dial bypasses the RSN-oracle 2-hop path  [#4100](https://github.com/skycoin/skywire/pull/4100)
-   fix(router): control forwards + same-LAN destinations stay direct (stop policy fighting --direct)  [#4099](https://github.com/skycoin/skywire/pull/4099)
-   fix(transport): AR registration is config-only; no runtime transport-count deregister  [#4098](https://github.com/skycoin/skywire/pull/4098)
-   test(cxo): publisher→subscriber convergence-lag-under-churn measurement  [#4097](https://github.com/skycoin/skywire/pull/4097)
-   fix(router): skip rotation add-leg plans reusing an in-group transport before dialing  [#4096](https://github.com/skycoin/skywire/pull/4096)
-   fix(router): skip mux grow-leg plans reusing an in-group transport before dialing  [#4095](https://github.com/skycoin/skywire/pull/4095)
-   feat(proxystatus): per-layer status ownership; move status.skysocks to skysocks-client  [#4094](https://github.com/skycoin/skywire/pull/4094)
-   feat(config): enable native browse-origin by default for HTTPS proxy-status pages  [#4093](https://github.com/skycoin/skywire/pull/4093)
-   fix(cxo): best-effort publisher hydrate — skip dangling sub-refs, keep the tree, name the culprit  [#4092](https://github.com/skycoin/skywire/pull/4092)
-   feat(router): composite destination-transport oracle + large warm-standby pool for adaptive routing  [#4091](https://github.com/skycoin/skywire/pull/4091)
-   diag(cxo): name the object behind the treestore publish-freeze (#4088)  [#4089](https://github.com/skycoin/skywire/pull/4089)
-   ci(publish-binary): rolling develop build covers all 6 linux arches (386, riscv64, armv6)  [#4087](https://github.com/skycoin/skywire/pull/4087)
-   fix(lint): clear develop golangci-lint debt (unblocks all open PRs)  [#4085](https://github.com/skycoin/skywire/pull/4085)
-   build(deps): bump brace-expansion in /static/skywire-manager-src  [#4084](https://github.com/skycoin/skywire/pull/4084)
-   feat(router): capacity-weighted mux distribution + responder bulk-spread default  [#4083](https://github.com/skycoin/skywire/pull/4083)
-   test(policy): fix stale avoidDirect no-signal case (red on develop)  [#4082](https://github.com/skycoin/skywire/pull/4082)
-   feat(router): exclude same-LAN peers as routing intermediates (route diversity)  [#4081](https://github.com/skycoin/skywire/pull/4081)
-   feat(transport): per-type connection metadata on `visor state` + `tp`; state snapshot completeness  [#4080](https://github.com/skycoin/skywire/pull/4080)
-   feat(routing-policy): adaptive default holds warm standby + asymmetric F/R (app-agnostic)  [#4079](https://github.com/skycoin/skywire/pull/4079)
-   feat(router): parallel candidate route-group setup (steady-connection fix)  [#4078](https://github.com/skycoin/skywire/pull/4078)
-   feat(skywire-cli): svc gains --testenv + URL overrides + fetch-chain flags; --jq/--shape parity  [#4077](https://github.com/skycoin/skywire/pull/4077)
-   fix(cli): apps-cli — skycoin daemon list --json, group catalog self-listing, help polish  [#4076](https://github.com/skycoin/skywire/pull/4076)
-   fix(skywire-cli): default uptime queries to v3 to match the CXO mirror  [#4075](https://github.com/skycoin/skywire/pull/4075)
-   fix(skywire-cli): error on unknown subcommand under combined 'skywire cli'  [#4074](https://github.com/skycoin/skywire/pull/4074)
-   fix(skywire-cli): render ungrouped subcommands in help (Additional Commands block)  [#4073](https://github.com/skycoin/skywire/pull/4073)
-   cli(survey): honor --json/--jq/--shape global output flags  [#4072](https://github.com/skycoin/skywire/pull/4072)
-   fix(cli/visor): unhide 6 invisible subcommands, fix hv-ui port bug, standardize flags  [#4071](https://github.com/skycoin/skywire/pull/4071)
-   cli(hv/log/util/pv/survey/completion): fix small bugs, standardize help  [#4070](https://github.com/skycoin/skywire/pull/4070)
-   fix(config-cli): fix flag bugs + expose policy_per_dial in `config gen`  [#4069](https://github.com/skycoin/skywire/pull/4069)
-   fix(cli-route): validate --source on all paths; standardize route/rg flag vocabulary + help  [#4068](https://github.com/skycoin/skywire/pull/4068)
-   fix(skywire-cli): audit + standardize dmsg/mdisc command groups  [#4067](https://github.com/skycoin/skywire/pull/4067)
-   cli(sd/ut/svc): fix min-version + tpd-uptime handling, standardize flags & help  [#4066](https://github.com/skycoin/skywire/pull/4066)
-   refactor(cli/pty): standardize remote-access group; fix host whitelist-size log; drop dead dmsgpty pkg  [#4065](https://github.com/skycoin/skywire/pull/4065)
-   fix(rewards-cli): reward/rewards surface audit — delete exit code, lazy help RPC, quiet lookup, help + cross-refs  [#4064](https://github.com/skycoin/skywire/pull/4064)
-   feat(skywire-cli): standardize + clarify tp/tps transport commands  [#4062](https://github.com/skycoin/skywire/pull/4062)
-   fix(cli): proxy/forwarding surface audit — status IP bug, geoip help, mux/routes vocab converge (#77)  [#4061](https://github.com/skycoin/skywire/pull/4061)
-   fix(skywire-cli): tp v online filter + tp uptime graceful degradation  [#4060](https://github.com/skycoin/skywire/pull/4060)
-   fix(lint): clear errcheck/gosec debt in status+interstitial code  [#4059](https://github.com/skycoin/skywire/pull/4059)
-   feat(skywire-cli): unified transport vocabulary + scheme targets for the remote-access trinity  [#4058](https://github.com/skycoin/skywire/pull/4058)
-   fix(router): retransmit route-group setup handshake to prevent intermittent skynet route drops  [#4057](https://github.com/skycoin/skywire/pull/4057)
-   fix(dmsgscp): accept route-group skynet conns (routing.Addr); ensure transport before scp skynet dial  [#4056](https://github.com/skycoin/skywire/pull/4056)
-   feat(interstitial): stream live route-setup progress via chunked encoding  [#4054](https://github.com/skycoin/skywire/pull/4054)
-   feat(proxy-status): serve status over browse-origin real cert + AA contrast  [#4053](https://github.com/skycoin/skywire/pull/4053)
-   fix(routing): honor mux_routes=1 as a single-route-group dial  [#4052](https://github.com/skycoin/skywire/pull/4052)
-   fix(router): stop route-setup drops from wedging apps in "starting"; cascade→classic fallback  [#4051](https://github.com/skycoin/skywire/pull/4051)
-   feat(proxy): per-proxy status hosts + HTTPS interstitial permit-gate  [#4050](https://github.com/skycoin/skywire/pull/4050)
-   feat(routeviz): route visualizer scaffold — live per-leg route view + /route-mux HTTP seam  [#4049](https://github.com/skycoin/skywire/pull/4049)
-   lint: fix errcheck/misspell/unparam on develop (make check clean)  [#4048](https://github.com/skycoin/skywire/pull/4048)
-   feat(routing-policy): adaptive default proactively holds warm standby + asymmetric F/R (app-agnostic)  [#4047](https://github.com/skycoin/skywire/pull/4047)
-   fix(tpd): land a busy visor's full transport list via a targeted, bounded discovery-leaf fetch  [#4046](https://github.com/skycoin/skywire/pull/4046)
-   feat(routing-policy): make `adaptive` the default routing policy; prefer transport-diverse routes  [#4045](https://github.com/skycoin/skywire/pull/4045)
-   feat(routing-policy): --override key=value on `mux plot --pk` → policy CLIOverrides  [#4044](https://github.com/skycoin/skywire/pull/4044)
-   feat(routing-policy): AR-resolve geo layer for off-path intermediary hops  [#4043](https://github.com/skycoin/skywire/pull/4043)
-   fix(router): flush in-flight retx window on mux leg demote (no-dip hot-swap)  [#4042](https://github.com/skycoin/skywire/pull/4042)
-   fix(cxo): repair filling-item so treestore self-heal can restore evicted objects (real TPD/[stats] freeze)  [#4041](https://github.com/skycoin/skywire/pull/4041)
-   feat(routing-policy): native-Go preset engine so the TinyGo wasm-visor can run presets (no wazero)  [#4040](https://github.com/skycoin/skywire/pull/4040)
-   feat(cli): drop the `web` browser-UI command from the default binary  [#4039](https://github.com/skycoin/skywire/pull/4039)
-   feat(cli): drop the 'web' browser-UI command from the default binary  [#4038](https://github.com/skycoin/skywire/pull/4038)
-   fix(ci): binary channel + module-integrity build the repo-root skywire, not ./cmd/skywire  [#4037](https://github.com/skycoin/skywire/pull/4037)
-   fix(cxo/treestore): self-heal publisher freeze on evicted cached object  [#4036](https://github.com/skycoin/skywire/pull/4036)
-   feat(cli): stream 'proxy mux plot' default -n mode over gRPC (smooth, no more 1 Hz unary poll)  [#4035](https://github.com/skycoin/skywire/pull/4035)
-   fix(cxo): don't crash the visor on a recoverable DB miss serving/storing objects  [#4034](https://github.com/skycoin/skywire/pull/4034)
-   config(envfiles): refresh /etc/skywire.conf template — drop 'stable', add binary channels + GOPROXY_MODE  [#4033](https://github.com/skycoin/skywire/pull/4033)
-   feat(cli): add --bind listen-address flag to rewards ui, deprecate --port  [#4032](https://github.com/skycoin/skywire/pull/4032)
-   chore: remove stable auto-update channel machinery  [#4031](https://github.com/skycoin/skywire/pull/4031)
-   ci: publish a rolling compressed linux binary per merge (binary auto-update artifact)  [#4030](https://github.com/skycoin/skywire/pull/4030)
-   ci: module-mode integrity check (catch go.sum/module breaks vendored CI misses)  [#4029](https://github.com/skycoin/skywire/pull/4029)
-   feat(routing-policy): conditional presets — geo-avoid / transport-diverse / trust-tiered / time-of-day  [#4028](https://github.com/skycoin/skywire/pull/4028)
-   feat(cli): proxy mux plot on 0magnet/plot-go pipeline + --tui alt-screen  [#4027](https://github.com/skycoin/skywire/pull/4027)
-   test(cli): allowlist 'proxy mux plot' in the JSON-contract test (fix red develop)  [#4026](https://github.com/skycoin/skywire/pull/4026)
-   feat(visor): 'visor state' — one-call curated snapshot of live runtime state  [#4025](https://github.com/skycoin/skywire/pull/4025)
-   fix(router): full-window SACK so a persistent mux gap can't wedge the stream (#86)  [#4024](https://github.com/skycoin/skywire/pull/4024)
-   feat(cli): proxy mux plot --pk — live per-route chart of a controlled measured mux  [#4023](https://github.com/skycoin/skywire/pull/4023)
-   chore(deps): bump 0magnet/xterm-go for cursor-blink Dispose fix  [#4022](https://github.com/skycoin/skywire/pull/4022)
-   feat(cli): live per-leg bandwidth + RTT terminal chart (proxy mux plot)  [#4020](https://github.com/skycoin/skywire/pull/4020)
-   fix(lint): clear golangci-lint issues from the discovery/routing series  [#4019](https://github.com/skycoin/skywire/pull/4019)
-   feat(router): optional RSN-oracle 2-hop route calc via the setup-node (TPD-independent)  [#4018](https://github.com/skycoin/skywire/pull/4018)
-   fix(tpd-cxo): make TPD's published feeds fillable by subscribers (live CXO data)  [#4017](https://github.com/skycoin/skywire/pull/4017)
-   fix(routing-policy): serialize policy-bw leg_lifecycle events to NDJSON  [#4016](https://github.com/skycoin/skywire/pull/4016)
-   feat(routing-policy): in-process policy-bw rig — prove WASM presets per-leg over time  [#4015](https://github.com/skycoin/skywire/pull/4015)
-   fix(cxo): adopt existing conn instead of redundant reverse-dial that evicts the fill mid-transfer  [#4014](https://github.com/skycoin/skywire/pull/4014)
-   feat(transport): publish-then-verify discovery conformance loop  [#4013](https://github.com/skycoin/skywire/pull/4013)
-   fix(tpd): publish discovery list as top-level leaf so it fills first  [#4012](https://github.com/skycoin/skywire/pull/4012)
-   perf(stats): stop mirroring unused daily rollup leaf to CXO  [#4011](https://github.com/skycoin/skywire/pull/4011)
-   feat(tpd): compact transports/list discovery snapshot (~62% smaller)  [#4010](https://github.com/skycoin/skywire/pull/4010)
-   fix(tpd): land transports/list from partial CXO fills (heal discovery gap)  [#4009](https://github.com/skycoin/skywire/pull/4009)
-   fix(tpd): dial back to visors so CXO fills have a stable source (close the discovery gap)  [#4008](https://github.com/skycoin/skywire/pull/4008)
-   ci: persist $GOCACHE across runs so unchanged packages skip re-test  [#4007](https://github.com/skycoin/skywire/pull/4007)
-   feat(router): fast-prune a mux leg that black-holes bulk data (recover throughput)  [#4006](https://github.com/skycoin/skywire/pull/4006)
-   fix(router): never skip a reorder-buffer gap — the mux carries a noise stream  [#4005](https://github.com/skycoin/skywire/pull/4005)
-   feat(routing-policy): lift the performance presets onto the shared no-dip engine  [#4004](https://github.com/skycoin/skywire/pull/4004)
-   fix(skynet-client): silence unparam on the DialRoute ctx param (follow-up to #4002)  [#4003](https://github.com/skycoin/skywire/pull/4003)
-   fix(skynet-client): carry the forward over a warm port-59 route group via skyroute.Pool  [#4002](https://github.com/skycoin/skywire/pull/4002)
-   fix(router): GrowMuxRoute plans aux legs route-finder-first (cost-ranked), like the other two mux planners  [#4001](https://github.com/skycoin/skywire/pull/4001)
-   feat(dmsgweb,skynetweb): serve the branded interstitial over HTTPS (TLS-MITM) for mesh hosts  [#4000](https://github.com/skycoin/skywire/pull/4000)
-   feat(proxyinterstitial): real Skycoin cloud brand mark + status-line on the mesh-route interstitial  [#3999](https://github.com/skycoin/skywire/pull/3999)
-   feat(routing-policy): rotating-bw on_tick manages standby only, never adds/drops — kills rotation thrash  [#3998](https://github.com/skycoin/skywire/pull/3998)
-   fix(routing-policy): rotating-bw applies to any opt-in app, not just built-in binary names  [#3997](https://github.com/skycoin/skywire/pull/3997)
-   feat(routing-policy): rotating-bw manages mux by standby + transport quality, never drops legs  [#3996](https://github.com/skycoin/skywire/pull/3996)
-   feat(routing-policy): rotating-bw rotates by warm hot-swap, not tear-and-rebuild (gate-5)  [#3995](https://github.com/skycoin/skywire/pull/3995)
-   feat(routing): per-app override of the visor-global min_hops/mux_routes  [#3994](https://github.com/skycoin/skywire/pull/3994)
-   feat(skynet): --routing-policy flag on `skynet start` (parity with proxy start)  [#3993](https://github.com/skycoin/skywire/pull/3993)
-   feat(proxy): loadtest rig — steady controlled sink + exact goodput/gap recorder  [#3992](https://github.com/skycoin/skywire/pull/3992)
-   fix(proxyinterstitial): mechanism-specific copy + Skywire cloud branding  [#3991](https://github.com/skycoin/skywire/pull/3991)
-   feat(mux-bw): rank disjoint route candidates by transport cost (no more webrtc legs)  [#3990](https://github.com/skycoin/skywire/pull/3990)
-   feat(router): rank routes by MEASURED transport throughput, type only a prior (phase 3)  [#3989](https://github.com/skycoin/skywire/pull/3989)
-   feat(transport): active packet-pair bandwidth probe for idle transports (phase 2 of measured route ranking)  [#3988](https://github.com/skycoin/skywire/pull/3988)
-   feat(transport): passive per-transport throughput estimate (phase 1 of measured route ranking)  [#3987](https://github.com/skycoin/skywire/pull/3987)
-   fix(router): plan mux aux legs over the global TPD graph (route-finder first), not local-only transports  [#3986](https://github.com/skycoin/skywire/pull/3986)
-   feat(router): measure per-leg end-to-end latency; feed it to the policy + fastest-leg pick  [#3985](https://github.com/skycoin/skywire/pull/3985)
-   feat(router): retransmit SACK gaps on the fastest leg, not the mode-driven pick  [#3984](https://github.com/skycoin/skywire/pull/3984)
-   feat(router): transport-type-aware route ranking — deprioritize slow transports for route legs  [#3983](https://github.com/skycoin/skywire/pull/3983)
-   docs(routing-policy): reproducible measurement rig script + controlled far-end guide (gate-3)  [#3982](https://github.com/skycoin/skywire/pull/3982)
-   fix(routing-policy): make adaptive a fast default — lean start + inherit operator min-hops  [#3981](https://github.com/skycoin/skywire/pull/3981)
-   feat(routing-policy): per-leg mux telemetry harness — NDJSON + lifecycle events + chart (gate-2)  [#3980](https://github.com/skycoin/skywire/pull/3980)
-   feat(routing-policy): observe + emit warm-standby gate state; adaptive hot-swaps (gate-5 step 3)  [#3979](https://github.com/skycoin/skywire/pull/3979)
-   feat(routing-policy): add composite 'adaptive' default preset (size+membership+explore)  [#3978](https://github.com/skycoin/skywire/pull/3978)
-   feat(router): wire warm-standby demote/promote through on_tick ABI (gate-5 step 2)  [#3977](https://github.com/skycoin/skywire/pull/3977)
-   feat(cxo): gzip the all-transports CXO feed (bandwidth parity with HTTP)  [#3976](https://github.com/skycoin/skywire/pull/3976)
-   feat(deployment): gzip UT / SD / RF HTTP responses (Linode egress)  [#3975](https://github.com/skycoin/skywire/pull/3975)
-   feat(router): warm-standby mux leg state (dormant primitive, gate-5 step 1)  [#3974](https://github.com/skycoin/skywire/pull/3974)
-   docs: RFC for warm-standby mux legs + gated directions (gate-5 routing primitive)  [#3973](https://github.com/skycoin/skywire/pull/3973)
-   feat(routing-policy): add elastic-mux and probe-and-prune dynamic WASM presets  [#3971](https://github.com/skycoin/skywire/pull/3971)
-   test(router): age retx entries past retxMinAge in TestRetxBuffer_RetransmitList  [#3970](https://github.com/skycoin/skywire/pull/3970)
-   feat(routing-policy): stable per-leg transport ID + EWMA-smoothed latency-adaptive eviction  [#3969](https://github.com/skycoin/skywire/pull/3969)
-   fix(routing-policy): make latency-adaptive symmetric so its on_tick can act  [#3968](https://github.com/skycoin/skywire/pull/3968)
-   feat(routing-policy): add latency-adaptive dynamic WASM preset (evict-slowest, hysteresis-damped)  [#3967](https://github.com/skycoin/skywire/pull/3967)
-   feat(cli): app arg routing-policy — set an app's routing policy at runtime (no restart)  [#3966](https://github.com/skycoin/skywire/pull/3966)
-   feat(mux-bw): emit per-leg bandwidth + identity samples for policy measurement  [#3964](https://github.com/skycoin/skywire/pull/3964)
-   feat(routing-policy): combine WASM presets into one dispatched bundle.wasm (#3942)  [#3963](https://github.com/skycoin/skywire/pull/3963)
-   feat(routing-policy): embed compiled WASM presets, selectable by preset:<name> (#3942)  [#3962](https://github.com/skycoin/skywire/pull/3962)
-   fix(mux-bw): dial N disjoint routes via explicit hops, not N copies of one  [#3961](https://github.com/skycoin/skywire/pull/3961)
-   feat(meshproxy): serve the branded interstitial fast while a cold route warms  [#3960](https://github.com/skycoin/skywire/pull/3960)
-   docs: publish the code graph as a page on the site  [#3959](https://github.com/skycoin/skywire/pull/3959)
-   fix(router): full-duplex self-heal mux leg + local-calc-first GROW — make mux>1 carry  [#3958](https://github.com/skycoin/skywire/pull/3958)
-   feat(deployment): source the browse-origin domain from services-config.json  [#3957](https://github.com/skycoin/skywire/pull/3957)
-   feat(visor): branded interstitial from the mesh reverse-proxy (real-origin, no MITM)  [#3956](https://github.com/skycoin/skywire/pull/3956)
-   fix(router): lossless mux reorder + retx timeout — fix mux>1 stream corruption under load  [#3955](https://github.com/skycoin/skywire/pull/3955)
-   fix(router): disjoint mux legs + drain parked frames (mux>=2 = 0 bytes)  [#3954](https://github.com/skycoin/skywire/pull/3954)
-   build(deps): update Go dependencies to latest + regenerate wasm blobs  [#3953](https://github.com/skycoin/skywire/pull/3953)
-   feat(proxy): branded "building a route over skywire" interstitial for the native mesh proxies  [#3952](https://github.com/skycoin/skywire/pull/3952)
-   third_party: import the 0magnet modules instead of copying them  [#3951](https://github.com/skycoin/skywire/pull/3951)
-   wasm-visor: tour hardening — file browser, services-health, tp-viz crash, clearnet legibility  [#3950](https://github.com/skycoin/skywire/pull/3950)
-   fix(wallet): serve /wallet/ as an iframe-only surface; bounce top-level navigations to the HV UI  [#3949](https://github.com/skycoin/skywire/pull/3949)
-   fix(wasm-serve): serve the wallet cipher from the visor blob (fixes /wallet 'Go is not defined')  [#3948](https://github.com/skycoin/skywire/pull/3948)
-   fix(wasm-visor): pre-warm HV-UI caches at boot + longer TTLs + mount /api/client-log  [#3946](https://github.com/skycoin/skywire/pull/3946)
-   fix(wasm-visor + hv-ui): Network/Transports/visualizer/skychat tab loading on the browser visor  [#3945](https://github.com/skycoin/skywire/pull/3945)
-   fix(wasm-visor + hv-ui): Services-Health tab perpetual 'Checking…' spinner  [#3943](https://github.com/skycoin/skywire/pull/3943)
-   perf(transport/webrtc): empty MediaEngine — no media goroutines for data-channel-only PeerConnections  [#3940](https://github.com/skycoin/skywire/pull/3940)
-   fix(wasm-visor): proxy route-setup reliability + no-dmsg-relay wedge fix + verbose browse interstitial  [#3939](https://github.com/skycoin/skywire/pull/3939)
-   fix(logging): ringbuffer.go misspell (unblock CI lint)  [#3938](https://github.com/skycoin/skywire/pull/3938)
-   perf(logging): make RingBuffer a zero-alloc fixed circular buffer  [#3937](https://github.com/skycoin/skywire/pull/3937)
-   fix(wasm-visor): clearnet browsing reliability + speed (collision, recovery-wait, warm routes, fast-exit race)  [#3936](https://github.com/skycoin/skywire/pull/3936)
-   fix(wasm-visor): read Uint8Array/ArrayBuffer request bodies (POSTs were corrupted to "<object>")  [#3935](https://github.com/skycoin/skywire/pull/3935)
-   feat(cli): --jq / --shape output flags + --help --json, working on both binaries  [#3934](https://github.com/skycoin/skywire/pull/3934)
-   fix(transport): stale write-deadline causes ping/pong writes to fail on idle transports (half-open leak)  [#3933](https://github.com/skycoin/skywire/pull/3933)
-   feat(cxosub): live persistent CXO subscription instead of 5-min poll  [#3932](https://github.com/skycoin/skywire/pull/3932)
-   chore(wasm-visor): regenerate embedded blob from a clean tree (non-dirty version)  [#3931](https://github.com/skycoin/skywire/pull/3931)
-   make: refuse to build a committed wasm blob without provenance  [#3930](https://github.com/skycoin/skywire/pull/3930)
-   gofmt: format every remaining file so lint stops failing on all PRs  [#3929](https://github.com/skycoin/skywire/pull/3929)
-   cli: give hv probe an exit-code contract so it can gate CI  [#3926](https://github.com/skycoin/skywire/pull/3926)
-   visor, cli: test the browse-origin routing, and fix three CLI flag hazards  [#3925](https://github.com/skycoin/skywire/pull/3925)
-   fix(wasm-visor): stop 100% CPU peg — yamux Read/write spin on session shutdown  [#3924](https://github.com/skycoin/skywire/pull/3924)
-   integration: assert the VPN killswitch outcome, not one error string  [#3913](https://github.com/skycoin/skywire/pull/3913)
-   tpviz: drop the dead tpvizwasm view and its 11 MB stale blob  [#3912](https://github.com/skycoin/skywire/pull/3912)
-   chore(deps): bump CI actions (setup-java v5, gradle v6, setup-tinygo v3) + go mod x/mod  [#3911](https://github.com/skycoin/skywire/pull/3911)
-   fix(wasm-visor): mesh browser hangs when the visor is loaded via 127.0.0.1  [#3910](https://github.com/skycoin/skywire/pull/3910)
-   ui: restore the blank line before the mode badge's return  [#3909](https://github.com/skycoin/skywire/pull/3909)
-   fix(tpviz): resolve cosmos-go wasm assets against the bundle URL (Angular embed fix)  [#3908](https://github.com/skycoin/skywire/pull/3908)
-   feat(tpviz): default the network visualizer to the cosmos-go (WebGL2) view  [#3907](https://github.com/skycoin/skywire/pull/3907)
-   third_party: sync the xterm-go and cosmos-go ports with their upstream forks  [#3906](https://github.com/skycoin/skywire/pull/3906)
-   websh: handle the errors the shell was discarding  [#3905](https://github.com/skycoin/skywire/pull/3905)
-   third_party: sync vendored 0magnet/sh/v3 to fork @84701f5d  [#3904](https://github.com/skycoin/skywire/pull/3904)
-   feat(visor): mirror panic/fatal tracebacks to skywire-crash.log + dev-visor-loop.sh  [#3903](https://github.com/skycoin/skywire/pull/3903)
-   feat(tpviz): cosmos-go WebGL view as a role of the wasm-visor blob (drop tpviz-gl.wasm)  [#3902](https://github.com/skycoin/skywire/pull/3902)
-   feat(routing-policy): prefer-connected/balanced/hypervisor-priority presets + has_transport predicate + CLI discovery  [#3901](https://github.com/skycoin/skywire/pull/3901)
-   fix(autoconfig): restart a running hv-serve service after a binary update  [#3897](https://github.com/skycoin/skywire/pull/3897)
-   fix(wasm-visor): latch the 'connecting' toast so it can't linger after boot  [#3896](https://github.com/skycoin/skywire/pull/3896)
-   fix(wasm-visor): don't fall off the SharedWorker path on a slow boot (the 'already running in another tab' bug)  [#3895](https://github.com/skycoin/skywire/pull/3895)
-   feat(routing): asymmetric-fanout / low-latency-direct / privacy-max policy presets  [#3894](https://github.com/skycoin/skywire/pull/3894)
-   feat(wasm-visor): add the websh console to the tour  [#3893](https://github.com/skycoin/skywire/pull/3893)
-   refactor(visor): remove dormant standalone uptime-tracker client (keep TPD heartbeat)  [#3892](https://github.com/skycoin/skywire/pull/3892)
-   feat(wasm-visor): port config_refresh (dynamic setup-node refresh)  [#3891](https://github.com/skycoin/skywire/pull/3891)
-   fix(wasm-visor): restore VCS version stamp on embedded blobs (force -buildvcs=true)  [#3890](https://github.com/skycoin/skywire/pull/3890)
-   fix(dmsgweb,skynetweb): cache SOCKS upstream dialer + cooldown backoff on unready upstream  [#3889](https://github.com/skycoin/skywire/pull/3889)
-   fix(transport): dial-only networks log serve-unsupported at Debug, not ERROR (+guard Fatal)  [#3888](https://github.com/skycoin/skywire/pull/3888)
-   chore(wasm-visor): regenerate embedded blobs (self-recovery + logging fixes)  [#3887](https://github.com/skycoin/skywire/pull/3887)
-   feat(wasm-visor): dmsg self-recovery via ForceReconnect (signal-driven, no self-dial)  [#3886](https://github.com/skycoin/skywire/pull/3886)
-   refactor(visor): lazy on-demand node_health, remove dead LogRotationInterval RPC/HTTP, init cruft  [#3885](https://github.com/skycoin/skywire/pull/3885)
-   logging(transport): demote first-touch log-entry provisioning WARN→Debug  [#3884](https://github.com/skycoin/skywire/pull/3884)
-   logging: fix levels, stdout leaks, format deviations, and a Fatal-on-bad-pidfile availability bug  [#3883](https://github.com/skycoin/skywire/pull/3883)
-   chore(config): default log_store to memory; gitignore .angular cache  [#3882](https://github.com/skycoin/skywire/pull/3882)
-   integration: a non-zero exit is an error  [#3881](https://github.com/skycoin/skywire/pull/3881)
-   ci(android): fold APK + AAB into the main v* release workflow, retire android-release.yml  [#3880](https://github.com/skycoin/skywire/pull/3880)
-   ui: call it a wasm visor, say when the harness is on, and give it its own mark  [#3879](https://github.com/skycoin/skywire/pull/3879)
-   skycoin: serve the TinyGo visor as the wallet's cipher, not the std-Go one  [#3878](https://github.com/skycoin/skywire/pull/3878)
-   integration: wait for an app to reach running, don't sample once  [#3877](https://github.com/skycoin/skywire/pull/3877)
-   skycoin: assemble the command tree on skywire's side and serve the visor's cipher  [#3876](https://github.com/skycoin/skywire/pull/3876)
-   fix(wasm-visor): version from debug.BuildInfo, drop ldflags version scheme  [#3875](https://github.com/skycoin/skywire/pull/3875)
-   dmsg: stop the self-session test racing its own server for sequence 0  [#3874](https://github.com/skycoin/skywire/pull/3874)
-   cli: move 'web' under 'skywire cli'  [#3873](https://github.com/skycoin/skywire/pull/3873)
-   tree: structured JSON output, and widen the guard that missed it  [#3872](https://github.com/skycoin/skywire/pull/3872)
-   flags: restore the hidden 'tree' command  [#3871](https://github.com/skycoin/skywire/pull/3871)
-   netutil: make two //nolint directives parse  [#3870](https://github.com/skycoin/skywire/pull/3870)
-   cli: fix lint failures from the cliout merge  [#3869](https://github.com/skycoin/skywire/pull/3869)
-   cli: every command emits typed JSON, and a guard that keeps it that way  [#3868](https://github.com/skycoin/skywire/pull/3868)
-   chore(deps): refresh vendored deps; skycoin to develop HEAD (#3029)  [#3867](https://github.com/skycoin/skywire/pull/3867)
-   cli: structured output — --help --json, a printer that renders one value, and the first typed command  [#3866](https://github.com/skycoin/skywire/pull/3866)
-   Skywire Mobile: Fix wrong versioning on config  [#3865](https://github.com/skycoin/skywire/pull/3865)
-   docs(cli/proxy): update mux help/examples to `proxy mux <sub>` form  [#3864](https://github.com/skycoin/skywire/pull/3864)
-   ci: reach the shellcheck fallback when a download half-succeeds; drop two committed build artifacts  [#3863](https://github.com/skycoin/skywire/pull/3863)
-   wasm-visor: carry the skycoin browser cipher  [#3862](https://github.com/skycoin/skywire/pull/3862)
-   cli/proxy: show route in `proxy status` + nest mux ops under `proxy mux`  [#3861](https://github.com/skycoin/skywire/pull/3861)
-   cli: visor info reports WT registration, distinct peers, and no invented latency  [#3860](https://github.com/skycoin/skywire/pull/3860)
-   ui: turn on strictNullChecks  [#3859](https://github.com/skycoin/skywire/pull/3859)
-   ui: make the node components honest about null  [#3858](https://github.com/skycoin/skywire/pull/3858)
-   ui: make the vpn services honest about null  [#3857](https://github.com/skycoin/skywire/pull/3857)
-   wasm-visor: the mesh as the shell's network, and a shell that works under standard Go  [#3856](https://github.com/skycoin/skywire/pull/3856)
-   ui: make the services layer honest about null  [#3855](https://github.com/skycoin/skywire/pull/3855)
-   ui: drop baseUrl, and make the root tsconfig checkable  [#3854](https://github.com/skycoin/skywire/pull/3854)
-   Skywire Mobile: Multiple Language  [#3853](https://github.com/skycoin/skywire/pull/3853)
-   ui: upgrade to Angular 22 and ngx-translate 18  [#3852](https://github.com/skycoin/skywire/pull/3852)
-   cli: pin an explicit route for `ping` via --route  [#3851](https://github.com/skycoin/skywire/pull/3851)
-   transport: recover all transport types from a TPID (not just the legacy four)  [#3850](https://github.com/skycoin/skywire/pull/3850)
-   ui: build the manager UI with @angular/build  [#3849](https://github.com/skycoin/skywire/pull/3849)
-   ui: match skycoin's ESLint correctness rules  [#3848](https://github.com/skycoin/skywire/pull/3848)
-   router: reclaim retired mux legs immediately via CloseLegRetired (Phase 1a)  [#3847](https://github.com/skycoin/skywire/pull/3847)
-   ui: compile the manager UI under strict TypeScript and strictTemplates  [#3846](https://github.com/skycoin/skywire/pull/3846)
-   ui: explicit change detection before Angular 22, and two checks that catch what CI cannot  [#3845](https://github.com/skycoin/skywire/pull/3845)
-   feat(router): routing-table observability (RoutingStats RPC + route table-stats)  [#3844](https://github.com/skycoin/skywire/pull/3844)
-   build(deps): update all deps + skycoin/skycoin to latest develop  [#3842](https://github.com/skycoin/skywire/pull/3842)
-   fix(dmsg): carrier_converge_test compiles (noteCarrierFailure reason arg)  [#3841](https://github.com/skycoin/skywire/pull/3841)
-   build: Makefile cleanup — fix broken targets, prune dead entries  [#3840](https://github.com/skycoin/skywire/pull/3840)
-   dmsg-disc: reconcile server sessions against the live registry (self-heal address drift)  [#3839](https://github.com/skycoin/skywire/pull/3839)
-   visor: refuse to start a second visor on the same cli_addr  [#3836](https://github.com/skycoin/skywire/pull/3836)
-   dmsg: ordered carrier preference + convergence, per-carrier server health probe  [#3835](https://github.com/skycoin/skywire/pull/3835)
-   tpviz: a Go/wasm WebGL view alongside the JavaScript one  [#3834](https://github.com/skycoin/skywire/pull/3834)
-   Skywire Mobile: Fix Series 1 bugs and implement requested features (Telegram feedback)  [#3833](https://github.com/skycoin/skywire/pull/3833)
-   build: drop goimports-reviser from make format (unused; churned third_party)  [#3832](https://github.com/skycoin/skywire/pull/3832)
-   fix(dmsg): browser wss→WebTransport upgrade actually converges (re-resolve + WT-dial)  [#3830](https://github.com/skycoin/skywire/pull/3830)
-   feat(dmsg): registration-over-CXO always-on + HTTP keepalive stretch (follow-up to #3828)  [#3829](https://github.com/skycoin/skywire/pull/3829)
-   feat(dmsg): registration-over-CXO — visor entry as a CXO feed, dmsg-disc aggregates (opt-in)  [#3828](https://github.com/skycoin/skywire/pull/3828)
-   chore(deps): update dependencies + pin skycoin to ab113fbd  [#3827](https://github.com/skycoin/skywire/pull/3827)
-   feat(cxo): quiet-feed heartbeat republish to keep subscriber connections alive  [#3826](https://github.com/skycoin/skywire/pull/3826)
-   wasm-visor: a real shell in the visor tab, from one binary in two roles  [#3824](https://github.com/skycoin/skywire/pull/3824)
-   third_party: vendor the browser-terminal Go ports (xterm-go, websh, sh, afero)  [#3823](https://github.com/skycoin/skywire/pull/3823)
-   feat(cli): add 'tp public [true|false]' to get/set is_public  [#3821](https://github.com/skycoin/skywire/pull/3821)
-   feat(visor): relay tries confirmed + blind candidates together, with per-candidate observability  [#3820](https://github.com/skycoin/skywire/pull/3820)
-   feat(visor): relay discovery falls back to blind-trying direct peers when the transport graph is empty  [#3819](https://github.com/skycoin/skywire/pull/3819)
-   feat(visor): dmsg over skynet — reach .dmsg peers via the VStreamMux relay (no route)  [#3818](https://github.com/skycoin/skywire/pull/3818)
-   feat(wasm-visor): distinct browser-tab favicon (violet mesh-cloud)  [#3817](https://github.com/skycoin/skywire/pull/3817)
-   feat(tools): browser-inspection dev tooling — cdpeval, wfdrive, hvinspect hard-reload  [#3816](https://github.com/skycoin/skywire/pull/3816)
-   feat(visor): .skynet relay tier — direct → 1-hop relay → route in DialSkynet  [#3815](https://github.com/skycoin/skywire/pull/3815)
-   feat(transport): visor-as-relay over skynet (signed PK-addressed VStream forwarding)  [#3814](https://github.com/skycoin/skywire/pull/3814)
-   docs(dmsg): CXO relay presence finding + signed-heartbeat uptime (+ characterization test)  [#3813](https://github.com/skycoin/skywire/pull/3813)
-   feat(dmsg): always-on server↔server relay (remove peer toggles, add caps)  [#3812](https://github.com/skycoin/skywire/pull/3812)
-   docs(design): RFC — dmsg as a bootstrap-only floor (visor-relayed dmsg)  [#3811](https://github.com/skycoin/skywire/pull/3811)
-   feat(dmsg): idle-session reaper — settle session count toward MinSessions  [#3810](https://github.com/skycoin/skywire/pull/3810)
-   feat(wasm-visor): clearnet-over-skysocks real-origin + content-addressed browse origins  [#3809](https://github.com/skycoin/skywire/pull/3809)
-   feat(wasm-visor): configurable browse-origin suffix + two-port hosted mode  [#3808](https://github.com/skycoin/skywire/pull/3808)
-   feat: real-origin mesh browser — isolated per-site origins on native HV UI + wasm visor  [#3807](https://github.com/skycoin/skywire/pull/3807)
-   Mobile UI Boundary document  [#3805](https://github.com/skycoin/skywire/pull/3805)
-   fix(dmsgweb): move exit-IP check from visor landing page to home.dmsg  [#3804](https://github.com/skycoin/skywire/pull/3804)
-   feat(visor): serve the standalone wasm-visor / testing harness from the visor process  [#3803](https://github.com/skycoin/skywire/pull/3803)
-   chore(deps): batch open Dependabot bumps (CI actions + npm lockfile)  [#3802](https://github.com/skycoin/skywire/pull/3802)
-   fix(lint): clear golangci-lint debt on develop (errcheck/misspell/unparam)  [#3801](https://github.com/skycoin/skywire/pull/3801)
-   fix(sd): periodically republish the services CXO feed so a fresh Root is always servable  [#3800](https://github.com/skycoin/skywire/pull/3800)
-   fix(cxo): serve cold CXO snapshots + fix subscribe port collision (blank network-uptime page)  [#3799](https://github.com/skycoin/skywire/pull/3799)
-   fix(cxo): TPD aggregator feed leak + dmsg-disc encode-cache + shared conn reaper  [#3798](https://github.com/skycoin/skywire/pull/3798)
-   Skywire Mobile (Android)  [#3797](https://github.com/skycoin/skywire/pull/3797)
-   feat(hv-ui): in-place app GUIs — VPN tab, unified full-page app host, Chat-tab fix  [#3794](https://github.com/skycoin/skywire/pull/3794)
-   feat(wasm-visor): clearnet iframe browser — reliability + browser parity  [#3788](https://github.com/skycoin/skywire/pull/3788)
-   fix(wasm-visor): bound Services-Health probe so one dead service can't hang the tab  [#3787](https://github.com/skycoin/skywire/pull/3787)
-   fix(tour): spotlight the live dmsg/transport counts, not the column headers  [#3786](https://github.com/skycoin/skywire/pull/3786)
-   docs(tour): correct the services step + name .dmsg aliases in the browse demo  [#3780](https://github.com/skycoin/skywire/pull/3780)
-   feat(wasmhv): populate the Services-Health tab on a browser wasm visor  [#3779](https://github.com/skycoin/skywire/pull/3779)
-   test(visor): guard against wasm-HV mirror DTO drift from pkg/visor  [#3778](https://github.com/skycoin/skywire/pull/3778)
-   fix(dmsg-server): transit client reaches its own server via loopback  [#3776](https://github.com/skycoin/skywire/pull/3776)
-   feat(dmsg): advertise the server build version in its discovery entry  [#3775](https://github.com/skycoin/skywire/pull/3775)
-   feat(cli): mdisc servers — inferred connected-client count, sorted by load  [#3774](https://github.com/skycoin/skywire/pull/3774)
-   fix(cli): sd/proxy/vpn fetch uptimes/days/1, not days/30  [#3773](https://github.com/skycoin/skywire/pull/3773)
-   fix(cli): pv fetches uptimes/days/1, not days/30 (online filter only)  [#3772](https://github.com/skycoin/skywire/pull/3772)
-   fix(wasm-visor): tighter autoconnect dial timeouts free slots ~3x faster  [#3770](https://github.com/skycoin/skywire/pull/3770)
-   fix(transport): rank direct WT/WS above NAT-traversing WEBRTC in preference  [#3769](https://github.com/skycoin/skywire/pull/3769)
-   feat(cli): pv -y — break public-visor transport counts down by type  [#3768](https://github.com/skycoin/skywire/pull/3768)
-   feat(wasm-visor): uptime-heartbeat parity with the native visor  [#3767](https://github.com/skycoin/skywire/pull/3767)
-   fix(cli): pv — drop redundant --uturl/--cdu, uptime derives from --tpdurl  [#3766](https://github.com/skycoin/skywire/pull/3766)
-   feat(visorcore): shared DmsgServicePKs for native + wasm  [#3765](https://github.com/skycoin/skywire/pull/3765)
-   feat(visorcore): shared BuildTransportManager for native + wasm  [#3764](https://github.com/skycoin/skywire/pull/3764)
-   fix(cli): deprecate standalone uptime tracker, route CLI uptime via TPD-integrated /uptimes?v=v3  [#3763](https://github.com/skycoin/skywire/pull/3763)
-   fix(cxo): pin-gate benign sendLastRoot "no such head" WARN  [#3762](https://github.com/skycoin/skywire/pull/3762)
-   feat(transport): native route-setup hook + tp add use the full auto-transport preference order  [#3761](https://github.com/skycoin/skywire/pull/3761)
-   fix(wasm-visor): route-setup hook creates a p2p direct transport (webrtc), not dmsg  [#3760](https://github.com/skycoin/skywire/pull/3760)
-   fix(wasm-visor): skysocks-lite dials exits direct (dmsg 1-hop), not via route-finder  [#3759](https://github.com/skycoin/skywire/pull/3759)
-   feat(wasm-tour): app-window demos + progressive hypervisor-cluster demo  [#3758](https://github.com/skycoin/skywire/pull/3758)
-   feat(wasm-tour): simulate a managed cluster for the "Your cluster, live" step  [#3757](https://github.com/skycoin/skywire/pull/3757)
-   fix(hv-ui): dmsg-server carrier column was dead in the node list (tree path)  [#3756](https://github.com/skycoin/skywire/pull/3756)
-   feat(wasm-tour): front-page-first tour reflow + node-list label/ip cell hooks  [#3755](https://github.com/skycoin/skywire/pull/3755)
-   feat(hv-ui): dmsg-server carrier column, wasm tab hiding, services-health graceful degrade  [#3754](https://github.com/skycoin/skywire/pull/3754)
-   feat(wasm-visor): tour visits each overview tab (not just points at it)  [#3753](https://github.com/skycoin/skywire/pull/3753)
-   feat(wasm-visor): navigation-driven guided tour  [#3752](https://github.com/skycoin/skywire/pull/3752)
-   fix(visor): svc health — DMSG Server version via discovery-routed probe  [#3751](https://github.com/skycoin/skywire/pull/3751)
-   fix(hv-ui): de-dup log timestamp/level; add wasm fresh-worker reload  [#3750](https://github.com/skycoin/skywire/pull/3750)
-   feat(wasmhv): ctl-bridge dials /ctl/rpc so the CLI can drive a wasm-visor tab  [#3749](https://github.com/skycoin/skywire/pull/3749)
-   fix(router): skip route finder when visor has no transports  [#3748](https://github.com/skycoin/skywire/pull/3748)
-   fix(dmsg): WT never advertised — single ALPN-demux listener (fixes #3739 serve path)  [#3747](https://github.com/skycoin/skywire/pull/3747)
-   fix(dmsg): browser wss churn — only converge wss→WT for WT-capable servers  [#3746](https://github.com/skycoin/skywire/pull/3746)
-   feat(wasm-visor): resilient skysocks-lite — sticky reconnect + liveness  [#3745](https://github.com/skycoin/skywire/pull/3745)
-   fix(tpviz): remove window/document listeners on unmount  [#3744](https://github.com/skycoin/skywire/pull/3744)
-   perf(tpviz): suspend data sync + rendering when the visualizer isn't visible  [#3743](https://github.com/skycoin/skywire/pull/3743)
-   fix(tpviz): stop all loops + WebSocket on unmount (network-viz tab leak)  [#3742](https://github.com/skycoin/skywire/pull/3742)
-   chore(deps): batch CI action + postcss Dependabot bumps  [#3741](https://github.com/skycoin/skywire/pull/3741)
-   fix(ci): clear lint + scorecard regressions on develop  [#3740](https://github.com/skycoin/skywire/pull/3740)
-   feat(dmsg-server): WebTransport default-on (shared UDP socket with dmsg-over-QUIC)  [#3739](https://github.com/skycoin/skywire/pull/3739)
-   feat: wasm↔native visor config-view parity — runtime-reconfigurable, SK-redacted, editor-wired  [#3738](https://github.com/skycoin/skywire/pull/3738)
-   chore(security): OpenSSF Scorecard hardening (SECURITY.md, workflow permissions, CodeQL, Scorecard, Dependabot)  [#3732](https://github.com/skycoin/skywire/pull/3732)
-   fix(ci): clear misspell lint regression + regenerate Test badge on develop  [#3730](https://github.com/skycoin/skywire/pull/3730)
-   chore(readme): remove retired Go Report Card badge  [#3729](https://github.com/skycoin/skywire/pull/3729)
-   docs(guides): public-visor setup + reachability troubleshooting  [#3728](https://github.com/skycoin/skywire/pull/3728)
-   fix(address-resolver): /resolve of an unresolvable type returns 404, not 500  [#3727](https://github.com/skycoin/skywire/pull/3727)
-   feat(wasm-visor): mirror the full native config shape in the runtime-config view  [#3726](https://github.com/skycoin/skywire/pull/3726)
-   fix(httpauth): authenticate dmsg requests by the noise-verified RemoteAddr PK (security + load)  [#3725](https://github.com/skycoin/skywire/pull/3725)
-   feat(wasm-visor/harness): forward WARN+ subsystem logs to the /ctl/log shell bridge  [#3724](https://github.com/skycoin/skywire/pull/3724)
-   fix(skynet-browser): search forms, links, CSS images + legibility on proxied clearnet pages  [#3723](https://github.com/skycoin/skywire/pull/3723)
-   docs(gui-standardization): steps 3+5 landed; skycoin-web stays iframed  [#3721](https://github.com/skycoin/skywire/pull/3721)
-   feat(ui): mount skychat/logs in WinBox via a CDK-portal bridge (no self-iframe)  [#3720](https://github.com/skycoin/skywire/pull/3720)
-   docs(gui-standardization): record steps 1/2/4 landed; defer the WinBox CDK-portal step  [#3719](https://github.com/skycoin/skywire/pull/3719)
-   test(wasm-headless): fail fast with a diagnostic instead of hanging; retry once  [#3718](https://github.com/skycoin/skywire/pull/3718)
-   feat(ui): lazy-load the VPN feature as its own module  [#3717](https://github.com/skycoin/skywire/pull/3717)
-   feat(ui): generic <app-bundle-mount> host component  [#3716](https://github.com/skycoin/skywire/pull/3716)
-   refactor(ui): extract SharedModule from the monolithic AppModule  [#3715](https://github.com/skycoin/skywire/pull/3715)
-   docs(gui-standardization): inventory findings + SharedModule prerequisite + corrected plan  [#3714](https://github.com/skycoin/skywire/pull/3714)
-   fix(tpviz): WebGL grouping — contain nodes, size circles to fit, weight to centre  [#3713](https://github.com/skycoin/skywire/pull/3713)
-   feat(hv-serve): serve the tpviz visualizer bundle + /api/* for the wasm visor  [#3712](https://github.com/skycoin/skywire/pull/3712)
-   fix(hv-ui): network-visualizer degrades gracefully when tpviz bundle isn't served  [#3711](https://github.com/skycoin/skywire/pull/3711)
-   chore(deps): bump ip-address, socket.io-parser, fast-uri (npm lockfile)  [#3710](https://github.com/skycoin/skywire/pull/3710)
-   feat(hv-ui): mount shared tpviz visualizer + WebGL parity, SD-over-dmsg, tighter grouping, console-spam fix  [#3709](https://github.com/skycoin/skywire/pull/3709)
-   fix(router): use a freshly-created direct transport without waiting out the route-finder  [#3708](https://github.com/skycoin/skywire/pull/3708)
-   docs(rfc): GUI embedding standardization — mount vs iframe  [#3706](https://github.com/skycoin/skywire/pull/3706)
-   Improve SkyChat UI/UX  [#3705](https://github.com/skycoin/skywire/pull/3705)
-   feat(skychat): auto network mode — dmsg-first, background skynet upgrade  [#3702](https://github.com/skycoin/skywire/pull/3702)
-   feat(hv-ui): network visualizer — color nodes by country + full transport tooltip  [#3701](https://github.com/skycoin/skywire/pull/3701)
-   feat(tpviz): per-type stats for all transports; fold Uptime Tracker into TPD  [#3700](https://github.com/skycoin/skywire/pull/3700)
-   Skychat fixing: header ⋮ menus + avatar, port and forward fixes  [#3699](https://github.com/skycoin/skywire/pull/3699)
-   fix(tpviz): WebGL view keeps all visors (Flat parity) + dots stay inside bubbles  [#3698](https://github.com/skycoin/skywire/pull/3698)
-   Skychat Improvement: profiles, address book  [#3697](https://github.com/skycoin/skywire/pull/3697)
-   feat(netview): count all transport types (squicr/webrtc/swsr/swtr), not just 4  [#3696](https://github.com/skycoin/skywire/pull/3696)
-   fix(hv-ui): skychat defaults to skynet in wasm; embed=1 chrome-less only when iframed  [#3695](https://github.com/skycoin/skywire/pull/3695)
-   refactor(rewards): collector fetches UT through the proxy uniformly in --proxy mode  [#3694](https://github.com/skycoin/skywire/pull/3694)
-   fix(rewards): collector defaults UT to TPD-integrated /uptimes (standalone UT decommissioned)  [#3693](https://github.com/skycoin/skywire/pull/3693)
-   fix(rewards): collector --proxy fetches UT via RPC→DMSG, not the survey proxy  [#3692](https://github.com/skycoin/skywire/pull/3692)
-   docs(rewards): deploy recipe uses --minv auto (dynamic reward floor)  [#3691](https://github.com/skycoin/skywire/pull/3691)
-   feat(rewards): --minv auto computes the 14-day reward floor (retires the bash floor)  [#3690](https://github.com/skycoin/skywire/pull/3690)
-   fix(pty): only gate RPC-exec on the SELF target, not remote hypervisor control  [#3689](https://github.com/skycoin/skywire/pull/3689)
-   docs(rewards): reward-system operations & deployment guide  [#3688](https://github.com/skycoin/skywire/pull/3688)
-   feat(rewards): rewards run can collect via the dmsgweb SOCKS5 proxy  [#3687](https://github.com/skycoin/skywire/pull/3687)
-   feat(rewards): collector stages log_collecting → log_backups in Go (retire the rsync)  [#3686](https://github.com/skycoin/skywire/pull/3686)
-   feat(rewards): survey collector can fetch via a dmsgweb SOCKS5 resolving proxy  [#3685](https://github.com/skycoin/skywire/pull/3685)
-   chore(deps): bump @angular/common, compiler, core to 21.2.19  [#3681](https://github.com/skycoin/skywire/pull/3681)
-   feat(hvui): three-state reward column (eligible / rejected / no address)  [#3680](https://github.com/skycoin/skywire/pull/3680)
-   feat(visor): push node-info survey to the reward system over dmsg  [#3679](https://github.com/skycoin/skywire/pull/3679)
-   fix(rewards): fetch TPD /metrics over dmsg, not the default HTTP client  [#3678](https://github.com/skycoin/skywire/pull/3678)
-   feat(rewards): accept visor survey PUSH over dmsg (POST /node-info)  [#3677](https://github.com/skycoin/skywire/pull/3677)
-   fix(rewards): harden the Go survey collector to match the live fetch_surveys.sh  [#3676](https://github.com/skycoin/skywire/pull/3676)
-   fix(rewards): record "survey not found" as ineligible instead of a silent drop  [#3675](https://github.com/skycoin/skywire/pull/3675)
-   feat(skynet): pool + yamux-reuse explicit source-route dials  [#3672](https://github.com/skycoin/skywire/pull/3672)
-   Improve Skychat on UI, Groups and Channel feature  [#3670](https://github.com/skycoin/skywire/pull/3670)
-   feat(systray): revised menu + decoupled tray (--systray-only) controlling the visor over RPC  [#3669](https://github.com/skycoin/skywire/pull/3669)
-   Fix delete message for both side issue  [#3668](https://github.com/skycoin/skywire/pull/3668)
-   fix(reward-uptime): score visor uptime against the 5-min heartbeat cadence (fixes the ~30% regression)  [#3667](https://github.com/skycoin/skywire/pull/3667)
-   Notification Hub  [#3666](https://github.com/skycoin/skywire/pull/3666)
-   refactor(cli): centralize routing-session application in one shared helper  [#3665](https://github.com/skycoin/skywire/pull/3665)
-   feat(cli): lift generic routing-session flags onto `visor app start`  [#3664](https://github.com/skycoin/skywire/pull/3664)
-   feat(landingpage): exit-IP check links on the visor landing page  [#3663](https://github.com/skycoin/skywire/pull/3663)
-   feat(cli): make `visor app` the app-control superset — add pk/restart/conns  [#3662](https://github.com/skycoin/skywire/pull/3662)
-   feat(wasm-visor): model skysocks-client-lite as a configurable app (P1)  [#3661](https://github.com/skycoin/skywire/pull/3661)
-   feat(buildinfo): fall back to debug.BuildInfo settings for commit/date/tags  [#3660](https://github.com/skycoin/skywire/pull/3660)
-   feat(visor): Suspend/Resume RPC — quiesce the visor without stopping the process  [#3659](https://github.com/skycoin/skywire/pull/3659)
-   fix(security): gate RPC-initiated dmsgpty exec behind opt-in config  [#3658](https://github.com/skycoin/skywire/pull/3658)
-   feat(config): autoconfig + config-gen flags for privacy/routing knobs  [#3657](https://github.com/skycoin/skywire/pull/3657)
-   docs(guides): add privacy ↔ performance tuning guide  [#3656](https://github.com/skycoin/skywire/pull/3656)
-   feat(autoconfig): add --transport-port (shared master transport port)  [#3655](https://github.com/skycoin/skywire/pull/3655)
-   fix(wasm): route in-page .dmsg/.skynet links over the mesh, not the browser  [#3654](https://github.com/skycoin/skywire/pull/3654)
-   perf(router): race transport creation against routing over existing transports  [#3653](https://github.com/skycoin/skywire/pull/3653)
-   fix(wasm): dial skysocks over a single route, not a 2-route mux  [#3652](https://github.com/skycoin/skywire/pull/3652)
-   feat(apps): shared skynet→dmsg dial-fallback primitive; opt-in for proxy + vpn clients  [#3651](https://github.com/skycoin/skywire/pull/3651)
-   feat(transport): per-type transport-creation policy (disable direct p2p, keep dmsg)  [#3650](https://github.com/skycoin/skywire/pull/3650)
-   perf(autoconnect): dial public visors concurrently (native + wasm)  [#3649](https://github.com/skycoin/skywire/pull/3649)
-   perf(visor): first public-autoconnect pass ~3s after boot, not after 5 minutes  [#3648](https://github.com/skycoin/skywire/pull/3648)
-   perf(wasm): faster first transports — event-driven autoconnect start + fast-retry to target  [#3647](https://github.com/skycoin/skywire/pull/3647)
-   fix(wasmhv): duplicate Go/TinyGo variant selector inside the embedded chat iframe  [#3646](https://github.com/skycoin/skywire/pull/3646)
-   fix(transport): WalkTransports callback ran under tm.mx (silent wedge) + lock-stall watchdog  [#3645](https://github.com/skycoin/skywire/pull/3645)
-   feat(skychat): group admission control + encryption hardening (continues #3631) + "ask again"  [#3644](https://github.com/skycoin/skywire/pull/3644)
-   feat(wasm/skychat): desktop notifications for the browser visor + skynet-default sends + async-PK windows  [#3643](https://github.com/skycoin/skywire/pull/3643)
-   feat(wasmhv): the desktop's visor-log window hosts the SAME Angular Logs tab  [#3642](https://github.com/skycoin/skywire/pull/3642)
-   feat(ui,wasmhv): skychat gets its own Chat tab; the wasm desktop's chat window hosts the SAME Angular component  [#3641](https://github.com/skycoin/skywire/pull/3641)
-   fix: gofmt two skychat CLI files (#3595 rebase fallout — unbreaks the lint lane)  [#3640](https://github.com/skycoin/skywire/pull/3640)
-   fix(wasm): populate SelfSummary mirror fields, flag host-stats N/A, serve empty app-logs  [#3639](https://github.com/skycoin/skywire/pull/3639)
-   test(wasm): Tier B headless smoke — run the REAL wasm-visor blob under Node (no browser)  [#3638](https://github.com/skycoin/skywire/pull/3638)
-   test(dmsg,wasmhv): Tier A headless browser-edge regression gate + `mdisc check` wss health probe  [#3637](https://github.com/skycoin/skywire/pull/3637)
-   chore(deps): batch the open dependabot npm bumps for the manager UI  [#3636](https://github.com/skycoin/skywire/pull/3636)
-   feat(wallet): serve skycoin-web straight from the vendored module (drop the copied tree) + vendor skycoin@1bb47440  [#3635](https://github.com/skycoin/skywire/pull/3635)
-   fix(dmsg): browser client never falls back to TCP — robust on-demand rendezvous  [#3634](https://github.com/skycoin/skywire/pull/3634)
-   fix(wasm): mirror the subsystem log firehose into the node Logs page (/runtime-logs)  [#3633](https://github.com/skycoin/skywire/pull/3633)
-   fix(wasm/dmsg): robust on-demand rendezvous — browser never falls back to TCP (+ live server discovery)  [#3632](https://github.com/skycoin/skywire/pull/3632)
-   [WIP] Improve group feature of Skychat, both on features and infrastructures   [#3631](https://github.com/skycoin/skywire/pull/3631)
-   feat(visor/ui): show the dmsg-server connection type (carrier) in the node info card  [#3630](https://github.com/skycoin/skywire/pull/3630)
-   fix(wasmhv): size the tour window to its content (no dead space / no clipped "more" panel)  [#3629](https://github.com/skycoin/skywire/pull/3629)
-   feat(wasmhv): tour in a non-blocking WinBox window + accurate dmsg-carrier⇄transport story  [#3628](https://github.com/skycoin/skywire/pull/3628)
-   fix(wasmhv): wallet config overlay collapses to a thin strip (skysocks-exit controls unreachable)  [#3627](https://github.com/skycoin/skywire/pull/3627)
-   comprehensive unit + integration coverage; fix 4 latent bugs  [#3625](https://github.com/skycoin/skywire/pull/3625)

## 1.3.91

30 PRs on top of v1.3.90. Headlines: **the reward-uptime regression is fixed** (v1.3.89 collapsed heartbeat DELIVERY to ~30% for many visors — this restores it), **the browser TinyGo wasm-visor boots again** (its in-process apps no longer panic on TinyGo's missing reflect), the **skychat DM stack** gains delete-for-everyone + delivery ticks on one shared controller, and **Bitcoin config moves into the wallet** where it belongs.

-   **Reward uptime — delivery regression fixed (#3619).** v1.3.89's heartbeat loop ran the send inline, so a slow/blocked send (TPD auth contending with transport re-registration under load) made `time.Ticker` drop ticks — collapsing recorded uptime to ~30% for heartbeat-dependent visors despite being online. Now each round runs in its own bounded goroutine (never stalls the ticker) + fires immediately on start; the TPD store also backfills missed 5-min slots (bounded 30 min) so flaky delivery still records a continuously-up visor near 100%. **A current-fleet visor records uptime correctly again once it updates to this release.**
-   **TinyGo wasm-visor un-wedged (#3620).** `appserver.Proc.AwaitConn` still registered the app RPC gateway via reflection (`RegisterName`), which TinyGo's runtime reflect can't do — so the browser wasm-visor panicked the instant it launched an in-process app (skychat), wedging it inside the HV SharedWorker. The gateway's ~18 methods are now registered as reflection-free gobrpc `HandleFunc`s on the TinyGo build (mirroring the edge RPC). Verified live: the tinygo worker now boots + runs skychat.
-   **Skychat DM convergence + features (#3608–#3618).** Native app and browser wasm-visor now share ONE `pkg/skychat/dm` controller. New: delete-for-everyone (tombstones) and WhatsApp-style delivery-status ticks (sent → received → read), both native SPA + HV Angular + wasm.
-   **Bitcoin electrum config in the wallet (#3621).** BTC's electrum server is now the Bitcoin coin's node URL in skycoin-web's own Settings → Nodes (like every other coin); the visor side keeps only the skysocks exit.

-   fix(skyroute): deterministic ErrNoMux via yamux Ping liveness probe (flaky test under -race)  [#3623](https://github.com/skycoin/skywire/pull/3623)
-   feat(wallet): BTC electrum server lives in the wallet's Settings → Nodes (only skysocks stays visor-side)  [#3621](https://github.com/skycoin/skywire/pull/3621)
-   fix(appserver): reflection-free gob RPC on js (unwedge the TinyGo wasm-visor)  [#3620](https://github.com/skycoin/skywire/pull/3620)
-   fix(reward-uptime): heartbeat delivery + bounded backfill (v1.3.89 ~30% uptime regression)  [#3619](https://github.com/skycoin/skywire/pull/3619)
-   feat(skychat): delivery-status ticks on the wasm-visor path (HV Angular UI)  [#3618](https://github.com/skycoin/skywire/pull/3618)
-   feat(skychat): delivery-status ticks in the HV Angular UI (native path)  [#3617](https://github.com/skycoin/skywire/pull/3617)
-   fix(skychat): eslint errors in the Angular skychat component (unblock ui CI)  [#3616](https://github.com/skycoin/skywire/pull/3616)
-   feat(skychat): handle dm-status delete in the HV Angular UI native path  [#3615](https://github.com/skycoin/skywire/pull/3615)
-   feat(skychat): DM delete-for-everyone UI (native SPA + HV Angular)  [#3614](https://github.com/skycoin/skywire/pull/3614)
-   feat(skychat): delete-for-everyone in the native app + wasm visor  [#3613](https://github.com/skycoin/skywire/pull/3613)
-   feat(skychat/dm): delete-for-everyone wire type + controller support  [#3612](https://github.com/skycoin/skywire/pull/3612)
-   feat(skychat): migrate the native app onto the shared pkg/skychat/dm controller  [#3611](https://github.com/skycoin/skywire/pull/3611)
-   feat(skychat/dm): Serve (inject conns), Stats counters, nil-transport safety  [#3610](https://github.com/skycoin/skywire/pull/3610)
-   feat(wasm-visor): run 1:1 skychat on the shared pkg/skychat/dm controller  [#3609](https://github.com/skycoin/skywire/pull/3609)
-   feat(skychat): shared DM controller in pkg/skychat/dm  [#3608](https://github.com/skycoin/skywire/pull/3608)
-   feat(skychat): quoted-reply threading in the HV Angular UI  [#3607](https://github.com/skycoin/skywire/pull/3607)
-   feat(skychat): quoted-reply threading in the native SPA  [#3606](https://github.com/skycoin/skywire/pull/3606)
-   docs(skydex): RFC — SkyDEX as a website over dmsg  [#3605](https://github.com/skycoin/skywire/pull/3605)
-   feat(skychat): persist + surface message id and reply_to on native read paths  [#3604](https://github.com/skycoin/skywire/pull/3604)
-   fix(config): regen preserves custom apps + operator toggles, adds new defaults  [#3603](https://github.com/skycoin/skywire/pull/3603)
-   feat(wasm-visor): backfill group chat history via Manager.ReplayHistory  [#3602](https://github.com/skycoin/skywire/pull/3602)
-   feat(wasm-visor): back the skychat buffer with pkg/skychat/history MemStore  [#3601](https://github.com/skycoin/skywire/pull/3601)
-   docs(packaging): document the three auto-update mechanisms + AUR packages  [#3600](https://github.com/skycoin/skywire/pull/3600)
-   refactor(skychat): move history store to pkg/skychat/history + wasm-safe MemStore  [#3599](https://github.com/skycoin/skywire/pull/3599)
-   feat(skychat): native send-side quoted replies (reply_to on /message)  [#3598](https://github.com/skycoin/skywire/pull/3598)
-   feat(skychat): wire-propagate quoted replies via the shared codec + DM message ids  [#3597](https://github.com/skycoin/skywire/pull/3597)
-   feat(wasm-visor): runtime Go<->TinyGo variant switch in one PWA  [#3594](https://github.com/skycoin/skywire/pull/3594)
-   Improve Skychat  [#3592](https://github.com/skycoin/skywire/pull/3592)

## 1.3.90

14 PRs on top of v1.3.89. A small release, mostly the transport-visualisation overlay and geo handling.

-   **tpviz.** Full-fleet country grouping from survey GeoIP, satellites for country-less visors on an exact flat-Fibonacci distribution, centring on the core, and an end to the perpetual WebGL drift.
-   **Geo, registered live.** A visor's geo is now registered with service discovery synchronously rather than being lost to the async-lookup race; the wasm-visor learns its own country from a dmsg server via `LookupIPGeo`.
-   **Mesh gateway.** `vpn-router --mesh-gateway-only` gives transparent `.dmsg`/`.skynet` to a LAN sitting behind an existing gateway, and `resolver up --bind <host>` serves the LAN with the choice persisted.

-   feat(tpviz): render country-less satellites as 🛰️ on the overlay  [#3591](https://github.com/skycoin/skywire/pull/3591)
-   chore(wasmhv): rebuild embedded Go wasm-visor blob (geo + node-list fixes)  [#3590](https://github.com/skycoin/skywire/pull/3590)
-   feat(wasmhv): real transport counts + per-hypervisor node sections + flap resistance  [#3589](https://github.com/skycoin/skywire/pull/3589)
-   feat(wasm-visor): learn own country from a dmsg server (LookupIPGeo)  [#3588](https://github.com/skycoin/skywire/pull/3588)
-   fix(servicedisc): register visor geo LIVE so it isn't lost to the async-lookup race  [#3587](https://github.com/skycoin/skywire/pull/3587)
-   feat(tpviz): full-fleet country grouping from survey GeoIP  [#3586](https://github.com/skycoin/skywire/pull/3586)
-   feat(tpviz): satellites for country-less visors + exact Flat Fibonacci distribution  [#3585](https://github.com/skycoin/skywire/pull/3585)
-   feat(tpviz): center on the core + Flat-parity country group boundaries  [#3584](https://github.com/skycoin/skywire/pull/3584)
-   fix(tpviz): keep the WebGL graph framed — stop the perpetual drift  [#3583](https://github.com/skycoin/skywire/pull/3583)
-   docs(guide): lead the LAN gateway with the transparent mesh gateway  [#3582](https://github.com/skycoin/skywire/pull/3582)
-   fix(cxo): lower MaxFillingTime default 10m → 2m  [#3581](https://github.com/skycoin/skywire/pull/3581)
-   feat(vpn-router): `--mesh-gateway-only` — transparent .dmsg/.skynet for a LAN behind an existing router  [#3580](https://github.com/skycoin/skywire/pull/3580)
-   feat(resolver): `resolver up --bind <host>` to serve the LAN (persisted)  [#3579](https://github.com/skycoin/skywire/pull/3579)

## 1.3.89

15 PRs on top of v1.3.88. Headline: **reward uptime is fixed** — the fleet's presence heartbeat, silently broken since the standalone uptime tracker was decommissioned, works again; the TPD memory leaks behind the reward outage are closed; and a board can now share its `.dmsg` / `.skynet` resolver with a whole home-router LAN. **Because the heartbeat fix is visor-side and the reward minimum-version floor auto-increments, a current-fleet visor only earns rewards correctly once it updates to this release** — which both clears the version floor and carries the working heartbeat.

-   **Reward uptime restored — the reason to update (#3563, #3570, #3564, #3567).** When the standalone uptime tracker was decommissioned fleet-wide, `initUptimeTracker` returned early on the now-empty `uptime_tracker` config and silently disabled the TPD `/v4/update` heartbeat that had become the reward-critical presence signal — so visors' recorded uptime collapsed to near-zero despite being online, invisibly, because it logged at Debug. #3563 decouples the TPD heartbeat from the dead tracker (it runs whenever a transport-discovery address is set) and raises the failure to Warn + a health flag. #3570 fixes the deeper cause for high-transport visors: a visor holds several httpauth clients to the same TPD (uptime heartbeat + transport registration), each with its own nonce counter, so concurrent same-PK requests raced the server's single monotonic nonce and 401'd until a resync that kept losing under load — now one shared nonce per (server, identity) serializes them. #3564 makes the TPD store log reward-heartbeat write failures instead of discarding them (a Redis outage was previously invisible at every log level), and #3567 adds a CI regression test for the decouple.
-   **TPD memory leaks fixed (#3561, #3562, #3571).** The transport-discovery's CXO aggregator subscribed to every visor feed but never pruned the Roots it received (~1 GB/day of unbounded growth); #3561 adds the cleanup, and #3562 caps the fill timeout at 90s so hung fills from flapping peers release their "wanted" objects in seconds instead of the 10-minute node default. #3571 also stops two CXO node timeouts (fill / response) from silently disabling themselves at a non-positive config value.
-   **Latent-bug hardening from a codebase audit (#3565, #3566).** Bounded two remaining unbounded `stun.ready` waits that could wedge transport setup or the heartbeat client at boot, and pruned an unbounded per-transport bandwidth-baseline map that grew with transport churn.
-   **`.dmsg` / `.skynet` for a whole LAN (#3572, #3576, #3574).** The embedded resolving proxy's SOCKS5 bind is now configurable via `skywire autoconfig --dmsgweb-addr 0.0.0.0`, so one board with a visor becomes a `.dmsg`/`.skynet` gateway for a home-router (OpenWRT / DD-WRT) LAN — every device reaches dmsg-only deployment services (reward system, transport discovery, …) with no per-device install. New guide: [A `.dmsg` / `.skynet` gateway for your LAN](https://skycoin.github.io/skywire/guides/dmsg-lan-gateway/).
-   **Go-native VPN-router control plane (#3556, #3558).** The vpn-router's `ip`/iptables shell-outs move to a pure-Go netlink + nftables backend (tag-gated), toward a dependency-free gokrazy appliance image, and add a firewall backend seam.
-   **Vendored skycoin bumped to develop HEAD (#3575).**

-   feat(autoconfig): DMSGWEBADDR/SKYNETWEBADDR + rework LAN-gateway guide  [#3576](https://github.com/skycoin/skywire/pull/3576)
-   chore(vendor): bump skycoin to develop HEAD (skycoin#2954)  [#3575](https://github.com/skycoin/skywire/pull/3575)
-   docs(guides): .dmsg gateway for a home router (OpenWRT / DD-WRT)  [#3574](https://github.com/skycoin/skywire/pull/3574)
-   feat(resolver): configurable SOCKS5 bind host so a board can serve the LAN  [#3572](https://github.com/skycoin/skywire/pull/3572)
-   fix(cxo): treat non-positive fill/response timeouts as default, not disabled  [#3571](https://github.com/skycoin/skywire/pull/3571)
-   fix(httpauth): share the nonce per (addr, PK) to stop concurrent-request 401s  [#3570](https://github.com/skycoin/skywire/pull/3570)
-   test(visor): regression-guard the reward-uptime decouple in CI  [#3567](https://github.com/skycoin/skywire/pull/3567)
-   fix(visor/stats): prune day-start bandwidth baselines (unbounded map)  [#3566](https://github.com/skycoin/skywire/pull/3566)
-   fix(visor): bound the remaining unbounded stun.ready waits  [#3565](https://github.com/skycoin/skywire/pull/3565)
-   fix(tpd): log reward-heartbeat store failures instead of dropping them silently  [#3564](https://github.com/skycoin/skywire/pull/3564)
-   fix(visor): TPD uptime heartbeat must run without the standalone tracker  [#3563](https://github.com/skycoin/skywire/pull/3563)
-   fix(tpd): cap aggregator fill time to release hung-fill memory  [#3562](https://github.com/skycoin/skywire/pull/3562)
-   fix(tpd): prune superseded CXO roots in the aggregator (memory leak)  [#3561](https://github.com/skycoin/skywire/pull/3561)
-   feat(vpnrouter): firewall backend seam (iptables | nftables) — completes Phase 0b  [#3558](https://github.com/skycoin/skywire/pull/3558)
-   feat(vpn): Go-native control plane — netlink (default) + nftables firewall (tag-gated)  [#3556](https://github.com/skycoin/skywire/pull/3556)

## 1.3.88

16 PRs on top of v1.3.87. Headline: the **VPN router** graduates from prototype to a fully configurable, documented feature, and a new **mesh gateway** lets ordinary devices reach `.dmsg` / `.skynet` services by name.

-   **VPN router — turn a visor into a VPN gateway for LAN/WiFi clients.** A visor with the `vpn-router` app aggregates downstream clients on a wired NIC or a WiFi AP and NATs their traffic into the mesh-VPN tunnel the `vpn-client` maintains. This release makes it real and operable end to end: it's **enableable from `config gen` / autoconfig / `skywire.conf`** (#3534); the external `dnsmasq` dependency is replaced by an **embedded pure-Go DHCP + DNS engine** (vendored router7) so the router has no runtime deps (#3536); a **standalone run mode + LAN-through-tunnel policy routing** lets it run without visor coupling and routes only forwarded-in client traffic through the tun — matching by input interface, not source subnet, so the router's own uplink and local services are untouched (#3542, #3551); forwarded **TCP MSS is clamped to the path MTU** so large flows don't black-hole across the tunnel (#3549); the **WiFi-out variant** disables power-save before `hostapd` for rtl8723bs AP stability (#3543); **every uplink×downstream topology is configurable** (ethernet-out / WiFi-out, subnet, SSID/band/channel/country) (#3550); and a **complete VPN-router guide** documents all three deployment modes plus the real-world hardware measures (#3552).
-   **Mesh gateway — reach `.dmsg` / `.skynet` services by name (#3553).** The vpn-router (and, opt-in, the vpn-client host itself) answers `*.dmsg` / `*.skynet` DNS with a synthetic IP and transparently proxies those connections over the mesh — no per-device config, no SOCKS. A `.dmsg` name encodes a public key and a mesh routing port (often no externally-listening TCP port at all), so it can't be NAT'd: instead DNS leases a synthetic IP, an `iptables` REDIRECT sends pool-bound TCP to a local proxy, and the proxy reads `SO_ORIGINAL_DST` (the original port **is** the routing port) and dials it over the mesh — bypassing the tunnel. HTTPS works via on-the-fly **TLS-MITM** with a self-generated CA, and friendly **aliases** map names to keys. Validated end-to-end against real Linux netfilter.
-   **VPN reliability fixes — the VPN works again.** `DmsgVisorRPCPort` is moved off **44**, which collided with the `vpn-server` port and broke server startup (#3544, re-embedded wasm-visor #3545, e2e guard #3547); and the tunnel-exemption logic now **exempts every transport peer's IP** (not just stcpr), so a VPN client no longer tears down a pre-existing transport when it starts — with an IP-routable-only guard to avoid a regression on non-IP (webrtc) peers (#3546, #3548).
-   **dmsgpty** (#3541): auto-establish a direct transport before the skynet pty dial, so `cli dmsg pty` over skynet doesn't fail waiting on a route.
-   **Deployment** (#3554): split dmsg servers 2 and 3 onto new hosts (server migration).

-   separate two dsmg-server 2 and 3 to new servers  [#3554](https://github.com/skycoin/skywire/pull/3554)
-   feat(vpn): mesh gateway for .dmsg/.skynet — vpn-router + vpn-client  [#3553](https://github.com/skycoin/skywire/pull/3553)
-   docs(vpn): complete VPN-router guide (all modes + real-world measures)  [#3552](https://github.com/skycoin/skywire/pull/3552)
-   fix(vpnrouter): policy-route by input interface, not source subnet  [#3551](https://github.com/skycoin/skywire/pull/3551)
-   feat(vpnrouter): configure all router variants (ethernet/WiFi) + setup doc  [#3550](https://github.com/skycoin/skywire/pull/3550)
-   fix(vpnrouter): clamp forwarded TCP MSS to the path MTU  [#3549](https://github.com/skycoin/skywire/pull/3549)
-   fix(vpn): only exempt transport peers with a routable IP (regression from #3546)  [#3548](https://github.com/skycoin/skywire/pull/3548)
-   test(e2e): guard the port-44 vpn-server collision (#3544)  [#3547](https://github.com/skycoin/skywire/pull/3547)
-   fix(vpn): exempt ALL transport peer IPs from the tunnel, not just stcpr  [#3546](https://github.com/skycoin/skywire/pull/3546)
-   chore(wasm): re-embed wasm-visor after the skyenv port-44 fix (#3544)  [#3545](https://github.com/skycoin/skywire/pull/3545)
-   fix(skyenv): move DmsgVisorRPCPort off 44 (collided with vpn-server)  [#3544](https://github.com/skycoin/skywire/pull/3544)
-   fix(vpnrouter): disable Wi-Fi power-save before hostapd (rtl8723bs AP stability)  [#3543](https://github.com/skycoin/skywire/pull/3543)
-   feat(vpnrouter): standalone run mode + LAN-through-tunnel policy routing  [#3542](https://github.com/skycoin/skywire/pull/3542)
-   fix(dmsgpty): auto-establish a direct transport before the skynet pty dial  [#3541](https://github.com/skycoin/skywire/pull/3541)
-   feat(vpnrouter): replace dnsmasq with an embedded pure-Go DHCP+DNS engine (router7)  [#3536](https://github.com/skycoin/skywire/pull/3536)
-   feat(vpn-router): enableable via config-gen + autoconfig + skywire.conf  [#3534](https://github.com/skycoin/skywire/pull/3534)

## 1.3.79

3 PRs on top of v1.3.78. Headline items:

-   **Bandwidth-reward telemetry restored + the browser-tab wasm visor becomes a full network participant (#3362).** A browser-tab wasm visor now reports each transport's bandwidth + latency to the transport-discovery over an **in-memory CXO feed** — the bbolt-backed CXDS/IdxDB datastores are build-tag-split so CXO compiles under `js/wasm`. This closes the gap that made browser-visor transports invisible to the bandwidth-reward calculation. The same PR carries the wasm-visor flagship: **federated group chat in the browser** (in-memory store + in-memory CXO over dmsg — the group record store is build-tag-split for js/wasm), a **shared skychat wire codec** (`pkg/skychat/message`) that collapses three duplicated framing implementations into one, a **fast-reconnect fix** that cuts federated-group first-message latency from ~30–40s to ~2s (adaptive warmup cadence + the standalone driver), a **dmsg/skynet transport selector + activity-log pane** in the skychat window, a WinBox z-order fix (new windows open in front) and a de-serialized skysocks route setup, plus a Go coding-standard compliance pass (gofmt/vet/golangci-lint green; documented gosec false-positives) and a dependency refresh. Docs: wasm-visor UI tour + skychat refactor RFC.
-   **Deployment** (#3358): add dmsg server `02a49bc0` @ `143.42.59.213:30088` to `services-config.json`.
-   **Dependencies** (#3364): bump the skywire-manager-src sigstore family — `sigstore` 4.1.1, `@sigstore/verify` 3.1.1, `@sigstore/core` 3.2.1 (replicates dependabot #3363/#3361/#3345).

-   feat(wasm-hv): in-memory CXO telemetry — browser visor reports transport bandwidth+latency to TPD  [#3362](https://github.com/skycoin/skywire/pull/3362)
-   chore(deployment): add dmsg server 02a49bc0 @ 143.42.59.213:30088 to services-config.json  [#3358](https://github.com/skycoin/skywire/pull/3358)
-   chore(deps): bump sigstore 4.1.1 / @sigstore/verify 3.1.1 / @sigstore/core 3.2.1  [#3364](https://github.com/skycoin/skywire/pull/3364)

## 1.3.64

14 PRs on top of v1.3.63. Headline items:

-   **dmsg-only configs by default.** `skywire cli config gen` now generates a dmsg-only (dmsghttp) config by default, with `--dual` for the http+dmsg-fallback config and `--http` for http-only (#2983). Supporting hardening makes dmsg-only robust: an empty discovery URL resolves to a no-op client instead of an invalid-host retry spin (#2981); service-discovery falls back to its dmsg URL with nil-client guards, so a dropped HTTP SD URL no longer panics the visor at startup (#2982); and public-autoconnect no longer silently re-defaults a dropped `service_discovery` to the prod HTTP endpoint (#2985).
-   **Routing reliability.** A single hop's failure no longer RSTs the whole route during setup (#2976); the router guards a `ClosePacket` panic, a double-close, a reverse-rule leak, and a MinHops race (#2977); and "ghost" transports — offline edges that stayed routable because the live edge kept re-registering them — are drained at the source: visors detect half-open links via missed transport pongs (#2979) and TPD expires each edge's transport set on its own TTL (#2980).
-   **TPD egress + visor CPU.** The dominant `/all-transports` egress storm is cut with a cached, pre-gzipped response body (#2980); the dmsghttp client now advertises `Accept-Encoding: gzip` and transparently decompresses, so dmsg service reads compress like clearnet ones (#2986); `/transports/edge` gets the same cached+gzipped treatment (#2987); and the CXO Filler no longer spawns thousands of parked goroutines — `MaxFillingParallel` was never populated and fell back to a magic 1024 (#2988).
-   **Windows installer restored** (#2989): the MSI build had been failing since v1.3.62 (a `Product.wxs` file reference added in #2965 was never staged in the release workflow), so v1.3.62/v1.3.63 shipped with no Windows `.msi`/`.zip` assets — fixed by staging `skywire-autoconfig.bat`.
-   **Rewards** (#2990): drop the receiver-side `min()` cap (deferred from v1.3.63) and set the per-IP share cap to 12 for June 2026.
-   **Diagnostics** (#2984): instrument the cipher verify / DH caches and expose a `/debug/cache` endpoint.

-   feat(rewards): drop receiver-side min() cap (pending v1.3.63); per-IP 12 for June 2026  [#2990](https://github.com/skycoin/skywire/pull/2990)
-   fix(release): stage skywire-autoconfig.bat for the Windows MSI build  [#2989](https://github.com/skycoin/skywire/pull/2989)
-   fix(cxo): bound Filler goroutines — populate MaxFillingParallel + sane fallback  [#2988](https://github.com/skycoin/skywire/pull/2988)
-   fix(tpd): cache /transports/edge:<PK> response body + gzip  [#2987](https://github.com/skycoin/skywire/pull/2987)
-   fix(dmsghttp): advertise Accept-Encoding: gzip + transparent decompress  [#2986](https://github.com/skycoin/skywire/pull/2986)
-   fix(visor/autoconnect): don't re-default dropped service_discovery to prod HTTP  [#2985](https://github.com/skycoin/skywire/pull/2985)
-   feat(cipher,dmsg/noise,cmdutil): instrument the verify / DH caches + /debug/cache endpoint  [#2984](https://github.com/skycoin/skywire/pull/2984)
-   feat(cli/config): generate dmsg-only config by default; add --dual flag  [#2983](https://github.com/skycoin/skywire/pull/2983)
-   fix(visor/servicedisc): service-discovery dmsg fallback + nil-client guards (no startup panic when HTTP SD dropped)  [#2982](https://github.com/skycoin/skywire/pull/2982)
-   fix(dmsg/disc): empty discovery URL → no-op client (no invalid-host retry spin)  [#2981](https://github.com/skycoin/skywire/pull/2981)
-   fix(tpd): drain ghost transports (per-edge-TTL) + cache/gzip /all-transports egress storm  [#2980](https://github.com/skycoin/skywire/pull/2980)
-   fix(transport): detect half-open links via missed transport pongs (drain ghost transports from TPD)  [#2979](https://github.com/skycoin/skywire/pull/2979)
-   fix(router): guard ClosePacket panic, double-close, reverse-rule leak, MinHops race  [#2977](https://github.com/skycoin/skywire/pull/2977)
-   fix(router): stop one hop's failure from RST-ing the whole route (setup cascade)  [#2976](https://github.com/skycoin/skywire/pull/2976)

## 1.3.63

7 PRs on top of v1.3.62. Headline items:

-   **Reward-accounting integrity.** TPD no longer attributes bandwidth to the zero PubKey — the source of thousands of null-PK "orphan" transport records and ghost-attributed bandwidth in `/metrics` (#2971) — fixed at the root in CXO by binding a received Root's decoded `Pub` to its cryptographically-authenticated feed, so a mis-attributed or zero publisher key can no longer be credited to the wrong (or no) visor (#2972); and the reward-server stats summary now credits single-edge-reported transports instead of zeroing them via an AND-gated `min()` (#2973).
-   **Routing / dmsg robustness.** The router no longer downgrades a dial's MinHops to 1 over a *closed* direct transport — which made the route-finder return dead 1-hop routes and made `cli route trace` disagree with the live dial path (#2970); and the dmsg-first discovery client no longer degrades to plain HTTP on a transient `dmsg error 202` ("cannot connect to delegated server"), which self-heals over dmsg on the next refresh (#2974).
-   **gotop multiload display** (#2968): a single `--multiload` flag switches `cli gotop` to a multiload-ng-style breakdown — per-state CPU (incl. iowait), RAM used/buffers/cache, disk read/write throughput, and a temperature graph (also fixes the temperature panel rendering empty).
-   **Reward-UI polish** (#2969): titled visor counts, No-transports cutoff fix, linkified reasons, pool caption.

-   `feat(cli/gotop)`: multiload-ng-style display behind --multiload (CPU + RAM breakdown)  [#2968](https://github.com/skycoin/skywire/pull/2968)
-   `feat(rewards/ui)`: title visor counts, fix No-transports cutoff, linkify reasons, pool caption  [#2969](https://github.com/skycoin/skywire/pull/2969)
-   `fix(router)`: don't downgrade MinHops to 1 over a closed direct transport  [#2970](https://github.com/skycoin/skywire/pull/2970)
-   `fix(tpd)`: never attribute bandwidth to the zero PubKey (null-PK orphans + ghost /metrics)  [#2971](https://github.com/skycoin/skywire/pull/2971)
-   `fix(cxo)`: bind a received Root's Pub to its authenticated feed  [#2972](https://github.com/skycoin/skywire/pull/2972)
-   `fix(rewards/server)`: credit single-edge-reported transports in the stats summary  [#2973](https://github.com/skycoin/skywire/pull/2973)
-   `fix(dmsg/disc)`: don't HTTP-fallback on dmsg 202 "cannot connect to delegated server"  [#2974](https://github.com/skycoin/skywire/pull/2974)

## 1.3.62

31 PRs on top of v1.3.61. Headline items:

-   **Multiplexed routing unblocked.** Aux mux legs are no longer selected for sending until the peer has registered their rule, fixing the mux≥2 "0 bytes / close code 0" stall that made multi-route spreading transfer nothing (#2962), on top of mux-degree-aware route counts and a bulk-snapshot TPD hop cache (#2955).
-   **VPN client UI pass** (#2963): fixes the dead servers tab (a full-viewport map overlay was swallowing clicks), reloads the History/Favorites/Blocked tabs correctly, decouples server-selection from connecting, adds inline multihop / multiplex route options on the status page, and removes the dead passcode plumbing.
-   **dmsg / hypervisor stability.** Eliminates the dmsg-discovery HTTP-fallback churn by raising the entry-cache TTL above the tracker interval (#2958) and scoping the round-trip tracker to currently-connected visors (#2959), with a negative-cache 202 backoff folded into #2963; plus hvui browser-error forwarding to the visor log (#2961) and transitive hypervisor whitelisting (#2940).
-   **Rewards accounting + Windows install.** TPD `/metrics` now retains the bandwidth history of deregistered/offline transports (#2966) — fixing the shrinking bandwidth-history chart and a bandwidth-reward undercount (transports that carried traffic but went offline before the daily reward collect ran); and the Windows MSI runs config setup at install time for postinstall parity with Linux (#2965).

Continued rewards / reward-UI work and CLI improvements round out the release.

-   `fix(tpd)`: include offline transports with bandwidth history in /metrics — fixes shrinking bandwidth chart + reward undercount  [#2966](https://github.com/skycoin/skywire/pull/2966)
-   `fix(win_installer)`: run config setup at MSI install (postinstall parity with Linux)  [#2965](https://github.com/skycoin/skywire/pull/2965)
-   `chore(release)`: v1.3.62 changelog + free routing port 46 for hv-RPC skynet parity (drop redundant route-based latency probe)  [#2964](https://github.com/skycoin/skywire/pull/2964)
-   `fix(router)`: gate aux mux legs on readiness — fixes mux>=2 stall (0 bytes / close code 0)  [#2962](https://github.com/skycoin/skywire/pull/2962)
-   `fix(vpn-ui,dmsg)`: dead servers tab, select/connect decouple, route options, dmsg-tracker 202 backoff  [#2963](https://github.com/skycoin/skywire/pull/2963)
-   `fix(cli/route)`: dispatch `route policy test`/`bench` to the WASM backend for .wasm scripts  [#2960](https://github.com/skycoin/skywire/pull/2960)
-   `feat(hypervisor)`: forward hvui browser errors to the visor log  [#2961](https://github.com/skycoin/skywire/pull/2961)
-   `fix(hypervisor)`: scope dmsg round-trip tracker to currently-connected visors  [#2959](https://github.com/skycoin/skywire/pull/2959)
-   `fix(dmsg)`: raise entry-cache TTL above the tracker interval to stop disc-dial thrash  [#2958](https://github.com/skycoin/skywire/pull/2958)
-   `feat(autoconfig)`: add --maxtransports (public-visor limit); drop deprecated --dmsghttp/--dmsgconf  [#2957](https://github.com/skycoin/skywire/pull/2957)
-   `fix(router)`: bulk-snapshot TPD for hop lookups + honor mux degree in route count  [#2955](https://github.com/skycoin/skywire/pull/2955)
-   `log(dmsg/dmsgfirst)`: surface DMSG-discovery HTTP fallback at Warn (was Debug)  [#2953](https://github.com/skycoin/skywire/pull/2953)
-   `feat(skychat)`: CXO-backed messaging over native TCP in standalone (P2P, no dmsg)  [#2950](https://github.com/skycoin/skywire/pull/2950)
-   `feat(rewards/ui)`: add resources nav dropdown + home page intro section  [#2952](https://github.com/skycoin/skywire/pull/2952)
-   `feat(rewards/ui)`: SEO enrichment for per-date reward pages  [#2951](https://github.com/skycoin/skywire/pull/2951)
-   `fix(rewards/ui)`: exclude per-pool CSVs from /skycoin-rewards/csv endpoint  [#2949](https://github.com/skycoin/skywire/pull/2949)
-   `fix(skywire-cli)`: isUnderBase boundary match — stop /all-transports/per-key-stats aliasing onto the CXO feed  [#2948](https://github.com/skycoin/skywire/pull/2948)
-   `feat(rewards/ui)`: per-pool tables + bandwidth-mode shares SKY column fix  [#2947](https://github.com/skycoin/skywire/pull/2947)
-   `feat(rewards)`: per-pool detail CSVs + defensive empty-file fallback  [#2946](https://github.com/skycoin/skywire/pull/2946)
-   `feat(visor)`: transitive hypervisor whitelist — trust flows up the hypervisor chain  [#2940](https://github.com/skycoin/skywire/pull/2940)
-   `feat(cli/svc)`: `svc health --service <pk> --dmsg-server <pk>` — per-server service /health  [#2944](https://github.com/skycoin/skywire/pull/2944)
-   `fix(cxo/node)`: buffer delcq so connection cleanup doesn't block the actor  [#2945](https://github.com/skycoin/skywire/pull/2945)
-   `feat(rewards)`: default embedded reward.sh to bandwidth-pool mode  [#2943](https://github.com/skycoin/skywire/pull/2943)
-   `fix(hypervisor)`: guard getPty against nil PtyUI instead of crashing the visor  [#2939](https://github.com/skycoin/skywire/pull/2939)
-   `feat(router/policy)`: expose leg byte counters and hop chain to rotation callbacks  [#2938](https://github.com/skycoin/skywire/pull/2938)
-   `feat(cli)`: --reconnect + --routing-policy on proxy start; --routing-policy on vpn start  [#2937](https://github.com/skycoin/skywire/pull/2937)
-   `feat(survey)`: internalize zcalusic/sysinfo + populate CPU on ARM SBCs and Windows  [#2934](https://github.com/skycoin/skywire/pull/2934)
-   `feat(cli,hvui)`: hv ls multi-section + cross-section column alignment  [#2933](https://github.com/skycoin/skywire/pull/2933)
-   `feat(rewards)`: square-root scaling for the bandwidth pool  [#2929](https://github.com/skycoin/skywire/pull/2929)
-   `fix(lint)`: clear pre-existing lint debt in wasm policy + cxo connmap tests  [#2936](https://github.com/skycoin/skywire/pull/2936)
-   `fix(rewards/bw-collect)`: trust single-edge daily reports + --date/--days flags  [#2923](https://github.com/skycoin/skywire/pull/2923)

## 1.3.61

Patch release. One PR on top of v1.3.60 — fixes a regression where `skywire autoconfig` silently wrote the regenerated config to the wrong path on operator boards where `/etc/skywire.conf` doesn't explicitly set `PKGENV=true` / `USRENV=true`.

Symptom on a freshly-installed v1.3.60 board:

```
# cat /etc/skywire.conf | grep -vE '^#|^$'
HYPERVISORPKS=('0323272a…')
ISHYPERVISOR=true
DMSGPTYPKS=('0323272a…')

# skywire autoconfig
 -> Configuring skywire
 -> version: v1.3.60
 --> Generating skywire config with command:
   SKYENV=/etc/skywire.conf skywire cli config gen -r
 --> Skywire configuration updated
config path: /opt/skywire/skywire.json
 --> Restarting skywire service…

# skywire cli config show .hypervisors
[]
```

Trace: autoconfig's `resolveConfig()` auto-elects pkgEnv via `os.Geteuid() == 0` when the env file doesn't set `PKGENV`/`USRENV` explicitly. The visor binary was deliberately NOT propagating `-p`/`-u` to `cli config gen`, under the theory the child process would re-derive the same mode from `SKYENV`. That theory breaks for the common operator case where neither key is set in the env file — `scriptExecBool("${PKGENV:-false}")` returns false, gen falls through to PreRun's relative-path branch (`confPath = skyenv.ConfigName`), and writes a `skywire-config.json` to the autoconfig invoker's cwd (typically `/root/`). The pre-existing `/opt/skywire/skywire.json` stays untouched, autoconfig's `os.Stat` check passes against the stale file, the visor restarts on its old config, and `hypervisors` stays empty.

Fix: propagate `-p`/`-u` from the autoconfig-resolved mode. The mode is already known at `generateConfig` call time — no new resolution work needed. Same fix removes the matching silent-write-to-cwd that affected operators using HYPERVISORPKS / DMSGPTYPKS / ISHYPERVISOR set via `/etc/skywire.conf`.

### Autoconfig — write to the resolved config path

-   `fix(autoconfig)`: propagate -p/-u to cli config gen so writes hit the resolved path  [#2931](https://github.com/skycoin/skywire/pull/2931)

## 1.3.60

65 PRs on top of v1.3.59. The two headline pieces are the operator-programmable **routing policy** subsystem (Starlark + WASM, multi-phase build-out under RFC #2882) and the **unified app framework** refactor (#2775) that collapses every visor-managed binary into one Internal-app contract.

### Operator-programmable routing policy (RFC #2882)

This release lands the full multi-leg routing-policy stack — operator-supplied policies (Starlark by default, WASM for hot paths) that pick routes, weight forward/reverse asymmetry, and react to per-leg events. Policies are evaluated on dial *and* on packet (Layer 2 distribution + rotation hook), with stdlib helpers for `recent_latency`, `sd_recent_attempts`, multi-hop SD-CXO geo, and a sticky `5tuple` selector. Per-app overrides take precedence over the global policy; runtime swap is supported. The WASM backend (compiled via TinyGo) is wired through the same Provider so any Starlark policy can be re-implemented in WASM for sub-millisecond hot-path evaluation. The hypervisor UI gets a routing-policy panel in the routing tab. Skylark (operator-facing name) is documented; `docs/examples/routing-policies/` ships runnable examples (latency-adaptive, dscp-priority, rotating-bw, sticky:5tuple).

-   `docs`: add operator-programmable routing policy RFC  [#2882](https://github.com/skycoin/skywire/pull/2882)
-   `feat(router)`: Phase 1+2 routing-policy scaffold (Starlark)  [#2883](https://github.com/skycoin/skywire/pull/2883)
-   `feat(router/policy)`: Phase 3 stdlib + Phase 4 loader + CLI tooling  [#2884](https://github.com/skycoin/skywire/pull/2884)
-   `feat(router)`: integrate routing policy into dial path (RFC #2882)  [#2885](https://github.com/skycoin/skywire/pull/2885)
-   `fix(router/policy)`: drop goroutine-based cancellation (race with starlark.Call)  [#2887](https://github.com/skycoin/skywire/pull/2887)
-   `chore`: move policies/smoke-test.star to docs/examples + gitignore /policies/  [#2888](https://github.com/skycoin/skywire/pull/2888)
-   `chore`: gitignore /policies/ working-tree dir  [#2889](https://github.com/skycoin/skywire/pull/2889)
-   `feat(router/policy)`: visor-backed Provider + per-app routing policy  [#2890](https://github.com/skycoin/skywire/pull/2890)
-   `feat(router/policy)`: RouteSelectingHook + Starlark route selection  [#2891](https://github.com/skycoin/skywire/pull/2891)
-   `feat(router/policy)`: SD-CXO geo for multihop intermediates  [#2892](https://github.com/skycoin/skywire/pull/2892)
-   `feat(router/policy)`: finish Layer 2 packet distribution (RFC phase 5)  [#2897](https://github.com/skycoin/skywire/pull/2897)
-   `docs(routing-policy)`: runnable example policies + distribution observability  [#2898](https://github.com/skycoin/skywire/pull/2898)
-   `fix(router)`: strip DMSG hops from multihop routes everywhere  [#2899](https://github.com/skycoin/skywire/pull/2899)
-   `docs(routing-policy)`: fix empty-candidates drop bug across examples  [#2900](https://github.com/skycoin/skywire/pull/2900)
-   `fix(router/policy)`: SelectRoute distribution + leg-count truth + TPD perf  [#2902](https://github.com/skycoin/skywire/pull/2902)
-   `feat(router/policy)`: directional asymmetry (forward/reverse routing)  [#2903](https://github.com/skycoin/skywire/pull/2903)
-   `feat(router/policy)`: on_leg_change callback (RFC #2882 phase 6)  [#2904](https://github.com/skycoin/skywire/pull/2904)
-   `feat(router/policy)`: fallback="direct" + CLI overrides exposed  [#2905](https://github.com/skycoin/skywire/pull/2905)
-   `feat(router/policy)`: sticky:5tuple + latency-adaptive + dscp-priority  [#2906](https://github.com/skycoin/skywire/pull/2906)
-   `feat(router)`: wire DialHook into PingRoute  [#2908](https://github.com/skycoin/skywire/pull/2908)
-   `feat(router)`: mux loop best-effort + local-calc fallback  [#2909](https://github.com/skycoin/skywire/pull/2909)
-   `feat(router)`: parallel mux aux dials  [#2910](https://github.com/skycoin/skywire/pull/2910)
-   `feat(router/policy)`: hook into sky_forward_conn direct dials  [#2911](https://github.com/skycoin/skywire/pull/2911)
-   `docs(routing-policy)`: introduce "skylark" operator-facing name  [#2912](https://github.com/skycoin/skywire/pull/2912)
-   `feat(router/policy)`: WASM backend for routing policies  [#2913](https://github.com/skycoin/skywire/pull/2913)
-   `feat(router/policy)`: periodic rotation hook + WASM ABI extension  [#2916](https://github.com/skycoin/skywire/pull/2916)
-   `feat(hypervisor-ui)`: routing-policy panel in the routing tab  [#2918](https://github.com/skycoin/skywire/pull/2918)
-   `feat(router/policy)`: re-land extension dispatch + runtime per-app swap  [#2919](https://github.com/skycoin/skywire/pull/2919)
-   `fix(router)`: start rotation loop from SetRotation, not init  [#2921](https://github.com/skycoin/skywire/pull/2921)
-   `feat(router/policy)`: avoid_direct knob to force overlay path  [#2922](https://github.com/skycoin/skywire/pull/2922)
-   `refactor(router/policy)`: drop avoid_direct, rely on min_hops + re-apply AppName threading  [#2924](https://github.com/skycoin/skywire/pull/2924)
-   `fix(examples)`: rotating-bw policy gates drops on alive_count >= target  [#2925](https://github.com/skycoin/skywire/pull/2925)
-   `fix(router)`: tune circuit breaker for dmsg-session refresh cadence  [#2928](https://github.com/skycoin/skywire/pull/2928)

### Unified app framework (#2775)

A multi-phase refactor that names the implicit app contract every visor-managed binary already followed, then collapses each binary's three roles (cli / host / ui) into a single Internal app run with declared run-modes. `pty` (formerly `dmsgpty`) was the first migration to Internal-app, with RestartPolicy=Always; `dmsgweb` and `skynetweb` followed under RestartPolicy=OnFailure. `skysocks`, `vpn`, and `skynet` got their pair-binaries collapsed (delegating-wrapper helper extracted to fix recursion). The pkg/dmsg/dmsgpty tree was renamed to pkg/pty; the `Dmsgpty` config field was renamed to `Pty` with backward-compat JSON unmarshal. The cmd/dmsg/dmsgpty-{host,ui,cli} binaries were renamed to pty-*.

-   `docs`: RFC — unified service contract (#2775)  [#2863](https://github.com/skycoin/skywire/pull/2863)
-   `refactor(app)`: name the implicit app contract (#2775 phase 1)  [#2860](https://github.com/skycoin/skywire/pull/2860)
-   `refactor(app)`: extract status/error/port helpers onto *app.Client (#2775 phase 2a)  [#2861](https://github.com/skycoin/skywire/pull/2861)
-   `refactor(app)`: type ProcConfig.RunFunc as AppFunc (#2775 phase 2b)  [#2862](https://github.com/skycoin/skywire/pull/2862)
-   `refactor(pty)`: consolidate dmsgpty cli/host/ui under `skywire app pty`  [#2864](https://github.com/skycoin/skywire/pull/2864)
-   `chore(pty)`: gofmt follow-up for #2864  [#2865](https://github.com/skycoin/skywire/pull/2865)
-   `fix(pty)`: keep `dmsg pty *` subtree intact; hide-not-remove in skywire context  [#2866](https://github.com/skycoin/skywire/pull/2866)
-   `refactor(visor)`: pty as Internal app w/ RestartPolicy=Always (#2775 phase 3.3)  [#2867](https://github.com/skycoin/skywire/pull/2867)
-   `chore`: gofmt sweep for two pre-existing lint failures  [#2868](https://github.com/skycoin/skywire/pull/2868)
-   `fix(visor)`: pty AppFunc must complete the in-process IPC handshake  [#2869](https://github.com/skycoin/skywire/pull/2869)
-   `fix(launcher)`: RestartPolicy=Always must re-Start on operator stop  [#2870](https://github.com/skycoin/skywire/pull/2870)
-   `refactor(visor)`: dmsgweb + skynetweb as Internal apps (#2775 phase 3.2)  [#2871](https://github.com/skycoin/skywire/pull/2871)
-   `refactor(visor)`: RestartPolicy=OnFailure for dmsgweb + skynetweb (#2775 phase 4)  [#2872](https://github.com/skycoin/skywire/pull/2872)
-   `refactor(apps)`: skysocks pair collapse + fix delegating-wrapper recursion  [#2873](https://github.com/skycoin/skywire/pull/2873)
-   `refactor(apps)`: vpn pair collapse + extract shared delegating-wrapper helper  [#2874](https://github.com/skycoin/skywire/pull/2874)
-   `refactor(apps)`: skynet pair collapse (final pair binary)  [#2875](https://github.com/skycoin/skywire/pull/2875)
-   `refactor`: rename pkg/dmsg/dmsgpty → pkg/pty  [#2876](https://github.com/skycoin/skywire/pull/2876)
-   `refactor(visorconfig)`: rename Dmsgpty → Pty w/ JSON backward-compat  [#2877](https://github.com/skycoin/skywire/pull/2877)
-   `fix(visorconfig)`: V1.UnmarshalJSON must not wipe preset *Common  [#2878](https://github.com/skycoin/skywire/pull/2878)
-   `refactor(cmd)`: rename cmd/dmsg/dmsgpty-{host,ui,cli} → pty-*  [#2879](https://github.com/skycoin/skywire/pull/2879)
-   `chore`: remove run-visor.sh local dev helper + gofmt fix  [#2880](https://github.com/skycoin/skywire/pull/2880)

### Rewards: bandwidth-pool sender-pays + eligible-pair filter

The bandwidth-rewards pool was being skewed by two issues: the UT cache directory was unbounded growth in /tmp, and the per-pair bandwidth credit was counted on both ends so any high-traffic transport pumped its peer's reward too. This release pins UT cache under `hist/`, makes the bandwidth pool sender-pays (credit goes to the visor that originated the bytes, not both ends), and filters out pairs that aren't reward-eligible before the pool is split. Mainnet rules docs are updated: stale CSV claims retired, v1.3.59 cutoff noted, bandwidth-pool precision floor documented.

-   `fix(rewards)`: pin UT cache to hist/ + bandwidth pool sender-pays + eligible-pair filter  [#2886](https://github.com/skycoin/skywire/pull/2886)
-   `fix(transport)`: count VStream + ping bytes in sent counter  [#2927](https://github.com/skycoin/skywire/pull/2927)
-   `docs(mainnet_rules)`: retire CSV claims, add v1.3.59 cutoff, note bw-pool precision floor  [#2920](https://github.com/skycoin/skywire/pull/2920)

### bbolt corruption recovery — visor no longer crash-loops on damaged stores

Five visor bbolt stores (serviceuptime, app log_store, clicache, usermanager, skychat group) were opening their files unprotected — a corrupt freelist (e.g. `invalid freelist page: N, page type is leaf` from bbolt's freelist.read during Open) would panic on first write and crash-loop the whole visor. RepairIfCorrupt now wraps every visor bbolt.Open, AND the probeIntegrity helper recovers from panics inside bbolt.Open itself (the freelist-corruption panic is treated as proof of corruption, the file is moved aside as `*.corrupt.<unix-ts>`, and the next open creates a fresh empty store). This shipped after an operator's v1.3.59 visor crash-looped on exactly this signature and recovery required hand-removal of `/opt/skywire/local`.

-   `fix(bbolthealth)`: cover all visor bbolt opens + recover from probe panic  [#2926](https://github.com/skycoin/skywire/pull/2926)

### Hypervisor PK endpoint

A small operator-facing addition: `GET /api/pk` returns the hypervisor's own PK so an install-page generator (or any operator tool) can discover it without hand-copying from the config. The endpoint is opt-in (#2896 flipped the default after the initial #2895), gated behind `SW-Public` (so it only answers when the hypervisor is intentionally publicly-reachable), and exposed as a `--pk-endpoint` / `--no-pk-endpoint` flag from autoconfig (#2901).

-   `feat(hypervisor)`: GET /api/pk + DisablePKEndpoint + SW-Public gate  [#2895](https://github.com/skycoin/skywire/pull/2895)
-   `fix(hypervisor)`: flip pk-endpoint to opt-in + add config-gen flag  [#2896](https://github.com/skycoin/skywire/pull/2896)
-   `feat(autoconfig)`: --pk-endpoint / --no-pk-endpoint flag  [#2901](https://github.com/skycoin/skywire/pull/2901)

### Apps + CXO

-   `refactor(apps)`: skysocks/vpn/skynet pair collapses (see "Unified app framework" above)
-   `feat(apps)`: --reconnect for skysocks-client and skynet-client  [#2914](https://github.com/skycoin/skywire/pull/2914)
-   `fix(cxo/node)`: shard connection maps to remove Node.mx accept-path serialization  [#2907](https://github.com/skycoin/skywire/pull/2907)

### Lint + deps

-   `chore(lint)`: gofmt + misspell + errcheck cleanup on develop  [#2893](https://github.com/skycoin/skywire/pull/2893)
-   `chore(deps)`: bump tmp from 0.2.5 to 0.2.7 in /static/skywire-manager-src  [#2894](https://github.com/skycoin/skywire/pull/2894)

## 1.3.59

Patch release. One PR on top of v1.3.58 — completes the hypervisor-UI responsiveness work started in #2842 → #2858.

Operators noticed that opening the hypervisor UI after a long closed-tab gap surfaced "fewer nodes / more last-seen-at" rows that filled in over the next 30-60s as the UI's per-peer Summary polls landed. Trace: the hypervisor only fired Summary RPCs when `/api/visors-summary` was hit. Closed UI = no polling = `summaryCache` rotted. Worse, peer-side `idleConn` (90s, #2856) closed every served stream from "no UI traffic" alone, peers cycled redial + re-accept on every idle window for nothing.

This release ships a hypervisor-side background poll loop (30s cadence, independent of UI activity) that keeps both the cache always-fresh AND the served streams warm. Opening the UI after any duration now renders every connected peer from cache instantly. Streams stay alive on whatever transport they upgraded to (typically stcpr on LAN, dmsg as bootstrap+fallback).

### Hypervisor UI responsiveness

-   `fix(hypervisor)`: background summaryCache poll independent of UI activity  [#2858](https://github.com/skycoin/skywire/pull/2858)

## 1.3.58

Patch release. Four PRs on top of v1.3.57. All targeted at the "nodes appear slowly after restart and some flicker to last-seen-at" regression operators saw after upgrading to v1.3.57.

Trace: #2842's cache-update-on-success in the inner goroutine was the right call (it fixed an earlier stale-cache pathology), but it also moved RPC-failure eviction into the inner goroutine — that half was over-aggressive. Slow-but-functional peers (dmsg jitter, route flap, transient load) got kicked out of `remoteVisors` on every poll round; peers redialed and re-registered, but during the seconds between eviction and re-accept the UI rendered them via the sweep branch as `last seen at: …` — visible flicker.

#2855 walks back the aggressive eviction (the load-bearing cache-update stays). #2852 + #2853 widen the cache-fresh window to 3min (covering the dmsg.StreamIdleTimeout cycle) for both the in-remoteVisors fallback and the sweep branch. #2856 shrinks the visor-side hypervisor-RPC idle-timeout from 10min → 90s so when the stream genuinely dies silently the visor notices in seconds instead of minutes.

End-to-end: hypervisor UI matches the pre-#2842 behavior operators remember — peers stay online through normal flap cycles, only flip to "last seen at" when truly gone, and recover within ~90s of any silent stream death.

### Hypervisor UI stale-row regression

-   `fix(hypervisor)`: widen cache-fresh window 30s → 3min to absorb stream-idle cycle  [#2852](https://github.com/skycoin/skywire/pull/2852)
-   `fix(hypervisor)`: apply cache-fresh window to sweep branch too  [#2853](https://github.com/skycoin/skywire/pull/2853)
-   `fix(hypervisor)`: walk back the aggressive-eviction half of #2842  [#2855](https://github.com/skycoin/skywire/pull/2855)
-   `fix(visor)`: shorten hypervisor RPC idle-timeout 10min → 90s  [#2856](https://github.com/skycoin/skywire/pull/2856)

## 1.3.57

Patch release. 47 PRs on top of v1.3.56.

The big-picture pieces, in order from initial draft (#2805–#2819) to the additional work that landed before tagging (#2820–#2850):

visor↔hypervisor RPC self-heals on degraded sessions where it used to silently strand. The skynet-preferred dial from #2802 is now actually skynet-*preferred* rather than skynet-*first* — dmsg is the always-available baseline, skynet kicks in when a real transport exists, and a cooldown drops back to dmsg after any skynet failure. The TransportRPCServer init-order bug (it never started on any visor, silently) is fixed; combined with a shared `VStreamMux` and auto-create-transport, transport-RPC actually works end-to-end. `TCP_NODELAY` is now set on every TCP transport — hvui skypty drops from "laggy at every keystroke" to local-feeling. Hypervisor tree-summary surfaces sub-hypervisor sections correctly (Hostname propagated through HVVisorEntry, managed-sub-hypervisors no longer scrubbed from the local section, ghost rows on failed Summary fetches are gone). Remote-RPC UX is unified: `skywire cli <anything> --via dmsg://<pk>` or `--via skynet://<pk>` routes any CLI command through the local visor to a remote one — no separate CLI keypair, no special-case subcommand (`tp-rpc` is removed).

Post-draft work added: the install-page WASM generator (skywire-bin install command generator, TinyGo-compiled) — a chain of refactors purging `net/http` and `encoding/json` from the visorconfig + autoconfigcmd + deployment graph so TinyGo can compile the install-page bundle (final size: 1.85 MB, down 75% from the Go-WASM variant). Hypervisor's stale-summary-cache that froze visor rows at "last seen N hours ago" is fixed (cache update + eviction now live inside the inner RPC goroutine, with a 30s freshness window so genuinely-slow peers don't flicker offline). `cli dmsg pty exec --scheme dmsg|skynet` pin the transport explicitly so an unreachable skynet leg can't time out the MultiDialer chain. Services (transport-discovery, address-resolver, route-finder, uptime-tracker, dmsg-discovery) now stop self-publishing in dmsg-discovery — they're direct-client-reachable via the preloaded server set and never needed a discovery entry; this drops the load on dmsgd considerably. `dmsgfirst` discovery client now uses the direct-client-backed `dmsgDC` for its primary path (was the main `dmsgC` whose own discovery is dmsgfirst — recursion-prone, fell back to HTTP on every entry refresh). dmsgpty's skynet leg actually works end-to-end now (listener init-ordering race + PK extractor accepting both `appnet.Addr` and `routing.Addr` types). One stale port in the embedded keyring (`70.121.13.123:9082` → `:9083`) fixed.

Hypervisor UI: node-list sort extended to ip-location / transports / services / reward columns; drag-drop row reorder with localStorage persistence; version-distribution stacked-area chart added to `/nodes/uptime` sourced from the TPD integrated uptime tracker (no separate uptime-tracker round-trip). Standalone-mode operator guides added for `skywire app skychat --standalone --tcp-listen` and `skywire dmsg pty host --tcplisten`, including port-forwarding caveats.

CI: Windows arm64 MSI build enabled; release workflow auto-publishes the draft once every artifact uploads; `make check` serialized via flock so concurrent invocations don't trip each other.

### Hypervisor RPC reliability

-   `fix(visor)`: skypty + RPC dialer hard-timeout the skynet attempt  [#2805](https://github.com/skycoin/skywire/pull/2805)
-   `fix(visor+hypervisor)`: self-heal orphan RPC conns on degraded dmsg  [#2806](https://github.com/skycoin/skywire/pull/2806)
-   `fix(visor)`: skynet-preferred (not skynet-first) for hypervisor RPC conn  [#2807](https://github.com/skycoin/skywire/pull/2807)
-   `fix(visor)`: transport-RPC actually works — shared mux + init order + auto-create transport  [#2810](https://github.com/skycoin/skywire/pull/2810)
-   `fix(transport,dmsg)`: TCP_NODELAY on all interactive paths  [#2818](https://github.com/skycoin/skywire/pull/2818)
-   `fix(transport)`: remove recursive RLock in Manager.GetTransport  [#2821](https://github.com/skycoin/skywire/pull/2821)
-   `fix(visor,cli/dmsg/pty)`: hard-bound DmsgPtyExec dial + honor SIGINT in cli  [#2824](https://github.com/skycoin/skywire/pull/2824)
-   `fix(visor)`: quiet rpc_bridge accept-loop log spam during shutdown  [#2840](https://github.com/skycoin/skywire/pull/2840)
-   `fix(hypervisor)`: refresh summary cache from RPC goroutine, not select arm  [#2842](https://github.com/skycoin/skywire/pull/2842)

### Hypervisor UI tree summary + node-list

-   `fix(hypervisor)`: preserve Hostname + dual-list managed sub-hypervisors  [#2808](https://github.com/skycoin/skywire/pull/2808)
-   `fix(hypervisor)`: bridge Hostname from summaryCache for cross-version sub-sections  [#2809](https://github.com/skycoin/skywire/pull/2809)
-   `fix(hypervisor)`: no ghost rows on failed Summary + remove data race  [#2817](https://github.com/skycoin/skywire/pull/2817)
-   `feat(hypervisor-ui)`: sortable columns + drag-drop reorder + version-history chart + standalone-mode docs  [#2850](https://github.com/skycoin/skywire/pull/2850)

### Remote RPC / `--via` flag

-   `feat(cli)`: unify --rpc with skynet://<pk> + delete tp-rpc subcommand  [#2811](https://github.com/skycoin/skywire/pull/2811)
-   `feat(visor,cli)`: dmsg-direct visor-RPC listener + --rpc dmsg://<pk>  [#2812](https://github.com/skycoin/skywire/pull/2812)
-   `feat(visor,cli)`: dmsg-bridge for --rpc dmsg://<pk> (no separate CLI keypair needed)  [#2813](https://github.com/skycoin/skywire/pull/2813)
-   `feat(visor,cli)`: fold dmsg bridge into RPC port + --via <scheme>://<pk>  [#2814](https://github.com/skycoin/skywire/pull/2814)
-   `feat(visor,cli)`: unify dmsg + skynet bridges; --via skynet:// works end-to-end  [#2815](https://github.com/skycoin/skywire/pull/2815)
-   `fix(cli)`: parse :port from --via URL  [#2816](https://github.com/skycoin/skywire/pull/2816)

### dmsgpty `--scheme` + skynet leg fixes

-   `feat(dmsgpty)`: add `--scheme dmsg|skynet` flag to bypass MultiDialer chain  [#2841](https://github.com/skycoin/skywire/pull/2841)
-   `fix(visor)`: dmsgpty skynet listener never started due to init ordering  [#2846](https://github.com/skycoin/skywire/pull/2846)
-   `fix(visor)`: dmsgpty skynet listener silently rejects route-group conns  [#2847](https://github.com/skycoin/skywire/pull/2847)

### dmsg-discovery + services-as-direct-clients

-   `fix(cmdutil)`: BootstrapDmsg uses HTTP discovery for client entry lookups  [#2823](https://github.com/skycoin/skywire/pull/2823)
-   `fix(dmsgclient)`: stop services self-publishing via fallback wrapper  [#2845](https://github.com/skycoin/skywire/pull/2845)
-   `fix(dmsg/dmsgfirst)`: use direct-client-backed dmsg.Client for primary path  [#2848](https://github.com/skycoin/skywire/pull/2848)
-   `fix(deployment)`: correct embedded keyring port for `70.121.13.123` dmsg server (`9082`→`9083`)  [#2849](https://github.com/skycoin/skywire/pull/2849)

### Install-page WASM generator (TinyGo-clean)

The install-page command generator (`skywire-bin` deb/arch repo's `index.html`) is now a TinyGo-compiled WASM bundle (1.85 MB; ~75% smaller than the Go-WASM variant it replaced). The refactor purged `net/http` and `encoding/json` from the visorconfig + autoconfigcmd + deployment build graph so TinyGo's stdlib can link it.

-   `feat(skywireconfig)`: autoconfigcmd factory + keypair, WASM-clean  [#2826](https://github.com/skycoin/skywire/pull/2826)
-   `feat(autoconfigcmd)`: operator-helpful flag descriptions  [#2827](https://github.com/skycoin/skywire/pull/2827)
-   `feat(netutil,skyenv)`: js/wasm build-tag stubs  [#2828](https://github.com/skycoin/skywire/pull/2828)
-   `feat(visorconfig)`: extract schema leaf packages — V1 now WASM-clean  [#2829](https://github.com/skycoin/skywire/pull/2829)
-   `feat(skywireconfig)`: genvisor — WASM-clean visor config generator  [#2830](https://github.com/skycoin/skywire/pull/2830)
-   `feat(autoconfig)`: config-gen parity — every SKYENV variable now exposed  [#2832](https://github.com/skycoin/skywire/pull/2832)
-   `refactor(visorconfig,dmsg/disc)`: drop net/http from WASM build graph  [#2834](https://github.com/skycoin/skywire/pull/2834)
-   `refactor(deployment,visorconfig,genvisor)`: split json paths for WASM  [#2835](https://github.com/skycoin/skywire/pull/2835)
-   `refactor`: purge encoding/json from WASM build graph — TinyGo unlocked  [#2836](https://github.com/skycoin/skywire/pull/2836)
-   `feat(genvisor)`: hand-rolled streaming JSON serializer for TinyGo  [#2837](https://github.com/skycoin/skywire/pull/2837)
-   `feat(autoconfigcmd)`: expose config-gen defaults via EnvMapping  [#2838](https://github.com/skycoin/skywire/pull/2838)

### Third-party / metrics hygiene

-   `fix(metrics)`: internalize VictoriaMetrics/metrics + gotop to silence PSI log  [#2822](https://github.com/skycoin/skywire/pull/2822)
-   `fix(third_party)`: lint-clean VictoriaMetrics/metrics + xxxserxxx/gotop trees  [#2825](https://github.com/skycoin/skywire/pull/2825)
-   `fix(third_party)`: silence windows gosec G103 inside `Call()` args  [#2833](https://github.com/skycoin/skywire/pull/2833)

### Release ops + CI

-   `chore(release)`: auto-publish draft when all artifacts upload  [#2819](https://github.com/skycoin/skywire/pull/2819)
-   `chore`: update deps + v1.3.57 changelog  [#2820](https://github.com/skycoin/skywire/pull/2820)
-   `chore`: fix CI gofmt failures + serialize make check via flock  [#2831](https://github.com/skycoin/skywire/pull/2831)
-   `ci`: enable Windows arm64 MSI build  [#2839](https://github.com/skycoin/skywire/pull/2839)

## 1.3.56

Patch release. Three PRs on top of v1.3.55.

The big-picture pieces: hypervisor↔visor data-plane now upgrades to a fast p2p transport (stcpr / sudph) automatically once the dmsg session is up; RPC + skypty dial through skynet when a route exists, with dmsg as the bootstrap + fallback. The skypty UI banner is rebranded (skypty-ui, dropped the box that overflowed on the 66-char PK lines), DMSGPTYTERM=1 export is gone — operators wanting "install without auto-restart from inside a pty session" use `NOAUTOCONFIG=true` instead, which the Go autoconfig already honors. The Windows MSI and macOS .pkg installers finally match the deb / arch package post_install pattern from #2796: SK preserved across upgrades (the `-b` flag was rejected by current binaries since #2536's BESTPROTO cleanup, which silently broke the `-r` retain-keys path), and a persistent operator-knobs env file (`%ProgramData%\Skywire\skywire.conf` on Windows, `~/Library/Application Support/Skywire/skywire.conf` on macOS) generated on first install only and untouched on every upgrade.

### Multihop + multiplexed routing

-   `feat(visor)`: auto-upgrade visor↔hypervisor to skynet for RPC + skypty  [#2802](https://github.com/skycoin/skywire/pull/2802)

### dmsg utility belt

-   `feat(dmsgpty/ui)`: skypty-ui rebrand + drop box overflow + drop DMSGPTYTERM  [#2801](https://github.com/skycoin/skywire/pull/2801)

### Packaging + config UX

-   `fix(installers)`: preserve SK + add skywire.conf env file on win + mac  [#2803](https://github.com/skycoin/skywire/pull/2803)

## 1.3.55

Patch release. Two PRs on top of v1.3.54.

### Multihop + multiplexed routing

-   `feat(router)`: per-direction MuxRoutes for asymmetric route counts  [#2797](https://github.com/skycoin/skywire/pull/2797)

### Packaging + config UX

-   `fix(autoconfig,cli/config/gen)`: quiet gen subprocess + clean flag descs  [#2798](https://github.com/skycoin/skywire/pull/2798)

Companion AUR change (skycoin/aur, not in this repo): try-restart loop in deb postinst + arch post_upgrade so every package-shipped service that's currently active picks up the new binary on `apt upgrade` / `pacman -Syu` without operator intervention.

## 1.3.54

Multihop + multiplexed routing is operator-facing now. A new
`mux-bw` gRPC streaming RPC + bubbletea TUI measure aggregate
throughput across N parallel routes through ≥K hops, with an
idle-baseline + probe-rtt phase that reads latency, jitter, and
queueing-delay from one invocation. The accumulated multihop /
multiplex fixes — `--min-hops` plumbed through every dial path,
N-hop BFS in local route calc, per-route teardown decoupling,
DisjointMux + ExcludeIntermediatePKs on DialOptions,
MuxRouteFailure events surfacing pump-phase errors,
retry-on-bad-intermediate, the `ping.mu` critical-section
narrow, and the PingOnceWithEcho deadline-race close — turn
what was an undocumented research surface into a deterministic
operator tool. Skynet client + server gain `--routes N`
`--min-hops K` end-to-end, the per-conn accept loop no longer
exits after one conn, and the visor's CXO subscription manager
now backs `GetAllTransports` for all internal consumers so route
calc reads from the local snapshot instead of HTTP on every
dial.

CXO is reliable end-to-end after a multi-PR foundation pass.
The big one: `HasPrefix` was dropping every match when the
prefix ended in `/` — affected all 5 of the visor's CXO
subscription manager feeds network-wide (#2769). Publishers
pre-warm from their store at startup so subscribers connecting
in the post-restart gap get a real Root instead of a 10s timeout
(#2771). Duplicate per-feed subscribers that raced for the same
DMSG port are gone (#2768). Subscriber reconnect watchdog +
Conn idle watchdog close the silent-stale-conn cases. Bbolt
corruption auto-recovers on stats / idxdb / cxds opens. The
publisher hydrates from the on-disk container so restart no
longer truncates the published Root. The CXO-aware
`transport.DiscoveryClient` wrapper (#2773) routes CLI / hvui /
autoconnect / route calc through the in-memory snapshot — the
familiar "CXO miss" log finally goes quiet.

Skychat group chat federation. Members publish to their own
feeds (D1 distributed publisher) instead of relaying through the
owner. Signed roster + admin mutations gossip across the mesh,
with admin-side verbatim republish for non-admin subscribers; the
previous admin-mirror-feed shape is gone. Per-(group, peer)
reconnect backoff with eviction-clean state replaces the old
single-flag liveness signal, and per-peer `last_inbound` is
surfaced in `GroupInfo` + `cli skychat group info`. Persistent
group history at the visor layer survives restarts; history
replay extends past the in-memory inbox ring.
Manager.Resume reconnects subscribers on chat-app restart, and
pairs gain a CLI surface. The new noise-TCP direct transport +
`--standalone` mode let operators run chat-apps outside the
visor for inter-agent / inter-host coordination.

UDP-over-skynet ships as a faithful overlay datagram. New
`DatagramPacket` wire type with per-datagram AEAD,
`DatagramRouteGroup` + Group interface, `forwarded_ports.udp` in
the visor, and `DialPacket` / `DatagramPacketConn` API on appnet.
The standalone `skyudp-bridge` is a Plan-B UDP→dmsg shim for
stacks that need datagram semantics without the routing layer.

IPv6 lands as dual-stack. RemoteAddrV6 + AddressV6 schema,
dmsg-server v6 advertisement in the discovery entry, visor binds
AR over v6 HTTP, declared PublicIPv6 in bind payload, Happy
Eyeballs dialer in transport/network, ARSelfEntry surfaces v6
with CLI render, CI dual-stack dmsg e2e lane.

dmsg utility belt. New `ssh` + `sshd` commands provide
OpenSSH-equivalent surface over skywire identity; dmsgpty grows
direct-TCP entry with XK-noise handshake, MultiDialer (skynet
listener + dialer in chain), and a TCP-only / shared-SK
socket-activation mode for sshd-style daemons. `cli dmsg cat`
+ `cli util nc` stream piping; `cli dmsg iperf` bulk throughput
+ `--rtt`; `cli dmsg probe --ports` multi-port sweep; `cli util
foreach` templated-command parallelism; `cli route trace`
per-hop latency; `cli visor doctor` health rollup;
`cli visor whois` PK rollup over TPD + UT + SD; `dmsgscp` p2p
file transfer (default-on, no size cap, dmsg + skynet).

Hypervisor UI gets nested-visor tree management. Backend nested-
tree API + `cli hv tree`; node service + node-list consume tree
sections; hypervisor list under PK with live online-time + ping
/ NAT tooltips. Tab highlight + terminal iframe + DMSG /
Reachability tab cleanup, local-hypervisor ★ restored,
transient-offline visors stay visible.

Skymail bridge — SMTP-aware sender-side bridge for skywire email,
accepting both host-prefixed and bare-PK address shapes; operator
recipe + Postfix overrides documented.

Packaging + config UX. Persistent `skywire.conf` edits across
autoconfig runs; native-Go env-file parser + Windows phase 1;
LANDMSG defaults to ISHYPERVISOR + LANDMSGPORT knob; lan-dmsg
keypair generated at config-gen time; `--pair-enable` defaults
on for skychat with SKYCHATPAIR override; kebab-case
`--dmsg-port` + confbs tag fixes; `applyFlagsToConf` now handles
bash-array flags (HYPERVISORPKS et al, #2777) — the operator
regression where `config gen -qpij <pk>` emitted
`#HYPERVISORPKS=('')` is closed.

Docs site: MkDocs Material at
[skycoin.github.io/skywire](https://skycoin.github.io/skywire);
cobra-driven `docs/skywire/` tree with hidden `skywire doc`
generator. Specs trimmed of non-normative content. Skychat
group gossip RFC committed. README restructured for
"Skywire is encrypted UDP & TCP" lead.

Performance. dmsg noise static-static DH cache; hex-encode
allocation halving; per-transport tickers centralized in
Manager; bbolt batch coalescing on publisher tree-walk + stats
sample writes; AcceptBufferSize 20→256 for deployment burst
absorption.

### Multihop + multiplexed routing

-   `feat(skynet)`: --routes flag for parallel mux-route dialing  [#2727](https://github.com/skycoin/skywire/pull/2727)
-   `feat(visor)`: server-side gRPC streaming ping-tree RPC  [#2732](https://github.com/skycoin/skywire/pull/2732)
-   `feat(visor/ping-tree)`: use_transport_latency fast path at level-1  [#2733](https://github.com/skycoin/skywire/pull/2733)
-   `feat(cli/ping-tree)`: rewire TUI onto server-side gRPC streaming  [#2736](https://github.com/skycoin/skywire/pull/2736)
-   `feat(visor)`: StreamMuxBandwidth gRPC RPC + cli mux-bw  [#2737](https://github.com/skycoin/skywire/pull/2737)
-   `feat(cli/ping-mux-bw)`: bubbletea TUI dashboard for StreamMuxBandwidth  [#2739](https://github.com/skycoin/skywire/pull/2739)
-   `feat(visor)`: per-route teardown — decouple StopPing from PK-only  [#2745](https://github.com/skycoin/skywire/pull/2745)
-   `feat(router)`: DisjointMux + ExcludeIntermediatePKs in DialOptions  [#2746](https://github.com/skycoin/skywire/pull/2746)
-   `feat(cli/ping)`: human-readable defaults + built-in aggregation for tree-stream + mux-bw  [#2747](https://github.com/skycoin/skywire/pull/2747)
-   `feat(visor/cli)`: mux-bw idle-baseline phase + built-in queueing-delay output  [#2748](https://github.com/skycoin/skywire/pull/2748)
-   `fix(visor/router)`: plumb mux-bw --min-hops through DialPing → router  [#2749](https://github.com/skycoin/skywire/pull/2749)
-   `fix(router)`: blame intermediate not dst when id_reservation fails on a hop  [#2750](https://github.com/skycoin/skywire/pull/2750)
-   `fix(appnet)`: tryDirectPingDial shortcut bypassed MinHops constraint  [#2751](https://github.com/skycoin/skywire/pull/2751)
-   `feat(visor)`: honor caller SetupTimeout in DialPing  [#2752](https://github.com/skycoin/skywire/pull/2752)
-   `fix(router)`: calculateLocalRoutes must honor MinHops > 2  [#2753](https://github.com/skycoin/skywire/pull/2753)
-   `fix(rpcgrpc)`: mux-bw probe-after-pump race + per-level LevelDone counters  [#2754](https://github.com/skycoin/skywire/pull/2754)
-   `feat(router)`: N-hop BFS in calculateLocalRoutes — deterministic local routing  [#2755](https://github.com/skycoin/skywire/pull/2755)
-   `feat(rpcgrpc/mux-bw)`: surface pump-phase route failures as MuxRouteFailure events  [#2756](https://github.com/skycoin/skywire/pull/2756)
-   `fix(visor)`: PingOnceWithEcho adapter must forward RouteIndex + MinHops  [#2757](https://github.com/skycoin/skywire/pull/2757)
-   `fix(router)`: local-route BFS visited-set was shadowing longer paths  [#2758](https://github.com/skycoin/skywire/pull/2758)
-   `fix(skynet/client)`: accept loop with per-conn remote dial + half-close drain  [#2759](https://github.com/skycoin/skywire/pull/2759)
-   `fix(cli/skynet,visor)`: list custom-named skynet apps + render AppStatusStarting  [#2760](https://github.com/skycoin/skywire/pull/2760)
-   `fix(visor/ping)`: narrow ping.mu critical section to map lookup  [#2761](https://github.com/skycoin/skywire/pull/2761)
-   `fix(visor)`: write deadlines on PingOnceWithEcho + probe RouteIndex passthrough  [#2762](https://github.com/skycoin/skywire/pull/2762)
-   `fix(visor/ping)`: cap PingOnceWithEcho at 10s + emit failure on bytes=0 exit  [#2764](https://github.com/skycoin/skywire/pull/2764)
-   `fix(appnet)`: gate direct-dial shortcut on MinHops/MuxRoutes opts  [#2765](https://github.com/skycoin/skywire/pull/2765)
-   `feat(skynet-client)`: plumb --min-hops flag through app dial chain  [#2766](https://github.com/skycoin/skywire/pull/2766)
-   `fix(cli/skynet)`: forward --min-hops flag through start command  [#2770](https://github.com/skycoin/skywire/pull/2770)
-   `feat(visor)`: CXO-aware transport.DiscoveryClient — local route calc reads CXO before HTTP  [#2773](https://github.com/skycoin/skywire/pull/2773)
-   `fix(router)`: retry with different intermediate on id_reservation dial failure  [#2774](https://github.com/skycoin/skywire/pull/2774)
-   `fix(router)`: plumb opts.MinHops + AppName into establishMuxRoutes aux muxOpts  [#2776](https://github.com/skycoin/skywire/pull/2776)
-   `fix(skynet-srv)`: half-close forwardRawTCP before close — drains buffered bytes  [#2735](https://github.com/skycoin/skywire/pull/2735)
-   `fix(router)`: handleDataPacket panic on send-to-closed-readCh during remote close  [#2730](https://github.com/skycoin/skywire/pull/2730)
-   `feat(router)`: rank route candidates by measured per-hop latency  [#2782](https://github.com/skycoin/skywire/pull/2782)
-   `feat(router)`: unpair forward/reverse candidate selection  [#2785](https://github.com/skycoin/skywire/pull/2785)
-   `feat(router)`: per-direction MinHops for asymmetric routing  [#2792](https://github.com/skycoin/skywire/pull/2792)

### CXO reliability

-   `feat(cli,visor,tpd)`: close CXO subscriber gap (SD services, TPD all-transports) + bbolt cache fallback  [#2491](https://github.com/skycoin/skywire/pull/2491)
-   `feat(sd,tpd,dmsgd)`: make CXO publishers always-on (drop --cxo flag)  [#2494](https://github.com/skycoin/skywire/pull/2494)
-   `fix(sd,dmsgd)`: decouple HTTP register path from CXO publisher mutex  [#2495](https://github.com/skycoin/skywire/pull/2495)
-   `fix(cxo/node)`: plug Connection read/write-loop goroutine leak  [#2497](https://github.com/skycoin/skywire/pull/2497)
-   `fix(cxo/data/idxdb)`: implement real in-memory IdxDB  [#2499](https://github.com/skycoin/skywire/pull/2499)
-   `fix(cxo/node)`: bounded sendMsg + fan-out broadcastRoot  [#2538](https://github.com/skycoin/skywire/pull/2538)
-   `fix(cxo/node)`: evict stale Conn on peer rejoin instead of rejecting  [#2625](https://github.com/skycoin/skywire/pull/2625)
-   `fix(cxo/node)`: handleSub always pushes current Root on duplicate Subscribe  [#2647](https://github.com/skycoin/skywire/pull/2647)
-   `fix(cxo/treestore)`: subscriber.handleRootFilled filters by feedPK  [#2648](https://github.com/skycoin/skywire/pull/2648)
-   `feat(cxo/treestore)`: subscriber consumes OnFillingBreaks (visibility)  [#2649](https://github.com/skycoin/skywire/pull/2649)
-   `fix(cxo/treestore)`: hydrate Publisher in-memory tree from container on restart  [#2651](https://github.com/skycoin/skywire/pull/2651)
-   `fix(cxo)`: thread context.Context through Subscriber.Connect → dmsg ConnectPK  [#2652](https://github.com/skycoin/skywire/pull/2652)
-   `fix(cxo/node)`: Conn idle watchdog — close half-dead conns within ~2min  [#2657](https://github.com/skycoin/skywire/pull/2657)
-   `fix(cxo/cxds)`: memoryCXDS.Del actually removes the map entry  [#2676](https://github.com/skycoin/skywire/pull/2676)
-   `fix(cxo)`: recover from missing seqs in RemoveRootObjects + periodic forced sweep  [#2677](https://github.com/skycoin/skywire/pull/2677)
-   `fix(cxo/node)`: guard Conn init signaling against double-close panic  [#2681](https://github.com/skycoin/skywire/pull/2681)
-   `fix(cxo/skyobject)`: LastRoot + rootByHash nil-deref on missing CXDS bytes  [#2683](https://github.com/skycoin/skywire/pull/2683)
-   `feat(cxo/treestore)`: ConnectAndWaitForRoot — atomic-subscribe (phase C foundation)  [#2690](https://github.com/skycoin/skywire/pull/2690)
-   `fix(cxo/node)`: synchronize OnRootFilled/OnFillingBreaks callback access  [#2703](https://github.com/skycoin/skywire/pull/2703)
-   `fix(cxo/skyobject)`: DelRoot walk best-effort on ErrNotFound (TPD leak fix)  [#2704](https://github.com/skycoin/skywire/pull/2704)
-   `fix(dmsg-discovery)`: drop tombstone writes from clients-by-server CXO publisher  [#2705](https://github.com/skycoin/skywire/pull/2705)
-   `fix(cxo/treestore)`: subscriber reconnect watchdog  [#2714](https://github.com/skycoin/skywire/pull/2714)
-   `fix(dmsg-discovery)`: skip CXO republish on heartbeat-only entry updates  [#2724](https://github.com/skycoin/skywire/pull/2724)
-   `fix(stats)`: auto-recover from bbolt corruption at startup  [#2728](https://github.com/skycoin/skywire/pull/2728)
-   `fix(cxo)`: auto-recover from bbolt corruption in idxdb + cxds opens  [#2729](https://github.com/skycoin/skywire/pull/2729)
-   `fix(visor)`: remove duplicate standalone CXO subscribers for tpd-metrics/uptime  [#2768](https://github.com/skycoin/skywire/pull/2768)
-   `fix(cxo)`: HasPrefix dropped every match for trailing-slash prefixes  [#2769](https://github.com/skycoin/skywire/pull/2769)
-   `fix(sd,dmsgd)`: pre-warm CXO publisher tree from redis at startup  [#2771](https://github.com/skycoin/skywire/pull/2771)
-   `fix(cxo/treestore)`: release publisher mutex around encode + bbolt write  [#2599](https://github.com/skycoin/skywire/pull/2599)

### Skychat group chat federation

-   `fix(skychat)`: /status groups visibility + stale-active demotion + inbox MarkMessage  [#2530](https://github.com/skycoin/skywire/pull/2530)
-   `feat(skychat/group)`: owner heartbeat watchdog for fast stale-sub detection  [#2531](https://github.com/skycoin/skywire/pull/2531)
-   `refactor(skychat/group)`: collapse 4 liveness flags into single lastInboundNs  [#2537](https://github.com/skycoin/skywire/pull/2537)
-   `feat(skychat/group)`: D1 distributed publisher — every member publishes own feed  [#2539](https://github.com/skycoin/skywire/pull/2539)
-   `feat(skychat/group)`: relay-ack for member→owner sends  [#2575](https://github.com/skycoin/skywire/pull/2575)
-   `feat(skychat/group)`: persistent group history at the visor layer  [#2578](https://github.com/skycoin/skywire/pull/2578)
-   `feat(skychat/group)`: federated send — members publish to own feed  [#2580](https://github.com/skycoin/skywire/pull/2580)
-   `feat(skychat)`: admin role + admin-set management (1/3, 2/3, 3/3)  [#2583](https://github.com/skycoin/skywire/pull/2583) [#2584](https://github.com/skycoin/skywire/pull/2584) [#2585](https://github.com/skycoin/skywire/pull/2585)
-   `fix(skychat/group)`: owner-role peerSubs also need reconnect coverage  [#2595](https://github.com/skycoin/skywire/pull/2595)
-   `fix(skychat/group)`: per-peerSub liveness signal so silent peers get reconnected  [#2606](https://github.com/skycoin/skywire/pull/2606)
-   `fix(skychat/group)`: per-(group, peer) reconnect backoff  [#2627](https://github.com/skycoin/skywire/pull/2627)
-   `feat(skychat/group)`: surface per-peer last-inbound in GroupInfo  [#2628](https://github.com/skycoin/skywire/pull/2628)
-   `feat(cli/group)`: human-readable info + peer_last_inbound table  [#2629](https://github.com/skycoin/skywire/pull/2629)
-   `fix(skychat/group)`: replay backlog on streaming reconnect  [#2630](https://github.com/skycoin/skywire/pull/2630)
-   `fix(skychat/group)`: clear per-peer reconnect state on roster eviction  [#2632](https://github.com/skycoin/skywire/pull/2632)
-   `feat(skychat/group)`: mutation types + signing for roster/admin gossip  [#2658](https://github.com/skycoin/skywire/pull/2658)
-   `feat(skychat/group)`: history-replay beyond inbox ring (Path A of #2637)  [#2659](https://github.com/skycoin/skywire/pull/2659)
-   `feat(skychat/group)`: publish signed roster/admin mutations on roster change  [#2674](https://github.com/skycoin/skywire/pull/2674)
-   `refactor(skychat/group)`: drop admin-mirror feeds — full mesh only  [#2682](https://github.com/skycoin/skywire/pull/2682)
-   `feat(skychat/group)`: sign + verify leaf-level Messages  [#2685](https://github.com/skycoin/skywire/pull/2685)
-   `feat(skychat/group)`: admin-side verbatim republish on onUpdate  [#2688](https://github.com/skycoin/skywire/pull/2688)
-   `feat(skychat/group)`: non-admin sub topology — admin-aggregator slice  [#2689](https://github.com/skycoin/skywire/pull/2689)
-   `feat(skychat/group)`: switch all 6 Connect call sites to ConnectAndWaitForRoot  [#2692](https://github.com/skycoin/skywire/pull/2692)
-   `feat(cli/skychat)`: pair subcommands for CXO p2p baseline  [#2699](https://github.com/skycoin/skywire/pull/2699)
-   `fix(skychat/pairing)`: add /pair/inbox HTTP handler (closes #2699 CLI gap)  [#2702](https://github.com/skycoin/skywire/pull/2702)
-   `feat(skychat)`: noise-TCP direct transport — listener, peer dialer, CLI --via  [#2707](https://github.com/skycoin/skywire/pull/2707)
-   `feat(skychat)`: --standalone mode (skip PROC_CONFIG, keep TCP-direct + HTTP control)  [#2708](https://github.com/skycoin/skywire/pull/2708)
-   `fix(skychat/pairing)`: Manager.Resume reconnects subscribers (3-agent pair-RX root cause)  [#2711](https://github.com/skycoin/skywire/pull/2711)
-   `feat(skychat/group)`: server-streaming gRPC for group listen  [#2571](https://github.com/skycoin/skywire/pull/2571)
-   `feat(cli/skychat)`: default --wait=5s for delivery-confirmed send  [#2573](https://github.com/skycoin/skywire/pull/2573)
-   `feat(skychat)`: network fallback — try alternate transport on send failure  [#2574](https://github.com/skycoin/skywire/pull/2574)
-   `feat(skychat/group)`: surface gRPC subscriber drop count + per-layer drop counters  [#2662](https://github.com/skycoin/skywire/pull/2662) [#2664](https://github.com/skycoin/skywire/pull/2664) [#2665](https://github.com/skycoin/skywire/pull/2665)
-   `feat(cli/skychat/group)`: render sub_drop_count / stream_send_count / deliver_count  [#2663](https://github.com/skycoin/skywire/pull/2663) [#2666](https://github.com/skycoin/skywire/pull/2666) [#2669](https://github.com/skycoin/skywire/pull/2669)

### UDP-over-skynet

-   `feat(routing)`: add DatagramPacket wire type (faithful UDP, stage 1)  [#2609](https://github.com/skycoin/skywire/pull/2609)
-   `feat(router)`: DatagramRouteGroup + Group interface (faithful UDP, stage 2)  [#2610](https://github.com/skycoin/skywire/pull/2610)
-   `feat(skyudp-bridge)`: standalone UDP→dmsg bridge — Plan B for UDP-over-skynet  [#2611](https://github.com/skycoin/skywire/pull/2611)
-   `feat(router)`: per-datagram AEAD for DatagramRouteGroup (faithful UDP, stage 3)  [#2612](https://github.com/skycoin/skywire/pull/2612)
-   `feat(visor)`: UDP-over-skynet via forwarded_ports.udp (faithful UDP, stage 4)  [#2613](https://github.com/skycoin/skywire/pull/2613)
-   `feat(appnet)`: DialPacket / DatagramPacketConn API (faithful UDP, stage 5)  [#2614](https://github.com/skycoin/skywire/pull/2614)

### IPv6 dual-stack

-   `feat(ipv6)`: phase 1 schema — RemoteAddrV6 + AddressV6, per-family bind merge  [#2715](https://github.com/skycoin/skywire/pull/2715)
-   `feat(ipv6)`: phase 2a — dmsg-server v6 advertisement in discovery entry  [#2716](https://github.com/skycoin/skywire/pull/2716)
-   `feat(ipv6)`: phase 3 — Happy Eyeballs dialer in transport/network  [#2717](https://github.com/skycoin/skywire/pull/2717)
-   `feat(ci)`: ipv6 phase 5 — dual-stack dmsg e2e lane  [#2718](https://github.com/skycoin/skywire/pull/2718)
-   `feat(ipv6)`: phase 2b — visor binds AR over v6 HTTP to register RemoteAddrV6  [#2719](https://github.com/skycoin/skywire/pull/2719)
-   `feat(ipv6)`: phase 4a — ARSelfEntry exposes RemoteAddrV6 + CLI dual-stack render  [#2721](https://github.com/skycoin/skywire/pull/2721)
-   `feat(ipv6)`: phase 2c — declared PublicIPv6 in bind payload (dmsg-routed AR path)  [#2722](https://github.com/skycoin/skywire/pull/2722)

### dmsg utility belt

-   `feat(cli)`: add dmsgcat + util nc — stream piping over dmsg/skynet/TCP  [#2544](https://github.com/skycoin/skywire/pull/2544)
-   `feat(cli/visor)`: doctor — one-shot health rollup with verdict + exit code  [#2545](https://github.com/skycoin/skywire/pull/2545)
-   `feat(cli/dmsg)`: iperf — bulk throughput measurement over dmsg streams  [#2546](https://github.com/skycoin/skywire/pull/2546)
-   `feat(cli/dmsg)`: iperf --rtt + listener --echo for latency + jitter  [#2550](https://github.com/skycoin/skywire/pull/2550)
-   `feat(cli/route)`: trace — per-hop latency printout for a route to a peer  [#2553](https://github.com/skycoin/skywire/pull/2553)
-   `feat(cli/dmsg)`: probe --ports <list> for multi-port sweep  [#2554](https://github.com/skycoin/skywire/pull/2554)
-   `feat(cli/util)`: foreach — run templated command against each target in parallel  [#2555](https://github.com/skycoin/skywire/pull/2555)
-   `feat(cli/visor)`: whois — single-PK rollup over TPD + UT + SD  [#2556](https://github.com/skycoin/skywire/pull/2556)
-   `feat(dmsgpty)`: direct-TCP entry point with XK-noise handshake (server-side)  [#2559](https://github.com/skycoin/skywire/pull/2559)
-   `feat(cli/dmsg/pty)`: --via tcp://<pk>@<host:port> direct-TCP path  [#2560](https://github.com/skycoin/skywire/pull/2560)
-   `feat(dmsgpty)`: TCPListen on standalone dmsgpty-host + --via-visor on cli  [#2561](https://github.com/skycoin/skywire/pull/2561)
-   `feat(dmsgpty-host)`: TCP-only / shared-SK / socket-activation for sshd-style daemon  [#2569](https://github.com/skycoin/skywire/pull/2569)
-   `feat(cli)`: ssh + sshd commands — OpenSSH-equivalent surface over skywire identity  [#2572](https://github.com/skycoin/skywire/pull/2572)
-   `refactor(dmsgpty)`: StreamDialer interface for outbound proxy dial (phase 1, 2, 3)  [#2671](https://github.com/skycoin/skywire/pull/2671) [#2672](https://github.com/skycoin/skywire/pull/2672) [#2675](https://github.com/skycoin/skywire/pull/2675)
-   `feat(dmsgscp)`: scp-over-dmsg utility, port 23, whitelist-gated, dmsg + skynet  [#2524](https://github.com/skycoin/skywire/pull/2524) [#2527](https://github.com/skycoin/skywire/pull/2527) [#2542](https://github.com/skycoin/skywire/pull/2542)

### Hypervisor UI

-   `feat(hv)`: nested-visor tree API + cli hv tree command (backend half)  [#2633](https://github.com/skycoin/skywire/pull/2633)
-   `feat(hvui)`: node service getNodesTree() + NodeSection type (frontend service half)  [#2641](https://github.com/skycoin/skywire/pull/2641)
-   `feat(hvui)`: node-list consumes tree sections + header shows local hypervisor PK  [#2642](https://github.com/skycoin/skywire/pull/2642)
-   `fix(hv-ui)`: tab highlight, terminal iframe, hypervisor list on Info  [#2740](https://github.com/skycoin/skywire/pull/2740)
-   `chore(ui)`: npmrc + refreshed lockfile so make build-ui works on npm 11  [#2741](https://github.com/skycoin/skywire/pull/2741)
-   `fix(hv)`: restore local-hypervisor ★ + keep transient-offline visors visible  [#2742](https://github.com/skycoin/skywire/pull/2742)
-   `fix(hv-ui)`: drop DMSG + Reachability tabs; terminal tab now works  [#2743](https://github.com/skycoin/skywire/pull/2743)
-   `feat(hv-ui)`: hypervisor list under PK, live online-time, ping/NAT tooltips  [#2744](https://github.com/skycoin/skywire/pull/2744)
-   `feat(visor/hv)`: runtime management — rm/--all/passwd CLI, hypervisor section on visor info  [#2779](https://github.com/skycoin/skywire/pull/2779)
-   `fix(hv)`: dedup hypervisor visor rows in tree-summary local section + surface Hostname  [#2780](https://github.com/skycoin/skywire/pull/2780)
-   `feat(hvui)`: default node label to hostname; surface hostname on node info tab  [#2781](https://github.com/skycoin/skywire/pull/2781)
-   `feat(hvui/node-list)`: render per-sub-hypervisor sections below the main table  [#2783](https://github.com/skycoin/skywire/pull/2783)
-   `fix(hv)`: propagate ServicesHealth through HVVisorEntry → sub-section status dots  [#2784](https://github.com/skycoin/skywire/pull/2784)
-   `fix(hvui/node-list)`: sub-section tables now use the full-fat main-table styling  [#2786](https://github.com/skycoin/skywire/pull/2786)
-   `feat(hv)`: proxy visor RPC through a sub-hypervisor for drill-down access  [#2787](https://github.com/skycoin/skywire/pull/2787)
-   `fix(hv)`: default sub-section Online visors to healthy when ServicesHealth empty  [#2788](https://github.com/skycoin/skywire/pull/2788)
-   `fix(hv)`: surface sub-section visor transports column  [#2789](https://github.com/skycoin/skywire/pull/2789)
-   `fix(hvui)`: parse transports in sub-section tree response  [#2790](https://github.com/skycoin/skywire/pull/2790)
-   `fix(hv)`: sub-section ★ icon + clean placeholder transport rows  [#2791](https://github.com/skycoin/skywire/pull/2791)
-   `fix(hv)`: only star sub-section row matching the section's hypervisor PK  [#2793](https://github.com/skycoin/skywire/pull/2793)
-   `fix(hvui)`: main flat list = section 0 only; visors per their own hypervisor  [#2794](https://github.com/skycoin/skywire/pull/2794)
-   `fix(hv)`: sub-section transports fallback via HVVisorSummary  [#2795](https://github.com/skycoin/skywire/pull/2795)

### Skymail bridge

-   `feat(skymail-bridge)`: SMTP-aware sender-side bridge for skywire email  [#2598](https://github.com/skycoin/skywire/pull/2598)
-   `feat(skymail-bridge)`: mode b accepts both host-prefixed and bare-PK address shapes  [#2605](https://github.com/skycoin/skywire/pull/2605)
-   `docs(guides)`: skymail-bridge operator recipe + Postfix overrides  [#2604](https://github.com/skycoin/skywire/pull/2604)

### Packaging + config UX

-   `fix(autoconfig)`: propagate resolved PKGENV/USRENV mode to the gen subprocess  [#2490](https://github.com/skycoin/skywire/pull/2490)
-   `fix(cli)`: operator-UX bundle — config template + WARN, router/proxy clarity  [#2501](https://github.com/skycoin/skywire/pull/2501)
-   `feat(autoconfig,skyenv)`: native-Go env-file parser; Windows-aware autoconfig (phase 1)  [#2503](https://github.com/skycoin/skywire/pull/2503)
-   `feat(cli/config)`: LANDMSG defaults to ISHYPERVISOR + add LANDMSGPORT env knob  [#2535](https://github.com/skycoin/skywire/pull/2535)
-   `feat(skywire-autoconfig)`: persistent skywire.conf edits + cleanup vestigial knobs  [#2536](https://github.com/skycoin/skywire/pull/2536)
-   `fix(cli/config)`: generate lan-dmsg-server keypair at gen time (unblocks CI)  [#2541](https://github.com/skycoin/skywire/pull/2541)
-   `fix(cli/config/gen)`: default --pair-enable on skychat so groups work out of the box  [#2594](https://github.com/skycoin/skywire/pull/2594)
-   `fix(cli/config/gen)`: SKYCHATPAIR env knob for --pair-enable + conf template entry  [#2596](https://github.com/skycoin/skywire/pull/2596)
-   `fix(svc)`: kebab-case --dmsg-port + fix garbled --config + confbs tag  [#2602](https://github.com/skycoin/skywire/pull/2602)
-   `chore(deps)`: bump vis-data and vis-network in /pkg/tpviz/ui  [#2767](https://github.com/skycoin/skywire/pull/2767)
-   `chore(transport)`: remove unused tpdCache field + Set/GetTPDCache methods  [#2772](https://github.com/skycoin/skywire/pull/2772)
-   `fix(cli/config)`: applyFlagsToConf handles bash-array flags (HYPERVISORPKS et al)  [#2777](https://github.com/skycoin/skywire/pull/2777)

### Docs

-   `docs(specs)`: strip non-normative content from specifications  [#2579](https://github.com/skycoin/skywire/pull/2579)
-   `docs(specs)`: trim impl detail from TPD + Transport Management  [#2581](https://github.com/skycoin/skywire/pull/2581)
-   `docs`: cobra-driven docs/skywire/ tree + hidden `skywire doc` generator  [#2582](https://github.com/skycoin/skywire/pull/2582)
-   `docs(skywire/doc)`: code-fence ASCII-art Longs + scrub raw DBIVersion  [#2586](https://github.com/skycoin/skywire/pull/2586)
-   `docs(skywire/doc)`: strip ANSI escape sequences from generated Longs  [#2588](https://github.com/skycoin/skywire/pull/2588)
-   `docs(skywire/doc)`: expand --capture allowlist + commit captured samples  [#2589](https://github.com/skycoin/skywire/pull/2589)
-   `feat(docs)`: MkDocs Material site at skycoin.github.io/skywire  [#2590](https://github.com/skycoin/skywire/pull/2590)
-   `fix(docs)`: drop --strict from CI so deploy doesn't abort on dangling specs links  [#2591](https://github.com/skycoin/skywire/pull/2591)
-   `docs(skywire/doc)`: include flags marked Hidden in the rendered output  [#2593](https://github.com/skycoin/skywire/pull/2593)
-   `docs(readme)`: lead with "Skywire is encrypted UDP & TCP"  [#2624](https://github.com/skycoin/skywire/pull/2624)
-   `docs`: RFC for cross-visor admin/roster gossip  [#2656](https://github.com/skycoin/skywire/pull/2656)
-   `docs(readme)`: counter-points + restructure Major features  [#2701](https://github.com/skycoin/skywire/pull/2701)

### Performance

-   `perf(cipher,dmsgd)`: halve hex-encode allocs on MarshalText + dedupe in hot map  [#2500](https://github.com/skycoin/skywire/pull/2500)
-   `perf(dmsg/noise)`: cache static-static DH results for repeat peers  [#2507](https://github.com/skycoin/skywire/pull/2507)
-   `perf(dmsg)`: bump AcceptBufferSize 20 → 256 to absorb deployment burst  [#2552](https://github.com/skycoin/skywire/pull/2552)
-   `perf(stats/cxo)`: batch sample writes into single Publisher mutex acquire  [#2564](https://github.com/skycoin/skywire/pull/2564)
-   `perf(cxo)`: coalesce publisher tree-walk writes into one bbolt tx  [#2566](https://github.com/skycoin/skywire/pull/2566)
-   `perf(transport)`: centralize per-transport tickers in Manager  [#2567](https://github.com/skycoin/skywire/pull/2567)

### CI

-   `ci(deploy)`: serialize Deploy runs per-branch via concurrency group  [#2502](https://github.com/skycoin/skywire/pull/2502)
-   `feat(ci)`: mux-route-probe.sh — 3-visor multiplexed-route test runner  [#2723](https://github.com/skycoin/skywire/pull/2723)
-   `feat(ci/mux-runner)`: endpoint-a/-b, intermediate-pool, avoid-direct flags  [#2725](https://github.com/skycoin/skywire/pull/2725)
-   `feat(ci)`: mux-probe-assert — Go harness for mux-route-probe.sh tally  [#2726](https://github.com/skycoin/skywire/pull/2726)

## 1.3.53

Skychat group chat — feature complete. A three-agent live
coordination session ran against the new group machinery and
surfaced every reliability gap: stuck CXO subscribers after a
chat-app restart now self-heal via a 30s background reconnect
loop; the owner's relay isMember check now reads the live
allowlist rather than an Open-time snapshot; member sends are
visible in the sender's own inbox via publisher self-echo;
owner-side `MarkMessage` fires on every observed message, not
just outbound sends; member CXO node dial port no longer drifts
by one and breaks join; group listen auto-reconnects across visor
restarts without log spam; and the in-process Manager replays
the last 100 messages per group on Resume so a freshly restarted
operator sees recent context instead of an empty feed. Skychat
send/receipt semantics gain a `--wait` peer-receipt ack via
chat-msg/chat-ack envelopes (#2511) and the `/status` endpoint
now reports send-failure / outbound-retry / sse-drop counters
plus per-group health (last_message_at, lag_seconds,
subscriber_alive). messageHandler retries once through a fresh
dial when the cached `framedConn` to a peer is stale.

dmsg utility belt. New `dmsg pty exec` for non-interactive
remote-command execution — one-shot, returns
stdout/stderr/exit. New `dmsgscp` (port 23, on by default —
whitelist-gated identically to dmsgpty, dual-listens on dmsg +
skynet) for peer-to-peer file transfer using the OpenSSH SCP
framing.

skychat operator UX. New unified bubbletea TUI (picker → DM →
group) at `skywire cli skychat chat`; PK alias addressbook with
reverse-resolve on listen/history output; `skywire cli skychat
history` and `status` subcommands; `listen` and `group listen`
default to single-line-per-message output (escapes `\n`) so log
aggregators stop fragmenting multi-line messages — `--raw` for
human reading, `--json` for NDJSON tool ingestion.

Performance. dmsg noise handshake caches static-static DH
results for repeat peers (#2507) — meaningful for visors that
hold long-term peer sets.

### Group chat

-   `feat(skychat/group)`: member-side send via dmsg relay  [#2506](https://github.com/skycoin/skywire/pull/2506)
-   `fix(skychat/group)`: member CXO node dial-port off-by-one breaks join  [#2513](https://github.com/skycoin/skywire/pull/2513)
-   `fix(skychat-cli)`: group listen auto-reconnect on visor restart  [#2514](https://github.com/skycoin/skywire/pull/2514)
-   `fix(skychat/group)`: owner sees own group sends in local inbox  [#2515](https://github.com/skycoin/skywire/pull/2515)
-   `fix(skychat-cli)`: suppress reconnect-spam on group listen retries  [#2516](https://github.com/skycoin/skywire/pull/2516)
-   `feat(skychat/group)`: replay last 100 messages on visor restart  [#2520](https://github.com/skycoin/skywire/pull/2520)
-   `fix(skychat/group)`: isMember reads live allowlist, not snapshot  [#2522](https://github.com/skycoin/skywire/pull/2522)
-   `fix+resilience(skychat)`: MarkMessage + TUI/GUI polish + cli single-line listen + stale-conn retry + group auto-reconnect + /status health  [#2523](https://github.com/skycoin/skywire/pull/2523)

### Skychat reliability and observability

-   `fix(skychat)`: listen --json + --from + outgoing-mirror + network on inbound  [#2508](https://github.com/skycoin/skywire/pull/2508)
-   `feat(skychat)`: schema v1 — id/to/len/schema on listen + /status counters  [#2510](https://github.com/skycoin/skywire/pull/2510)
-   `feat(skychat)`: send --wait peer-receipt ack via chat-msg/chat-ack envelopes  [#2511](https://github.com/skycoin/skywire/pull/2511)
-   `fix(gotop)`: capture stray log output instead of corrupting the TUI  [#2521](https://github.com/skycoin/skywire/pull/2521)
-   `feat(skychat)`: /status send-failure + sse-drop counters; cached-only retry guard; CLI --retries  [#2526](https://github.com/skycoin/skywire/pull/2526)

### Operator UX

-   `feat(skychat-cli)`: history + status subcommands  [#2509](https://github.com/skycoin/skywire/pull/2509)
-   `feat(skychat-cli)`: PK alias / addressbook with reverse-resolve on listen + history  [#2512](https://github.com/skycoin/skywire/pull/2512)
-   `feat(skychat-cli)`: unified TUI — picker + DM + group chat  [#2518](https://github.com/skycoin/skywire/pull/2518)

### dmsg utility belt

-   `feat(dmsgpty)`: non-interactive Exec — run one command, return stdout/stderr/exit  [#2519](https://github.com/skycoin/skywire/pull/2519)
-   `feat(dmsgscp)`: scp-over-dmsg utility, port 23, whitelist-gated  [#2524](https://github.com/skycoin/skywire/pull/2524)
-   `fix(dmsgscp)`: default-on, listen on dmsg + skynet  [#2527](https://github.com/skycoin/skywire/pull/2527)

### Performance and docs

-   `perf(dmsg/noise)`: cache static-static DH results for repeat peers  [#2507](https://github.com/skycoin/skywire/pull/2507)
-   `docs(auto-update)`: GOPROXY=direct for branch-tip resolution  [#2517](https://github.com/skycoin/skywire/pull/2517)

## 1.3.52

Network-state release: CXO publishers/subscribers close the last
HTTP-only gaps so service-discovery, transport-discovery and
DMSG-discovery can be consumed over CXO with HTTP/bbolt fallback;
operator-facing observability gains live-refresh CLI views and a
pprof-summary command; HTTPS on `.skynet` / `.dmsg` arrives via an
opt-in locally-installed CA; and the ping subtree gets a focused
rewrite. Plus a tight stack of correctness fixes — including a
12-minute production deadlock between the cxo cache and bbolt.

### HTTPS on .skynet / .dmsg

-   `pkg/skynetca` + resolver MITM: opt-in TLS termination for
    `.skynet` / `.dmsg` URLs using a locally-installed
    name-constrained CA. Browsers get padlock-green when the operator
    enrolls the CA in their trust store; nothing changes for users
    who don't. [#2484](https://github.com/skycoin/skywire/pull/2484)
-   `skynetweb`: Host-header rewrite via subdomain prefix so
    multi-tenant origins can disambiguate vhosts behind the resolver.
    Depends on the MITM termination from #2484 to see the unencrypted
    Host header. [#2485](https://github.com/skycoin/skywire/pull/2485)
-   `pkg/visor`: port-80 reverse-proxy `--preserve-host` for
    vhost-based backends — don't overwrite the Host header when the
    backend keys on it. [#2486](https://github.com/skycoin/skywire/pull/2486)
-   `skynetca,resolver`: base32 PK DNS labels so HTTPS-via-skynet
    leaf certs are RFC-compliant — hex 66-char PKs exceeded the
    63-char DNS-label limit. [#2487](https://github.com/skycoin/skywire/pull/2487)
-   `skywire-cli`: `serve add --to <host>:<port>` preserves the
    explicit host verbatim (previous behavior rewrote it to
    `127.0.0.1`). [#2488](https://github.com/skycoin/skywire/pull/2488)

### CXO subscribe/publish gap closed

-   `pkg/service-discovery`, `pkg/transport-discovery`,
    `pkg/dmsg/discovery`: CXO publishers are always-on instead of
    flag-gated. Drops the `--cxo` toggle and its operator-error
    surface; the publishers idle cheaply when there are no
    subscribers. [#2494](https://github.com/skycoin/skywire/pull/2494)
-   `pkg/service-discovery`, `pkg/dmsg/discovery`: decouple the HTTP
    register path from the CXO publisher mutex so a slow CXO
    publisher tick can't stall a service registering by HTTP.
    [#2495](https://github.com/skycoin/skywire/pull/2495)
-   `cli,visor,tpd`: close the last CXO subscriber gap (SD
    `/api/services?type=…`, TPD `/all-transports`) and add a
    bbolt-backed CLI fallback cache at
    `$XDG_CACHE_HOME/skywire/cli-fetch.db` so URL-keyed responses
    persist across CLI invocations and visor restarts. Replaces the
    legacy `/tmp/*.json` files (which umask was silently
    downgrading). [#2491](https://github.com/skycoin/skywire/pull/2491)
-   `pkg/cxo/node`: backpressure the CXO accept loop so a pile of
    pending handshakes can't exhaust file descriptors before any
    completes. [#2482](https://github.com/skycoin/skywire/pull/2482)
-   `pkg/cxo`: break a Cache.mu ↔ bbolt.rwlock A/B deadlock in
    `cxoutils.RemoveObjects`. `Publisher.runCleanupLoop` held the
    bbolt write lock while waiting on `Cache.IsCached` (Cache.mu);
    `Publisher.runLoop` held Cache.mu via `Cache.Set` while waiting
    on bbolt. Snapshot the cached-keys set once up front so the
    iterator callback only does a map lookup — eliminates the lock
    inversion. Observed in production after ~12 minutes of run; the
    new `skywire cli visor goroutines` summary surfaced it in
    seconds. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)

### Hypervisor + CLI parity, observability

-   `hv/cli` parity wave 1: bulk-reward operations, runtime log
    streaming with module/level filters, DMSG servers tab, route
    groups view, skynet UI polish. [#2483](https://github.com/skycoin/skywire/pull/2483)
-   `cli`: `--live` (-L) on every visor self-data command —
    bubbletea split-pane with header + spinner + scrollable
    viewport. Wired into `visor info`, `visor dmsg-servers`,
    `visor uptime`, `visor app ls`, `tp`, `route`, `route groups`,
    `rg ls`. Shared `cliutil/livetui` helper. Part of
    [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `cli`: `skywire cli visor goroutines` — fetches and summarises
    the visor's pprof goroutine dump. Per-state distribution,
    top-N stack heads, and a lock-waiter analysis that surfaces
    A/B deadlocks within seconds (`--full`, `--filter <regex>`,
    `--state <substr>`, `--out <file>`). Works while the visor RPC
    is hung — pprof keeps serving even when the rpc.Server is
    wedged behind a mutex. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `cli`: `skywire cli visor cxo {status,refresh,fetch}` —
    introspection for the visor's lazy-on-demand CXO subscription
    manager. Status shows snapshot size / last-sync / refcount /
    last-error per feed; `refresh` forces a synchronous
    subscribe→Root→Walk and reports the post-refresh FeedStatus;
    `fetch` invokes FetchCXO RPC directly. Part of
    [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `route calc --by-latency`: rank candidate routes by
    transport-aggregate latency, not just hop count. [#2492](https://github.com/skycoin/skywire/pull/2492)
-   `pkg/visor/stats`: panic-recover wrapping on every sample
    function so one misbehaving subsystem can't crash the
    tracker goroutine. [#2492](https://github.com/skycoin/skywire/pull/2492)

### Ping tree

-   `cli`: collapse the three overlapping ping commands (`tree`,
    `tree2`, `graph`) into a single `tree` with the better
    rendering + status messages. Adds the `--testenv` flag,
    extracts a `pingTreeConfig` struct from 31 globals, fixes the
    `--hops 2` hang where level-1 entries were skipped before BFS
    expansion, and preserves full error text in saved JSON results
    instead of truncating display-only errors at storage time.
    [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `pkg/visor`: direct-transport bypass for ping — skip the
    route-setup-node when a direct transport to the target is
    available. Reuses the AppDirect VStreamMux from skynet
    resolving proxy. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `pkg/app/appnet`: ConvertAddr now passes appnet.Addr through
    unchanged. Listeners using WrapConn on AppDirect dial conns
    previously errored with ErrUnknownAddrType; the ping listener
    closed the conn and the client saw "write size: use of closed
    network connection". Part of [#2493](https://github.com/skycoin/skywire/pull/2493)

### Skychat

-   `cli`: `skywire cli skychat chat -t <pk>` — interactive
    bubbletea split-pane with viewport history, textinput
    compose, and SSE-livefeed. Part of
    [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `cli/skychat`: `send -t X -m hi` (no `--net`) now correctly
    defaults to `skynet`. The package-level `networkType` was
    shared between `send` and `listen` with different cobra
    defaults; the second registration's empty default clobbered
    the first. Split into separate vars. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `apps/skychat`: SSE stream survives the HTTP server's
    `WriteTimeout`. Disabled the per-request write deadline for
    `/sse` only (via `http.NewResponseController`) and added
    periodic `: ping` keepalive comments so the connection stays
    open between message bursts and any reverse-proxy idle timeout
    is kept warm. Previous behavior killed every SSE subscriber
    after 10s. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)

### Stability + correctness

-   `cli/skychat`: drop the redundant trailing `\n` in the `listen`
    banner `Println` (go vet). [#2496](https://github.com/skycoin/skywire/pull/2496)
-   `cli/tp`: register the `--cfa` (cache-files-age) flag on `tp`
    itself, not just `tp tree`. `tp -m` previously ran with
    `cacheFilesAge=0`, which `clicache.Fresh` treats as "always
    stale" — so every invocation refetched SD and watched its
    online-state filter flap between calls. Default of 5m matches
    `tp tree`. Part of [#2493](https://github.com/skycoin/skywire/pull/2493)
-   `autoconfig`: propagate resolved `PKGENV`/`USRENV` mode to the
    `skywire-config gen` subprocess so the generated config picks
    the right paths. Previously the parent's resolved mode was
    discarded and `gen` re-detected from scratch — sometimes
    landing on the wrong one. [#2490](https://github.com/skycoin/skywire/pull/2490)
-   `svc`: `pkg/services.Duration` accepts both numeric (ns) and
    string forms so e2e `services.json` parses `entry_timeout`
    correctly. [#2481](https://github.com/skycoin/skywire/pull/2481)
-   `chore(deb)`: drop the redundant `SKYWIRE_USER` drop-in cleanup
    from `postinst` — `autoconfig`'s own cleanup (in #2476)
    supersedes it. [#2480](https://github.com/skycoin/skywire/pull/2480)

### Docs

-   `README`: expand the intro into a Major-features section
    covering the resolver, .skynet HTTPS, CXO publishers, mux
    routing, and the diagnostic CLI. [#2489](https://github.com/skycoin/skywire/pull/2489)

## 1.3.51

Hotfix release. Two nil-deref fixes that surfaced in the wild on
v1.3.50 visors immediately after package upgrade, plus a related
package-state cleanup in `skywire autoconfig`.

### Fixes
-   `pkg/dmsg/dmsg`: ClientSession.serve() unconditionally
    derefenced `cs.sm.yamux.IsClosed()` even when the session was
    negotiated as smux, panicking on any non-EOF accept error. The
    panic was caught by the goroutine's recover but left sessions
    in a half-dead state. Branch on which protocol is non-nil. [#2477](https://github.com/skycoin/skywire/pull/2477)
-   `pkg/vpn/server.go`: race between `Server.Close()` setting
    `s.lis = nil` and `Server.Serve()` reading `s.lis.Accept()`
    without holding the listener mutex — any accept error that
    didn't match `net.ErrClosed` (e.g. wrapped dmsg listener
    returning "io: read/write on closed pipe") routed to
    `continue`, then nil-deref'd on the next iteration. The panic
    fired outside the recover and took down the whole visor
    process. Accept off the local parameter, not the shared
    field, plus a defensive post-Close check. [#2477](https://github.com/skycoin/skywire/pull/2477)
-   `skywire autoconfig`: clear the stale systemd drop-in at
    `/etc/systemd/system/skywire.service.d/skywire-user.conf` when
    `/etc/skywire.conf` no longer has `SKYWIRE_USER=` set. Previous
    behavior wrote the drop-in on first set but never removed it
    on unset, leaving the unit pinned to a user the operator no
    longer intended to run as — failing either CHDIR or the
    visor's `--pkg requires root` check on every restart. The same
    defensive cleanup landed in `scripts/deb_installer/deb.postinst`
    (the .deb's own service runs as root unconditionally, so any
    drop-in present at install time is definitionally stale). [#2476](https://github.com/skycoin/skywire/pull/2476)

## 1.3.50

### Mux & per-transport latency
-   tpd+visor: per-transport latency end-to-end; bandwidth + latency reach TPD via the discovery API and surface in `tp ls`. `--mux 0` now means *unlimited* (use every available transport-disjoint route). [#2401](https://github.com/skycoin/skywire/pull/2401)
-   Per-mux-leg byte counters + `proxy mux-info` (one row per leg with sent/recv bytes/packets and latency); `--watch` for top-style refresh. Part of [#2401](https://github.com/skycoin/skywire/pull/2401)
-   Runtime mux reconfiguration: `proxy mux-add` (caller-supplied route, piped from `route calc --json`), `proxy mux-rm <tp-id>`, `proxy mux-mode auto|equal`. `--rg <src-port>` disambiguates when an app has multiple concurrent rg's. [#2405](https://github.com/skycoin/skywire/pull/2405)

### Forwarded ports
-   `--proxy-addr` (a.k.a. `serve --to host:port`) now honored on every forwarded port, not just port 80 — exposes a service running anywhere on the LAN over `.skynet`/`.dmsg`. Hypervisor UI gains a "Target Address" input on the add-port form. [#2405](https://github.com/skycoin/skywire/pull/2405)
-   Per-port PK whitelist enforced on raw-TCP skynet and DMSG forwarders. [#2395](https://github.com/skycoin/skywire/pull/2395)
-   `serve whitelist` subcommand for in-place whitelist updates; "WHITELIST" column in `serve ls`. [#2394](https://github.com/skycoin/skywire/pull/2394)
-   Port-80 reverse proxy: honor `--local-port` when `--proxy-addr` is empty [#2371](https://github.com/skycoin/skywire/pull/2371); fix WebSocket upgrade on the proxied path [#2377](https://github.com/skycoin/skywire/pull/2377).
-   `cli skynet port {add,ls,rm}` renamed to `cli serve {add,ls,rm}`; the old commands stay as deprecated shims. [#2373](https://github.com/skycoin/skywire/pull/2373)
-   Move `DefaultCXOPort` off 46 to stop colliding with `DmsgHypervisorPort`. [#2367](https://github.com/skycoin/skywire/pull/2367)

### Routing & route-finder
-   `cli route calc` returns multiple routes (streamed via gRPC) and respects the visor's `routing.min_hops`. [#2397](https://github.com/skycoin/skywire/pull/2397)

### DHT / address mirror — added then removed
The Kademlia DHT subsystem was added during this release cycle and then removed in [#2459](https://github.com/skycoin/skywire/pull/2459) once the CXO publisher/subscriber tree (below) demonstrated a simpler way to fan out the same data. The PRs are listed for completeness but the code is gone in v1.3.50.
-   DHT mirror to HTTP discoveries with `addr` and `self_publish` payloads; one signing writer per tp salt. [#2399](https://github.com/skycoin/skywire/pull/2399)
-   Spec audit + comparison doc; `cli dht peers/reconcile/source` for inspecting DHT state. [#2398](https://github.com/skycoin/skywire/pull/2398)
-   Publish a real signed DMSG entry on the DHT path. [#2365](https://github.com/skycoin/skywire/pull/2365)
-   Fix DHT seq + self-probe endpoint. [#2351](https://github.com/skycoin/skywire/pull/2351)
-   **Remove the Kademlia DHT subsystem in favor of CXO + HTTP discovery.** [#2459](https://github.com/skycoin/skywire/pull/2459)

### DMSG / dmsg-servers
-   `dmsg_servers`: embedded list refresh, live `confbs` response, visor-side disk cache. Bootstrap continues working with stale or unreachable `confbs`. [#2403](https://github.com/skycoin/skywire/pull/2403)
-   dmsg-server: preload direct client with peer dmsg-server entries to skip first-message lookup miss. [#2390](https://github.com/skycoin/skywire/pull/2390)
-   dmsgd: keep entry cache populated on `SetEntry` instead of invalidating. [#2382](https://github.com/skycoin/skywire/pull/2382)
-   setup-node: dmsg-http for outbound TPD/AR clients. [#2362](https://github.com/skycoin/skywire/pull/2362)
-   docs(deployment): dmsg-server DHT/Redis configuration. [#2392](https://github.com/skycoin/skywire/pull/2392)
-   `disc/dmsgfirst`: APIClient that tries DMSG first, falls back to HTTP on dial failure. [#2433](https://github.com/skycoin/skywire/pull/2433)
-   dmsg-disc: decouple discovery from the HTTP-bootstrap loop. [#2436](https://github.com/skycoin/skywire/pull/2436)
-   dmsg-disc: drop `--dmsg-servers` flag; rely on the embedded keyring. [#2437](https://github.com/skycoin/skywire/pull/2437)
-   dmsg-disc: polymorphic `dmsg` config block; per-deployment server lists. [#2438](https://github.com/skycoin/skywire/pull/2438)
-   visor: upgrade dmsg disc clients to dmsgfirst once `dmsgC` is ready. [#2441](https://github.com/skycoin/skywire/pull/2441)

#### dmsgfirst self-deadlock and recursion fixes
A series of related bugs surfaced once dmsgfirst was wired into more code paths — entries getting locked while another goroutine on the same entity tried to refresh them, and recursion through the HTTP-fallback path when the DMSG dial itself needed a fresh entry.
-   Self-deadlock in `updateClientEntry` under the dmsgfirst path. [#2443](https://github.com/skycoin/skywire/pull/2443)
-   Self-deadlock in `updateServerEntry` under the dmsgfirst path. [#2448](https://github.com/skycoin/skywire/pull/2448)
-   Self-deadlock in `EnsureAndObtainSession` across the dmsgfirst path. [#2449](https://github.com/skycoin/skywire/pull/2449)
-   Break `getServerEntry` recursion under the dmsgfirst path. [#2451](https://github.com/skycoin/skywire/pull/2451)
-   Reject cached server entries in `getClientEntryCached`. [#2454](https://github.com/skycoin/skywire/pull/2454)
-   Break dmsgfirst recursion via context guard in `HTTPTransport`. [#2455](https://github.com/skycoin/skywire/pull/2455)

### SUDPH / STCPR
-   sudph: reconnect on AR conn drop; retry STUN on transient failure; relax handshake to 5s. [#2372](https://github.com/skycoin/skywire/pull/2372)
-   stcpr re-register body retry; allow dmsg-only visors to register SUDPH. [#2388](https://github.com/skycoin/skywire/pull/2388)
-   sudph: nil-guard Dial when listen() failed. [#2400](https://github.com/skycoin/skywire/pull/2400)

### Visor
-   Self-tracking telemetry + TPD-as-aggregator via CXO TreeStore. [#2359](https://github.com/skycoin/skywire/pull/2359)
-   geoip: visor uses embedded MMDB instead of querying `ip.skycoin.com`. [#2352](https://github.com/skycoin/skywire/pull/2352)
-   Register DHT + RSN await-setup listeners early in init. [#2396](https://github.com/skycoin/skywire/pull/2396)
-   `--dmsgweb` / `--skynetweb` gen flags for proxy auto-start. [#2366](https://github.com/skycoin/skywire/pull/2366)
-   User-publishable CXO feeds with `/feeds` discovery. [#2369](https://github.com/skycoin/skywire/pull/2369)
-   `httputil`: fix `WriteJSON` panic on slow clients; sweep `err.Error()` string matches → `errors.Is`. [#2402](https://github.com/skycoin/skywire/pull/2402)
-   geoip without HTTP — dmsg-server `LookupIPGeo` + `Geo` on SD entries means visors no longer need to round-trip the geoip service. [#2439](https://github.com/skycoin/skywire/pull/2439)
-   Wire `--pprofmode` through dmsg cmdutil so non-http modes (cpu/mem/mutex/block/trace) actually work. [#2431](https://github.com/skycoin/skywire/pull/2431)
-   Register `RuntimeLogs` on the RPC server. [#2417](https://github.com/skycoin/skywire/pull/2417)
-   Stop disabling SD/visor registrations on transient failures. [#2407](https://github.com/skycoin/skywire/pull/2407)
-   Re-register in service discovery once transport count drains below max. [#2453](https://github.com/skycoin/skywire/pull/2453)
-   `--public-ip`: trust visor-declared `PublicIP` when AR's observed source is non-public (dmsg-only / NATed paths). [#2409](https://github.com/skycoin/skywire/pull/2409)

### CXO publishers, subscribers, and on-demand sync
A new pattern emerged this cycle: deployment services publish their state into a CXO TreeStore feed, visors and the hypervisor subscribe on demand, and the bulk of "discover what's out there" traffic moves off HTTP polling. Replaces the discarded DHT path.
-   TPD: mirror transport register/deregister via CXO publisher. [#2452](https://github.com/skycoin/skywire/pull/2452)
-   SD + DMSG-D: CXO publishers for services + clients-by-server. [#2456](https://github.com/skycoin/skywire/pull/2456)
-   Visor: on-demand CXO subscription manager — subscriptions stay open while a UI tab is acquired and tear down on a configurable grace period. [#2457](https://github.com/skycoin/skywire/pull/2457)
-   Cycle-based CXO sync: subscribe → snapshot → unsubscribe → wait → repeat. [#2460](https://github.com/skycoin/skywire/pull/2460)
-   tpviz: cut `/api/services` over to CXO subscriber with HTTP fallback. [#2458](https://github.com/skycoin/skywire/pull/2458)
-   tpviz: `/api/dmsg/servers/clients` mirroring DMSG-D's clients-by-server CXO snapshot. [#2463](https://github.com/skycoin/skywire/pull/2463)
-   visor/autoconnect: pull public visors from the CXO snapshot when available. [#2461](https://github.com/skycoin/skywire/pull/2461)
-   visor: cut `VPNServers` / `ProxyServers` / `PublicVisors` over to the CXO snapshot. [#2462](https://github.com/skycoin/skywire/pull/2462)

#### CXO bug fixes
-   `Cache.cleanDown` eviction bug; skip re-encoding unchanged sub-trees. [#2420](https://github.com/skycoin/skywire/pull/2420)
-   Drop superseded roots + sticky `Unpack.created` for dedup safety. [#2434](https://github.com/skycoin/skywire/pull/2434)
-   Skip blank refs in `DelRoot` walk so cleanup actually decrements shared subtrees. [#2446](https://github.com/skycoin/skywire/pull/2446)
-   `cxoaggregator`: log `OnRootReceived` + `OnFillingBreaks`. [#2411](https://github.com/skycoin/skywire/pull/2411)
-   `cxo/node`: evict dead DMSG conns from cache on Conn cleanup. [#2413](https://github.com/skycoin/skywire/pull/2413)
-   `cxo/node`: guard `fillHead` nil-deref races (`closeFiller` + `handleDelConn`). [#2423](https://github.com/skycoin/skywire/pull/2423)
-   tpd panic loop: cxo `Finc`-to-negative + httputil short-write discriminator. [#2422](https://github.com/skycoin/skywire/pull/2422)

### Multi-service framework — `skywire svc run`
A new entry point that runs any subset of the deployment-side services (TPD, SD, AR, RF, DMSG-D, DMSG server, setup-node, transport-setup, stun-server) — and optionally a visor — in a single process. Drives the CI e2e from eleven separate containers down to one.
-   JSON config files for the five existing deployment services. [#2440](https://github.com/skycoin/skywire/pull/2440)
-   `pkg/services` framework + `skywire svc run` cobra subcommand. [#2464](https://github.com/skycoin/skywire/pull/2464)
-   Per-service migrations onto the framework: dmsg-discovery [#2465](https://github.com/skycoin/skywire/pull/2465); dmsg-server [#2466](https://github.com/skycoin/skywire/pull/2466); transport-discovery [#2468](https://github.com/skycoin/skywire/pull/2468); SD/AR/RF [#2469](https://github.com/skycoin/skywire/pull/2469); setup-node, transport-setup, stun-server [#2470](https://github.com/skycoin/skywire/pull/2470); skywire-visor [#2472](https://github.com/skycoin/skywire/pull/2472).
-   Collapse nine deployment containers to one in CI e2e (`docker/docker-compose.yml`). [#2471](https://github.com/skycoin/skywire/pull/2471)
-   Update Makefile `e2e-run` for the collapsed services container. [#2473](https://github.com/skycoin/skywire/pull/2473)

### Hypervisor UI
-   New tabs: VPN, Skysocks, multi-instance app surface, autoconfig-userspace; Resources / fleet CXO metrics views. [#2424](https://github.com/skycoin/skywire/pull/2424), [#2435](https://github.com/skycoin/skywire/pull/2435)
-   Transport latency display in the transports list. [#2419](https://github.com/skycoin/skywire/pull/2419)
-   skysocks/vpn-server whitelist setting + dmsg reverse proxy in the UI. [#2412](https://github.com/skycoin/skywire/pull/2412)
-   WAN-reachable embedded dmsg, discovery proxy, and per-visor terminal-tab persistence. [#2450](https://github.com/skycoin/skywire/pull/2450)
-   Always-on tpviz tab (drops the `config.TPViz.Enable` gate so older configs no longer 404). [#2474](https://github.com/skycoin/skywire/pull/2474)
-   Render cached fields on offline visors instead of going blank. [#2447](https://github.com/skycoin/skywire/pull/2447)

### Transport metrics
-   Persist latency in a dedicated key, decoupled from registration TTL. [#2418](https://github.com/skycoin/skywire/pull/2418)
-   Don't zero-clobber latency on partial measurements. [#2415](https://github.com/skycoin/skywire/pull/2415)
-   Drop outlier RTT samples above `MaxReasonableRTTMs` (30s). [#2421](https://github.com/skycoin/skywire/pull/2421)
-   Cap `UpdateLatency` at `MaxReasonableRTTMs`. [#2425](https://github.com/skycoin/skywire/pull/2425)
-   Fix verified-bandwidth zero-edge in tp metrics. [#2414](https://github.com/skycoin/skywire/pull/2414)
-   Render AR registration without duplicate port for SUDPH in `cli visor info`. [#2410](https://github.com/skycoin/skywire/pull/2410)
-   Nest daily transport rollup under `<date>/rollup`, not `<date>`. [#2445](https://github.com/skycoin/skywire/pull/2445)

### Service-self uptime
-   `serviceuptime`: per-service self-uptime tracker + version provenance via local bbolt. [#2428](https://github.com/skycoin/skywire/pull/2428)
-   Transport uptime: CXO-driven heartbeats + slot-accurate timeline + visor-published bitmap merge. [#2426](https://github.com/skycoin/skywire/pull/2426)
-   Retire the on-disk CSV transport-log store; serve history from stats bbolt. [#2427](https://github.com/skycoin/skywire/pull/2427)

#### TPD perf
-   Bulk-read uptime timeline bitmaps with one GET per day. [#2429](https://github.com/skycoin/skywire/pull/2429)
-   No-latency variant for `mirrorEdges` + concat redis-key builders. [#2430](https://github.com/skycoin/skywire/pull/2430)

### Skychat / pairing
-   Consent-based pair-invite flow with accept/decline UI. [#2386](https://github.com/skycoin/skywire/pull/2386)
-   ECDH + ChaCha20-Poly1305 body encryption on paired feeds. [#2385](https://github.com/skycoin/skywire/pull/2385)
-   UI: pair toggle, CXO send, paired-contact sync. [#2384](https://github.com/skycoin/skywire/pull/2384)
-   HTTP `/pair` endpoints + pair-invite/ack handshake over legacy. [#2383](https://github.com/skycoin/skywire/pull/2383)
-   `cli visor pair tree` + end-to-end pair integration test. [#2381](https://github.com/skycoin/skywire/pull/2381)
-   Visor: chat-pair feed manager + RPC surface. [#2380](https://github.com/skycoin/skywire/pull/2380)
-   Pairing: per-pair feed primitives + bolt store. [#2379](https://github.com/skycoin/skywire/pull/2379)
-   cxo/treestore: subscriber allowlist on `Publisher`. [#2378](https://github.com/skycoin/skywire/pull/2378)
-   Fan SSE messages out to all clients (fix self-send drop). [#2389](https://github.com/skycoin/skywire/pull/2389)
-   docs: skychat pairing design + regenerate goda graph. [#2387](https://github.com/skycoin/skywire/pull/2387)

### CLI
-   Top-level shortcuts for the high-traffic visor verbs. [#2374](https://github.com/skycoin/skywire/pull/2374)
-   Drop the `{"output": ...}` JSON envelope; route JSON errors to stderr. [#2375](https://github.com/skycoin/skywire/pull/2375)
-   Migrate remaining commands to `PrintOutput`; drop envelope from integration tests. [#2376](https://github.com/skycoin/skywire/pull/2376)
-   Stop truncating public keys in logs / CLI output. [#2391](https://github.com/skycoin/skywire/pull/2391), [#2393](https://github.com/skycoin/skywire/pull/2393)
-   Include PK on untrusted-setup-node reject; quiet expected dmsg-tracker miss. [#2368](https://github.com/skycoin/skywire/pull/2368)
-   Ask the running visor for its config path instead of guessing. [#2361](https://github.com/skycoin/skywire/pull/2361)
-   `rewards run` orchestrator, eliminate bash from the rewards cycle. [#2370](https://github.com/skycoin/skywire/pull/2370)
-   socks5: switch from `confiant-inc` to `armon/go-socks5` (removes the 5s tunnel deadline). [#2364](https://github.com/skycoin/skywire/pull/2364)

### Refactors
-   Drop the HTTP bridge in `dmsgweb` and `skynet-fwd` entirely — TCP-only on both sides. [#2358](https://github.com/skycoin/skywire/pull/2358), [#2360](https://github.com/skycoin/skywire/pull/2360); follow-up resolver fix [#2363](https://github.com/skycoin/skywire/pull/2363).
-   Flatten `pkg/skywire-utilities/pkg/*` into `pkg/*`. [#2356](https://github.com/skycoin/skywire/pull/2356)
-   Flatten `pkg/routefinder/rfclient` to `pkg/rfclient`. [#2355](https://github.com/skycoin/skywire/pull/2355)
-   Split visor `rpc.go` and `hypervisor.go` HTTP handlers by topic. [#2353](https://github.com/skycoin/skywire/pull/2353), [#2357](https://github.com/skycoin/skywire/pull/2357)
-   Split tpd `redis_store.go` by topic. [#2354](https://github.com/skycoin/skywire/pull/2354)

### Infra
-   docker: propagate build failures from deploy scripts; use `proxy.golang.org`. [#2404](https://github.com/skycoin/skywire/pull/2404)
-   nix: packaging — flake-based source builds. [#2416](https://github.com/skycoin/skywire/pull/2416)
-   nix: derive source-build version from flake git metadata, fix readme flake input. [#2432](https://github.com/skycoin/skywire/pull/2432)
-   chore(deps): bump fast-uri 3.1.0 → 3.1.2 (security advisory GHSA-v39h-62p7-jpjc); refresh Go module tree. [#2474](https://github.com/skycoin/skywire/pull/2474)

## 1.3.47

### Hypervisor TUI & multi-hypervisor management
-   New hypervisor terminal UI: `skywire cli visor hv tui` — list connected visors, view detail (transports, apps, route groups with hops, DMSG servers), and execute write actions via hotkeys (set min_hops/mux_routes/calculate_routes, reward address, public_autoconnect, start/stop/autostart apps, app logs, add/delete transports, delete routing rules, embedded resolving proxies, skynet/forwarded ports, services-health, dmsg connect-all/sessions-count, reload/shutdown). [#2337](https://github.com/skycoin/skywire/pull/2337)
-   Hypervisor-of-hypervisors: when a hypervisor has another hypervisor in its config, the parent transparently sees and manages the child's connected visors. `HVListVisors` merges sub-hypervisor visors (tagged `proxied_via`); all `HV*` write methods recurse one hop through the sub-hypervisor when the target isn't directly connected.
-   `RouteGroupInfo` now exposes the stored forward route hops (transport IDs, edges, types) — surfaced in TUI and the `/visors/{pk}/routegroups` endpoint.
-   `Summary` adds `route_groups` and `dmsg_servers` is now rendered in the visor detail panel.

### Routing & transport selection
-   Route-finder service now filters `LabelSetup` transports out of the graph at build time so RSN control-plane transports never appear in data routes.
-   DMSG hops constrained to the last hop of any route — multiple DMSG transports per route can silently loop traffic through the same opaque dmsg-server intermediary.
-   Mux setup and append paths refuse to multiplex routes containing DMSG transports.
-   Configurable transport-type preference (`routing.transport_preference`): defaults `stcpr > sudph > stcp > dmsg`. Applied in both route-finder graph and local route calc when multiple transports exist between the same edges.

### DHT
-   fix(dht): raise MaxValueSize 16K → 64K, log size-induced publish drops [#2349](https://github.com/skycoin/skywire/pull/2349)
-   dht: skip mirror publish when subject payload is unchanged [#2336](https://github.com/skycoin/skywire/pull/2336)
-   Perf/dht mirror list per subject [#2334](https://github.com/skycoin/skywire/pull/2334)
-   Perf/tpd dht mirror sign once [#2333](https://github.com/skycoin/skywire/pull/2333)
-   dht: skip p2p transport for bootstrap peers [#2332](https://github.com/skycoin/skywire/pull/2332)
-   Feat/dht to discovery — mirror DHT writes to HTTP discoveries [#2328](https://github.com/skycoin/skywire/pull/2328)
-   add DHT → discovery pusher [#2327](https://github.com/skycoin/skywire/pull/2327)

### Transport discovery (perf)
-   perf(tpd): replace `tp:*` SCAN with SMEMBERS on a transport-id index set [#2346](https://github.com/skycoin/skywire/pull/2346)
-   perf(tpd): extend allTransportsCache to cover getAllTransportsWithQoS [#2344](https://github.com/skycoin/skywire/pull/2344)
-   perf(tpd): short-TTL cache for GetAllTransports to absorb sync=true bursts [#2342](https://github.com/skycoin/skywire/pull/2342)
-   perf(tpd): per-edge entry cache eliminates repeat fetches in mirrorEdges [#2340](https://github.com/skycoin/skywire/pull/2340)
-   tpd: cache parsed edge pubkeys in redisStore [#2335](https://github.com/skycoin/skywire/pull/2335)

### Address resolver (perf)
-   perf(ar): replace GetAll SCAN with SMEMBERS on a per-netType index set [#2345](https://github.com/skycoin/skywire/pull/2345)
-   perf(ar): short-TTL cache for GetAll to absorb /transports endpoint SCANs [#2343](https://github.com/skycoin/skywire/pull/2343)

### Service discovery & DMSG
-   perf(sd): replace SCAN-for-existence with per-visor index lookup [#2339](https://github.com/skycoin/skywire/pull/2339)
-   perf(dmsghttp): per-destination stream pool to skip noise handshake on reuse [#2347](https://github.com/skycoin/skywire/pull/2347)
-   fix DMSG server DHT Redis auth: read REDIS_PASSWORD from env [#2326](https://github.com/skycoin/skywire/pull/2326)
-   Fix/dmsg fwd startup [#2325](https://github.com/skycoin/skywire/pull/2325)

### Setup node & cipher
-   setup-node: add porter watchdog to bound ephemeral port leaks [#2338](https://github.com/skycoin/skywire/pull/2338)
-   perf(cipher): disable DebugLevel1 to skip post-Sign verify-after-sign [#2341](https://github.com/skycoin/skywire/pull/2341)

### Skynet
-   Feat/skynet direct transport [#2331](https://github.com/skycoin/skywire/pull/2331)
-   add debug logging to forwardRawTCP for skynet data flow investigation [#2330](https://github.com/skycoin/skywire/pull/2330)
-   Feat/skynetweb fixes [#2323](https://github.com/skycoin/skywire/pull/2323)

### Visor & cleanup
-   visor: require IP in survey, retry dmsg LookupIP indefinitely [#2348](https://github.com/skycoin/skywire/pull/2348)
-   remove hardcoded service URLs, use deployment.Prod constants [#2324](https://github.com/skycoin/skywire/pull/2324)
-   Test/cli validation [#2322](https://github.com/skycoin/skywire/pull/2322)

## 1.3.40

### Auto-Update System
-   Rolling-release auto-update via `skywire-commit` branch (CI updates on test pass)
-   CI warms Go module proxy for global visor availability
-   Docker deployment auto-updater with commit-SHA tagged images
-   `UPDATE_CHANNEL` config (stable/develop/latest/pinned hash)
-   `DEPLOY_DIR` config for docker compose auto-update
-   Unprivileged build user (`skywire-build`) for compilation isolation

### Config Generation
-   Add `PROXYSERVERWL` and `VPNSERVERWL` whitelist config variables
-   Add `SKYCHAT` and `SKYCHATADDR` flags for skychat autostart/address
-   Add `REWARDSKYADDR` reward address to visor config and config gen
-   Add advanced tuning flags: `--hvaddr`, `--stun`, `--timeout`, `--regtimeout`, `--maxtransports`, `--muxroutes`
-   Rename `NOPROXYSERVER` to `PROXYSERVER` with correct default-true semantics
-   Remove dead password config entries
-   Offset localhost ports by +10000 for `--testenv` config gen
-   Refactor scriptExec helpers to shared `cmdutil.Skyenv*` library

### Infrastructure Services
-   Add `--keyfile` flag to all services (ar, rf, tpd, sd, ut, dmsg-discovery)
-   Auto-generate keypair on first run, eliminating bash ExecStartPre workarounds
-   Systemd services simplified to single ExecStart line
-   Add `GODEBUG=madvdontneed=1` to all systemd services and Docker compose

### Rewards UI & SEO
-   Add whitelisted-key file access to rewards UI
-   Add pprof, visor.log, and landing page to dmsghttp log server
-   Transport logs and custom files moved behind whitelist auth
-   Add canonical URLs, sitemap.xml, robots.txt, og:image for SEO
-   Add meta descriptions and Open Graph tags to all pages
-   Remove cogentcore UI dependency (~638K vendor lines removed)

### Route Finder & Transport Discovery
-   Fix route calc OOM: iterative DFS with depth limit (default 5 hops)
-   Add `NewGraphWithDepth` for concurrent-safe graph exploration
-   Route finder API uses maxHops from request to limit depth
-   TPD: always apply TTL on transport registration (stale entries expire)

### Memory & Performance
-   Reduce listener channel buffer from 1M to 128
-   Remove cogentcore and cmd/release CGO dependencies (~706K lines removed)
-   CI lint runs consolidated (4→1), redundant build steps removed
-   VPN E2E tests restructured as phased test (4 fewer container restarts)

### Visor
-   Add DMSG gRPC listener on port 49 for remote gotop and stats
-   Add reward address to V1 config struct with validation
-   Fix CXO DataDir panic when HOME is unset (systemd services)
-   Fix dead proc cleanup: auto-remove zombie apps on restart
-   Remove `DmsgHTTPServerPath` / custom path serving

### CXO
-   Fix flaky CXO tests: event-driven subscribe with retry
-   Fix iterative DFS vertex aliasing with pending map

### CI
-   Separate `update-commit` workflow (workflow_run trigger)
-   Remove push-to-develop test trigger (avoids duplicate runs)
-   Add `withoutsystray` build tag for CGO_ENABLED=0 builds
-   Cache golangci-lint via `go install`
-   Add `examples/hello` for proxy cache warming
-   Docker images tagged with commit SHA

## 1.3.38

### Route Multiplexing
-   Implement packet-level route multiplexing across multiple transports (Phase 1-3)
-   Capability negotiation via extended handshake (CapMux, CapSACK)
-   Sequenced DataPackets with reorder buffer for out-of-order delivery
-   SACK (Selective Acknowledgment) bitmap for fast retransmission
-   Adaptive transport weighting by latency (faster transports get more packets)
-   Exclude DMSG from mux routes (relay not suitable for multiplexing)

### Routing & Connectivity
-   Increase route keepalive to 2m and handshake timeout to 30s for high-load visors
-   Handle ping/latency routes directly in IntroduceRules, bypass accept queue
-   Fix accept loop crash on stale routes with missing transports
-   Fix 4 additional accept loop crashes in setupnode, embedded_tps, rpc_gateway, hypervisor
-   Fix accept loop spin on shutdown (treat closed connection as shutdown signal)
-   Add 10s timeout for ping route handshakes to limit goroutine lifetime
-   Add 5-minute deadline on CLI RPC connections to prevent hung methods
-   Retry latency probe with exponential backoff on failure

### CLI & Service Commands
-   Add `skywire cli route groups` to list active route groups
-   Add `skywire cli svc health` to check all deployment services (via visor RPC)
-   Add `skywire cli svc tpd` subcommands: stats, per-key-stats, visor-stats, versions, bandwidth, metrics-visor, metrics-tp
-   Add `skywire cli svc dmsgd` subcommands: all-servers, server-clients, clients
-   Add `skywire cli svc ar` for address resolver transport lists
-   Add `skywire cli tp metrics` sent/recv columns, transport count, tree view, latency display
-   Add generic FetchServiceData RPC for proxying service queries through visor

### Reward System
-   Add opt-in login chain auto-setup for blockchain-based wallet authentication
-   Add fiber node reverse proxy for skycoin-web thin client
-   Add TPD network summary to /stats page with on-demand caching
-   Update mainnet rules: version v1.3.36 cutoff, rewrite transport/latency sections

### CXO Integration
-   Integrate CXO P2P content-addressable object distribution system
-   Add `skywire cxo daemon` and `skywire cxo cli` commands

### Release & Infrastructure
-   Update release workflow to build from cmd/release/ (hardware wallet support)
-   Update Docker base image to golang:1.26-alpine
-   Add paths-ignore to CI workflow for documentation changes
-   Add STUN server to E2E docker-compose for SUDPH testing
-   Fix AR UDP port mismatch in E2E environment
-   Add E2E test for route multiplexing
-   Update skycoin vendor to v0.28.4-alpha4

## 1.3.35

-   Fix dmsgtracker fails to connect  [#2188](https://github.com/skycoin/skywire/pull/2188)
-   Optimize /metrics endpoint with Redis pipelining  [#2187](https://github.com/skycoin/skywire/pull/2187)
-   Refactor tpviz cache config to use directories  [#2186](https://github.com/skycoin/skywire/pull/2186)
-   Add SKYWIRE_RPC environment variable to set CLI RPC address  [#2185](https://github.com/skycoin/skywire/pull/2185)
-   Dockerfile: build from repository root  [#2184](https://github.com/skycoin/skywire/pull/2184)
-   Install git in Docker builder for VCS version stamping  [#2183](https://github.com/skycoin/skywire/pull/2183)
-   Include .git in Docker build for automatic version embedding  [#2182](https://github.com/skycoin/skywire/pull/2182)
-   Use go install in Dockerfile for proper version info  [#2181](https://github.com/skycoin/skywire/pull/2181)
-   Add --testenv flag and SKYWIRETEST env for cli commands  [#2180](https://github.com/skycoin/skywire/pull/2180)
-   Add latency probe listener for transport latency measurement  [#2179](https://github.com/skycoin/skywire/pull/2179)
-   fix CI errors  [#2178](https://github.com/skycoin/skywire/pull/2178)
-   Transport discovery changes  [#2177](https://github.com/skycoin/skywire/pull/2177)
-   Consolidate TPD metrics endpoints and simplify API  [#2176](https://github.com/skycoin/skywire/pull/2176)
-   Add globe visualization  [#2174](https://github.com/skycoin/skywire/pull/2174)
-   Update Transport Discovery & specifications  [#2172](https://github.com/skycoin/skywire/pull/2172)
-   Improve network visualizer / network control panel WASM to match TypeScript UI  [#2171](https://github.com/skycoin/skywire/pull/2171)
-   Vendor dmsg f86aa3297c2f with help menu improvements  [#2170](https://github.com/skycoin/skywire/pull/2170)
-   Add color functions to custom help template for usage=false mode  [#2169](https://github.com/skycoin/skywire/pull/2169)
-   Fix coloredcobra: set help template before cc.Init()  [#2168](https://github.com/skycoin/skywire/pull/2168)
-   Improve help menus & ping  [#2167](https://github.com/skycoin/skywire/pull/2167)
-   Add fallback for old per-key-stats JSON format; update test deployment config  [#2166](https://github.com/skycoin/skywire/pull/2166)
-   More improvements to `skywire cli visor ping` & autoconnect logic  [#2165](https://github.com/skycoin/skywire/pull/2165)
-   update deps  [#2164](https://github.com/skycoin/skywire/pull/2164)
-   Fix reward system unwanted stdout logging  [#2163](https://github.com/skycoin/skywire/pull/2163)
-   Add missing DMSG API routes and caching to rewards UI  [#2162](https://github.com/skycoin/skywire/pull/2162)
-   Fix bundle.js 404 on rewards UI transport graph  [#2161](https://github.com/skycoin/skywire/pull/2161)
-   Fix nil pointer panic in log collection goroutine  [#2160](https://github.com/skycoin/skywire/pull/2160)
-   Fix dirty version  [#2159](https://github.com/skycoin/skywire/pull/2159)
-   Move from PG to Redis  [#2148](https://github.com/skycoin/skywire/pull/2148)

## 1.3.34

-   hypervisor ip display  [#2158](https://github.com/skycoin/skywire/pull/2158)

## 1.3.33

-   SkyNet P2P port forwarding, tp-viz network visualization UI, and various improvements  [#2156](https://github.com/skycoin/skywire/pull/2156)
-   Update dmsg to include servers/clients endpoint fix  [#2155](https://github.com/skycoin/skywire/pull/2155)
-   Various improvements  [#2149](https://github.com/skycoin/skywire/pull/2149)
-   Fix duplicate /health endpoint registration in reward server  [#2147](https://github.com/skycoin/skywire/pull/2147)
-   Fix tp-viz integration in reward system UI  [#2146](https://github.com/skycoin/skywire/pull/2146)
-   Fix internal apps logging cross-contamination  [#2145](https://github.com/skycoin/skywire/pull/2145)
-   Add /all-transports/per-key-stats endpoint  [#2143](https://github.com/skycoin/skywire/pull/2143)
-   Resolve e2e test failures  [#2142](https://github.com/skycoin/skywire/pull/2142)
-   fix internal apps  [#2140](https://github.com/skycoin/skywire/pull/2140)
-   update hardcoded dmsg-server IPs  [#2139](https://github.com/skycoin/skywire/pull/2139)
-   Fix release issues  [#2138](https://github.com/skycoin/skywire/pull/2138)
-   remove stcpr heartbeat  [#2137](https://github.com/skycoin/skywire/pull/2137)
-   add alpha and beta to tag detection on winget for skip winget request  [#2135](https://github.com/skycoin/skywire/pull/2135)

## 1.3.32

-   Fix release pipeline  [#2134](https://github.com/skycoin/skywire/pull/2134)
-   Fix/autoconnect unknown network type  [#2133](https://github.com/skycoin/skywire/pull/2133)
-   Fix/dmsg invalid pubkey panic  [#2132](https://github.com/skycoin/skywire/pull/2132)
-   Improve retry logic for TPD queries and STCPR binding  [#2131](https://github.com/skycoin/skywire/pull/2131)
-   update code deps  [#2129](https://github.com/skycoin/skywire/pull/2129)
-   fix `skywire cli tp disc` direct http queries  [#2127](https://github.com/skycoin/skywire/pull/2127)
-   remove useless code  [#2126](https://github.com/skycoin/skywire/pull/2126)
-   Fix reward system accessibility over dmsg  [#2124](https://github.com/skycoin/skywire/pull/2124)
-   More e2e  [#2123](https://github.com/skycoin/skywire/pull/2123)
-   internal / external app launcher test  [#2122](https://github.com/skycoin/skywire/pull/2122)
-   [WIP] Optimization and reverting some changes  [#2120](https://github.com/skycoin/skywire/pull/2120)
-   fix e2e tests  [#2119](https://github.com/skycoin/skywire/pull/2119)
-   test improvements  [#2117](https://github.com/skycoin/skywire/pull/2117)
-   fix go.mod  [#2114](https://github.com/skycoin/skywire/pull/2114)
-   Improve e2e tests  [#2112](https://github.com/skycoin/skywire/pull/2112)
-   Fix failing e2e tests  [#2110](https://github.com/skycoin/skywire/pull/2110)
-   fix e2e tests  [#2103](https://github.com/skycoin/skywire/pull/2103)
-   Reduce verification requests complexity  [#2101](https://github.com/skycoin/skywire/pull/2101)
-   Diagnostic logging code for TPD (Temporary)  [#2100](https://github.com/skycoin/skywire/pull/2100)
-   Add load test script for multi-instance visor testing  [#2099](https://github.com/skycoin/skywire/pull/2099)
-   Revert cache feature added on TPD   [#2098](https://github.com/skycoin/skywire/pull/2098)
-   Remove `//nolint`  [#2096](https://github.com/skycoin/skywire/pull/2096)
-   fix apps launcher [WIP]  [#2095](https://github.com/skycoin/skywire/pull/2095)
-   auth cache | revert old changes  [#2094](https://github.com/skycoin/skywire/pull/2094)
-   add cache layer for verify signature on tpd  [#2093](https://github.com/skycoin/skywire/pull/2093)
-   upgrade dmsg to develop  [#2091](https://github.com/skycoin/skywire/pull/2091)
-   Add pprof to all services  [#2090](https://github.com/skycoin/skywire/pull/2090)
-   fix pprof of ar  [#2087](https://github.com/skycoin/skywire/pull/2087)
-   improve deploy stage config  [#2086](https://github.com/skycoin/skywire/pull/2086)
-   SD in-memory store  [#2085](https://github.com/skycoin/skywire/pull/2085)
-   In-Memory Cache for TPD  [#2082](https://github.com/skycoin/skywire/pull/2082)
-   improve code of windows bat file  [#2080](https://github.com/skycoin/skywire/pull/2080)
-   Apps launcher revisions  [#2079](https://github.com/skycoin/skywire/pull/2079)
-   Update code deps  [#2078](https://github.com/skycoin/skywire/pull/2078)
-   Update mainnet rules minimum version requirement to v1.3.31  [#2076](https://github.com/skycoin/skywire/pull/2076)
-   update vendor deps  [#2072](https://github.com/skycoin/skywire/pull/2072)
-   embed database for geoip service  [#2071](https://github.com/skycoin/skywire/pull/2071)
-   Improve non-transportability logic  [#2070](https://github.com/skycoin/skywire/pull/2070)
-   add vendor dir  [#2068](https://github.com/skycoin/skywire/pull/2068)
-   [WIP] GeoIP Service  [#2067](https://github.com/skycoin/skywire/pull/2067)
-   Fix hardcoded issue on apps  [#2066](https://github.com/skycoin/skywire/pull/2066)
-   add condition to skip winget on rc versions  [#2064](https://github.com/skycoin/skywire/pull/2064)
-   improve winget script 5  [#2063](https://github.com/skycoin/skywire/pull/2063)
-   some other improve for winger  [#2062](https://github.com/skycoin/skywire/pull/2062)
-   fix issue on komac config  [#2061](https://github.com/skycoin/skywire/pull/2061)
-   replace zip with exe 2  [#2060](https://github.com/skycoin/skywire/pull/2060)
-   replace zip with exec komac  [#2059](https://github.com/skycoin/skywire/pull/2059)
-   change winget installer with manual on komac  [#2058](https://github.com/skycoin/skywire/pull/2058)
-   missed depends on ci stage  [#2057](https://github.com/skycoin/skywire/pull/2057)
-   winger update stage  [#2056](https://github.com/skycoin/skywire/pull/2056)
-   fixi missing veresion  [#2055](https://github.com/skycoin/skywire/pull/2055)
-   fix winget publish script on github workflow  [#2054](https://github.com/skycoin/skywire/pull/2054)
-   Fix Windows release issue  [#2053](https://github.com/skycoin/skywire/pull/2053)
-   update code deps & github actions workflow  [#2050](https://github.com/skycoin/skywire/pull/2050)
-   update code deps  [#2049](https://github.com/skycoin/skywire/pull/2049)
-   update changelog  [#2047](https://github.com/skycoin/skywire/pull/2047)
-   fix release workflow  [#2046](https://github.com/skycoin/skywire/pull/2046)
-   fix release workflow config  [#2045](https://github.com/skycoin/skywire/pull/2045)


## 1.3.31

-   fix release workflow config  [#2045](https://github.com/skycoin/skywire/pull/2045)
-   add winget release  [#2044](https://github.com/skycoin/skywire/pull/2044)
-   Fix release issue on Darwin installer  [#2043](https://github.com/skycoin/skywire/pull/2043)
-   improve deploy stage workflow  [#2042](https://github.com/skycoin/skywire/pull/2042)
-   add missing script  [#2041](https://github.com/skycoin/skywire/pull/2041)
-   add (again) deploy workflow on ci/cd process  [#2038](https://github.com/skycoin/skywire/pull/2038)
-   Update skycoin, dmsg, and other vendor deps  [#2037](https://github.com/skycoin/skywire/pull/2037)
-   Add `smux` lib  [#2034](https://github.com/skycoin/skywire/pull/2034)
-   re-implement country and version filtering for proxy & vpn list  [#2033](https://github.com/skycoin/skywire/pull/2033)
-   Fix IP issue behind LB  [#2032](https://github.com/skycoin/skywire/pull/2032)
-   public autoconnect via stcpr before sudph  [#2031](https://github.com/skycoin/skywire/pull/2031)
-   Improve public autoconnect logic  [#2030](https://github.com/skycoin/skywire/pull/2030)
-   Fix reward UI  [#2028](https://github.com/skycoin/skywire/pull/2028)
-   Fix transportability checker logic for public visors  [#2024](https://github.com/skycoin/skywire/pull/2024)
-   add cmd/skywire/skywire.go to repo root  [#2022](https://github.com/skycoin/skywire/pull/2022)
-   Skywire build from repo root  [#2021](https://github.com/skycoin/skywire/pull/2021)
-   Fix/ip issue behind lb  [#2020](https://github.com/skycoin/skywire/pull/2020)

## 1.3.30

-   fix release pipeline  [#2016](https://github.com/skycoin/skywire/pull/2016)
-   fix release pipeline  [#2015](https://github.com/skycoin/skywire/pull/2015)
-   Fix release pipeline  [#2014](https://github.com/skycoin/skywire/pull/2014)
-   Fix release pipeline workflow  [#2013](https://github.com/skycoin/skywire/pull/2013)
-   update deps  [#2012](https://github.com/skycoin/skywire/pull/2012)
-   Update deps  [#2011](https://github.com/skycoin/skywire/pull/2011)
-   Public visor self-transportability checker  [#2010](https://github.com/skycoin/skywire/pull/2010)
-   Update README.md  [#2009](https://github.com/skycoin/skywire/pull/2009)
-   Remove default same-version filtering from proxy & vpn list subcommands  [#2008](https://github.com/skycoin/skywire/pull/2008)
-   Update code deps  [#2007](https://github.com/skycoin/skywire/pull/2007)
-   Revert ineffective changes to InitDmsgHTTP  [#2004](https://github.com/skycoin/skywire/pull/2004)
-   Fix dmsg-direct client setup in visor init  [#2002](https://github.com/skycoin/skywire/pull/2002)
-   improve initDmsgHTTP  [#2001](https://github.com/skycoin/skywire/pull/2001)
-   update dmsg  [#2000](https://github.com/skycoin/skywire/pull/2000)
-   deduplicate redundant code  [#1999](https://github.com/skycoin/skywire/pull/1999)
-   update vendor deps  [#1997](https://github.com/skycoin/skywire/pull/1997)
-   add health check rpc method for route setup-node  [#1996](https://github.com/skycoin/skywire/pull/1996)
-   Limit transports to remote visors with public autoconnect  [#1995](https://github.com/skycoin/skywire/pull/1995)
-   update readme  [#1994](https://github.com/skycoin/skywire/pull/1994)
-   update dmsg dep  [#1993](https://github.com/skycoin/skywire/pull/1993)
-   Update vendor deps  [#1992](https://github.com/skycoin/skywire/pull/1992)
-   Update autoconnect logic  [#1991](https://github.com/skycoin/skywire/pull/1991)
-   Expand public autoconnect logic [WIP]  [#1989](https://github.com/skycoin/skywire/pull/1989)
-   update systray to fix windows issues  [#1987](https://github.com/skycoin/skywire/pull/1987)
-   Fix public visor autoconnect logic  [#1986](https://github.com/skycoin/skywire/pull/1986)
-   vendor latest commits from dmsg & fix reward system core UI  [#1985](https://github.com/skycoin/skywire/pull/1985)
-   update deps & vendor latest commits from dmsg  [#1984](https://github.com/skycoin/skywire/pull/1984)
-   Fallback to old reward system UI if `core build web` fails  [#1982](https://github.com/skycoin/skywire/pull/1982)
-   update dmsg@develop  [#1981](https://github.com/skycoin/skywire/pull/1981)
-   Add calvin library  [#1980](https://github.com/skycoin/skywire/pull/1980)
-   Update github.com/skycoin/dmsg@develop  [#1979](https://github.com/skycoin/skywire/pull/1979)
-   Fix unexpected panic on everything  [#1978](https://github.com/skycoin/skywire/pull/1978)
-   Fix version parsing  [#1973](https://github.com/skycoin/skywire/pull/1973)
-   Upgrade golangci-lint to v2.1.1  [#1971](https://github.com/skycoin/skywire/pull/1971)
-   Fix CI issue  [#1970](https://github.com/skycoin/skywire/pull/1970)
-   More reward UI improvements  [#1967](https://github.com/skycoin/skywire/pull/1967)
-   Fix reward UI  [#1966](https://github.com/skycoin/skywire/pull/1966)
-   Fix reward UI  [#1965](https://github.com/skycoin/skywire/pull/1965)
-   fix relative paths issue  [#1964](https://github.com/skycoin/skywire/pull/1964)
-   Reward system cogentcore  [#1963](https://github.com/skycoin/skywire/pull/1963)
-   Fix version parsing with `go run github.com/skycoin/skywire/cmd/skywire@develop`  [#1962](https://github.com/skycoin/skywire/pull/1962)
-   Fix versioning  [#1961](https://github.com/skycoin/skywire/pull/1961)
-   Fix versioning  [#1960](https://github.com/skycoin/skywire/pull/1960)
-   Fix add transport panic #1956  [#1957](https://github.com/skycoin/skywire/pull/1957)
-   transition reward system UI to use cogentcore.org/core [WIP]  [#1953](https://github.com/skycoin/skywire/pull/1953)
-   Fix broken CI test  [#1950](https://github.com/skycoin/skywire/pull/1950)
-   Remove replace directive from go.mod  [#1948](https://github.com/skycoin/skywire/pull/1948)
-   `skywire cli tp add` use address resolver instead of public visor service discovery  [#1947](https://github.com/skycoin/skywire/pull/1947)
-   Fix version issue  [#1944](https://github.com/skycoin/skywire/pull/1944)

## 1.3.29

-   use embbedded configs by default for skywire cli config gen [#1941](https://github.com/skycoin/skywire/pull/1941)
-   Integrate updated versioning into buildinfo  [#1939](https://github.com/skycoin/skywire/pull/1939)
-   Add Version command  [#1938](https://github.com/skycoin/skywire/pull/1938)
-   Move skywire-services and skycoin-service-discovery to skywrie repo [#1937](https://github.com/skycoin/skywrie/pull/1937)
-   Add `--json` flag back to vpn and proxy `list` subcommand  [#1931](https://github.com/skycoin/skywire/pull/1931)
-   Remove UT checking in vpn and skysocks list  [#1930](https://github.com/skycoin/skywire/pull/1930)
-   remove hf from 386 release  [#1929](https://github.com/skycoin/skywire/pull/1929)
-   x86 linux release  [#1928](https://github.com/skycoin/skywire/pull/1928)
-   Update deps  [#1927](https://github.com/skycoin/skywire/pull/1927)
-   improve SetMinHop api method  [#1925](https://github.com/skycoin/skywire/pull/1925)
-   Improve route logic in visor side  [#1921](https://github.com/skycoin/skywire/pull/1921)
-   Update minimum version requirement & reward cutoff date in mainnet_rules.md  [#1920](https://github.com/skycoin/skywire/pull/1920)
-   fix `/log-collection/tree/:pk` route on reward system ui  [#1919](https://github.com/skycoin/skywire/pull/1919)
-   Fix goreleaser configs  [#1918](https://github.com/skycoin/skywire/pull/1918)
-   Win32 Installer  [#1917](https://github.com/skycoin/skywire/pull/1917)
-   Improve Docker scripts  [#1916](https://github.com/skycoin/skywire/pull/1916)
-   Update .gitigonre  [#1915](https://github.com/skycoin/skywire/pull/1915)
-   update deps  [#1914](https://github.com/skycoin/skywire/pull/1914)
-   update vendor deps  [#1913](https://github.com/skycoin/skywire/pull/1913)
-   Replace "github.com/skycoin/skywire-utilities" imports with "github.com/skycoin/skywire/pkg/skywire-utilities"  [#1912](https://github.com/skycoin/skywire/pull/1912)
-   add skywire-utilities libraries  [#1911](https://github.com/skycoin/skywire/pull/1911)
-   Remove `replace` directives from go.mod  [#1906](https://github.com/skycoin/skywire/pull/1906)
-   Add `gocyclo` to local and CI lint  [#1904](https://github.com/skycoin/skywire/pull/1904)
-   Fix Wireguard version  [#1903](https://github.com/skycoin/skywire/pull/1903)
-   joint compilation with skycoin  [#1901](https://github.com/skycoin/skywire/pull/1901)
-   Fix Mac installer issue [#1900](https://github.com/skycoin/skywire/pull/1900)
-   Fix WIX issue on versioning of Windows installer  [#1899](https://github.com/skycoin/skywire/pull/1899)
-   Fix issue on release workflow  [#1896](https://github.com/skycoin/skywire/pull/1896)
-   Improve Mac survey  [#1895](https://github.com/skycoin/skywire/pull/1895)
-   Improve Windows installer [#1894](https://github.com/skycoin/skywire/pull/1894)
-   Reward on Windows  [#1892](https://github.com/skycoin/skywire/pull/1892)
-   Embed scripts for reward system  [#1888](https://github.com/skycoin/skywire/pull/1888)
-   Fix codebase issues  [#1885](https://github.com/skycoin/skywire/pull/1885)
-   Update Reward Calculation Cli & UI  [#1884](https://github.com/skycoin/skywire/pull/1884)
-   Update dependencies  [#1880](https://github.com/skycoin/skywire/pull/1880)
-   Fix dmsghttp config gen logic  [#1877](https://github.com/skycoin/skywire/pull/1877)
-   Fix services-config.json path reference for `skywire cli config gen`  [#1875](https://github.com/skycoin/skywire/pull/1875)

## 1.3.26

-   Embed Deployment Configuration  [#1873](https://github.com/skycoin/skywire/pull/1873)
-   Remove hardcoded services  [#1872](https://github.com/skycoin/skywire/pull/1872)
-   Update dmsg server ip address in dmsghttp-config.json [#1868](https://github.com/skycoin/skywire/pull/1868)

## 1.3.25

-   update skywire-services and skycoin-service-discovery to v1.3.25  [#1864](https://github.com/skycoin/skywire/pull/1864)
-   make ready for v1.3.25  [#1863](https://github.com/skycoin/skywire/pull/1863)
-   update skywire-utilties  [#1862](https://github.com/skycoin/skywire/pull/1862)
-   Update Reward Calculation  [#1859](https://github.com/skycoin/skywire/pull/1859)

## 1.3.24

-   fix Windows installer script  [#1858](https://github.com/skycoin/skywire/pull/1858)
-   fix reward calculation  [#1857](https://github.com/skycoin/skywire/pull/1857)
-   fix survey & reward calculation  [#1856](https://github.com/skycoin/skywire/pull/1856)
-   Fix IP issue on survey  [#1855](https://github.com/skycoin/skywire/pull/1855)
-   Fix skywire cli log / update dmsg dep  [#1853](https://github.com/skycoin/skywire/pull/1853)
-   add windows arm64 archive to release  [#1852](https://github.com/skycoin/skywire/pull/1852)

## 1.3.23

-   Increment minimum version requirement to v1.3.23
-   add windows arm64 archive to release  [#1852](https://github.com/skycoin/skywire/pull/1852)
-   fix bearer token issue  [#1851](https://github.com/skycoin/skywire/pull/1851)
-   fix Windows release pipeline issue  [#1850](https://github.com/skycoin/skywire/pull/1850)
-   update service-config.json values  [#1849](https://github.com/skycoin/skywire/pull/1849)
-   fix missing `skywire` command in MacOS  [#1848](https://github.com/skycoin/skywire/pull/1848)
-   fix datarace in hv  [#1847](https://github.com/skycoin/skywire/pull/1847)
-   update deps  [#1846](https://github.com/skycoin/skywire/pull/1846)
-   add missing dmsg:// to dmsg services addresses  [#1845](https://github.com/skycoin/skywire/pull/1845)
-   build constraint to ignore gotop  [#1844](https://github.com/skycoin/skywire/pull/1844)

## 1.3.22

-   ready for release 5  [#1843](https://github.com/skycoin/skywire/pull/1843)
-   ready for release 4  [#1842](https://github.com/skycoin/skywire/pull/1842)
-   readey for release 3  [#1841](https://github.com/skycoin/skywire/pull/1841)
-   ready to release 2 for v1.3.22  [#1840](https://github.com/skycoin/skywire/pull/1840)
-   ready to release  [#1839](https://github.com/skycoin/skywire/pull/1839)
-   fix release pipeline issues  [#1838](https://github.com/skycoin/skywire/pull/1838)
-   a little change on dmsghttp-config.json  [#1837](https://github.com/skycoin/skywire/pull/1837)
-   replace dmsg disc public key values  [#1836](https://github.com/skycoin/skywire/pull/1836)
-   update deps  [#1835](https://github.com/skycoin/skywire/pull/1835)
-   Include more of the deployment config in the survey  [#1834](https://github.com/skycoin/skywire/pull/1834)
-   update dmsghttp-config.json  [#1833](https://github.com/skycoin/skywire/pull/1833)
-   fix: mac os build error  [#1829](https://github.com/skycoin/skywire/pull/1829)
-   Update Skywire Specifications document  [#1827](https://github.com/skycoin/skywire/pull/1827)
-   minor optimizations to reward system backend and UI  [#1825](https://github.com/skycoin/skywire/pull/1825)
-   Update dmsg  [#1823](https://github.com/skycoin/skywire/pull/1823)
-   Reward System UI improvements  [#1822](https://github.com/skycoin/skywire/pull/1822)
-   add heartbeat logic for stcpr and dmsg  [#1821](https://github.com/skycoin/skywire/pull/1821)
-   Transport setup-node request logging integration with reward system UI  [#1820](https://github.com/skycoin/skywire/pull/1820)
-   improve `skywire cli tp tree`  [#1818](https://github.com/skycoin/skywire/pull/1818)
-   improve `skywire cli tp tree`  [#1817](https://github.com/skycoin/skywire/pull/1817)
-   system monitor - `skywire cli visor top`  [#1813](https://github.com/skycoin/skywire/pull/1813)
-   More statistics for UT & dmsg discovery  [#1812](https://github.com/skycoin/skywire/pull/1812)
-   Reward System Documentation  [#1811](https://github.com/skycoin/skywire/pull/1811)
-   Update CHANGELOG.md  [#1810](https://github.com/skycoin/skywire/pull/1810)

## 1.3.21
-   Update documentation ; increment min version requirement  [#1809](https://github.com/skycoin/skywire/pull/1809)
-   TPD concurrency  [#1808](https://github.com/skycoin/skywire/pull/1808)
-   Add module to ensure the visor is transportable  [#1807](https://github.com/skycoin/skywire/pull/1807)
-   add systemd services  [#1806](https://github.com/skycoin/skywire/pull/1806)
-   Reward System UI  [#1805](https://github.com/skycoin/skywire/pull/1805)
-   Update skywire-services in go.mod  [#1801](https://github.com/skycoin/skywire/pull/1801)
-   Improve manual transport creation logic  [#1800](https://github.com/skycoin/skywire/pull/1800)
-   Fix `skywire cli visor route` subcommand implementation  [#1798](https://github.com/skycoin/skywire/pull/1798)
-   Merge develop into master after release  [#1794](https://github.com/skycoin/skywire/pull/1794)
-   fix skywire cli vpn command  [#1791](https://github.com/skycoin/skywire/pull/1791)
-   fix `go mod vendor` issue  [#1784](https://github.com/skycoin/skywire/pull/1784)
-   `setup-node` config gen  [#1783](https://github.com/skycoin/skywire/pull/1783)
-   Fix small issues   [#1782](https://github.com/skycoin/skywire/pull/1782)
-   fix various issues  [#1781](https://github.com/skycoin/skywire/pull/1781)
-   Fix some little issues before v1.3.20-rc1  [#1780](https://github.com/skycoin/skywire/pull/1780)
-   Fix app launch  [#1778](https://github.com/skycoin/skywire/pull/1778)
-   update README  [#1777](https://github.com/skycoin/skywire/pull/1777)
-   Move to united binary  [#1776](https://github.com/skycoin/skywire/pull/1776)
-   Update skywire-services and skywire-ut  [#1775](https://github.com/skycoin/skywire/pull/1775)
-   IPC issue on Windows  [#1771](https://github.com/skycoin/skywire/pull/1771)
-   Increment minimum version requirement  [#1766](https://github.com/skycoin/skywire/pull/1766)
-   change app subcommand names back to full app name  [#1763](https://github.com/skycoin/skywire/pull/1763)
-   new ConnectedServerType in config | little improve  [#1760](https://github.com/skycoin/skywire/pull/1760)
-   Fix `proxy start` and `proxy stop`  [#1757](https://github.com/skycoin/skywire/pull/1757)
-   Fix `skywire cli  config gen -pd`  [#1755](https://github.com/skycoin/skywire/pull/1755)
-   Improve help menus ; update vendor deps  [#1752](https://github.com/skycoin/skywire/pull/1752)
-   Fix setup-node help menu in skywire-deployment subcommands  [#1751](https://github.com/skycoin/skywire/pull/1751)
-   Merged Docs  [#1750](https://github.com/skycoin/skywire/pull/1750)
-   Update skywire-services and skycoin-service-discovery deps  [#1749](https://github.com/skycoin/skywire/pull/1749)
-   Fix flag on skysocks-client  [#1748](https://github.com/skycoin/skywire/pull/1748)
-   Fix skychat flag issue  [#1747](https://github.com/skycoin/skywire/pull/1747)
-   update skywire-deployment  [#1746](https://github.com/skycoin/skywire/pull/1746)
-   Add dmsg client type  [#1745](https://github.com/skycoin/skywire/pull/1745)
-   Update stun servers IPs  [#1744](https://github.com/skycoin/skywire/pull/1744)
-   `skywire cli rtree`  [#1743](https://github.com/skycoin/skywire/pull/1743)
-   Clean Codebase  [#1740](https://github.com/skycoin/skywire/pull/1740)
-   fix linux goreleaser config issues  [#1739](https://github.com/skycoin/skywire/pull/1739)
-   fix linux release pipeline  [#1738](https://github.com/skycoin/skywire/pull/1738)
-   Add skywire-deployment release archives  [#1737](https://github.com/skycoin/skywire/pull/1737)
-   Fix v1.3.17 survey collection  [#1736](https://github.com/skycoin/skywire/pull/1736)
-   Fix `skywire-cli config gen --all`  [#1735](https://github.com/skycoin/skywire/pull/1735)
-   Fix offline config gen  [#1734](https://github.com/skycoin/skywire/pull/1734)
-   Fix VPN issue on Windows  [#1729](https://github.com/skycoin/skywire/pull/1729)


## 1.3.17
-   Add http-proxy on skysocks-client  [#1728](https://github.com/skycoin/skywire/pull/1728)
-   Little Improve on skywire and setup-node  [#1723](https://github.com/skycoin/skywire/pull/1723)
-   Improve VPN and Proxy cli command  [#1722](https://github.com/skycoin/skywire/pull/1722)
-   Improve Survey and Log Collection  [#1721](https://github.com/skycoin/skywire/pull/1721)
-   Server list optimization  [#1720](https://github.com/skycoin/skywire/pull/1720)
-   Fix reward calc  [#1719](https://github.com/skycoin/skywire/pull/1719)
-   Fix reward calculation  [#1716](https://github.com/skycoin/skywire/pull/1716)
-   Fix log collection panic  [#1711](https://github.com/skycoin/skywire/pull/1711)
-   Fix win installer script  [#1706](https://github.com/skycoin/skywire/pull/1706)

## 1.3.16

-   fix VPN issues on CI and Windows  [#1703](https://github.com/skycoin/skywire/pull/1703)
-   fix logic of close app  [#1702](https://github.com/skycoin/skywire/pull/1702)

## 1.3.15

-   Update minimum version requirement in mainnet rules [#1699](https://github.com/skycoin/skywire/pull/1699)
-   `riscv64` archive structure  [#1698](https://github.com/skycoin/skywire/pull/1698)
-   update golangci-lint  [#1697](https://github.com/skycoin/skywire/pull/1697)
-   fix enable vpn server environmental variable detection for config gen  [#1695](https://github.com/skycoin/skywire/pull/1695)

## 1.3.14

-   improve postinstall script on Mac installer  [#1691](https://github.com/skycoin/skywire/pull/1691)
-   add skysocks client to windows archive  [#1690](https://github.com/skycoin/skywire/pull/1690)
-   add missed apps to package installer  [#1689](https://github.com/skycoin/skywire/pull/1689)
-   correct rewards update cutoff date  [#1688](https://github.com/skycoin/skywire/pull/1688)
-   update dmsghttp-config.json file  [#1687](https://github.com/skycoin/skywire/pull/1687)
-   Update Mainnet rules minimum version requirement  [#1686](https://github.com/skycoin/skywire/pull/1686)
-   Rebuild Hypervisor UI  [#1685](https://github.com/skycoin/skywire/pull/1685)
-   Update skywire-cli README.md  [#1684](https://github.com/skycoin/skywire/pull/1684)
-   new flag `--confpath` on generate config  [#1683](https://github.com/skycoin/skywire/pull/1683)
-   improve `skywire-cli proxy` command  [#1680](https://github.com/skycoin/skywire/pull/1680)
-   improve `skywire-cli ut` command logic  [#1679](https://github.com/skycoin/skywire/pull/1679)
-   update dmsg and skywire-utilities  [#1677](https://github.com/skycoin/skywire/pull/1677)
-   Fix/update skywire utilities  [#1676](https://github.com/skycoin/skywire/pull/1676)
-   Rebuild Hypervisor UI  [#1672](https://github.com/skycoin/skywire/pull/1672)
-   Fix two panic issues on `skywire-cli` commands  [#1671](https://github.com/skycoin/skywire/pull/1671)
-   Improve UI and Backend on `reboot` and `turn off`  [#1670](https://github.com/skycoin/skywire/pull/1670)

## 1.3.10 - 1.3.13

-   add skywire version to `skywire-cli log`  [#1669](https://github.com/skycoin/skywire/pull/1669)
-   `skywire-cli reward calc`  [#1668](https://github.com/skycoin/skywire/pull/1668)
-   fix issue fetching data from hardcoded server  [#1666](https://github.com/skycoin/skywire/pull/1666)
-   replace dmsgget with dmsgcurl lib  [#1663](https://github.com/skycoin/skywire/pull/1663)
-   Fix wrong config generation with t flag  [#1661](https://github.com/skycoin/skywire/pull/1661)
-   Remove Comments  [#1658](https://github.com/skycoin/skywire/pull/1658)
-   Develop  [#1655](https://github.com/skycoin/skywire/pull/1655)
-   Update minimum version requirement in mainnet rules  [#1654](https://github.com/skycoin/skywire/pull/1654)
-   Fix transport bandwidth logs  [#1653](https://github.com/skycoin/skywire/pull/1653)
-   fix `ping` command logic  [#1652](https://github.com/skycoin/skywire/pull/1652)
-   improve ping command output  [#1651](https://github.com/skycoin/skywire/pull/1651)
-   Update Mainnet rules ; increment minimum version requirement  [#1650](https://github.com/skycoin/skywire/pull/1650)
-   improve `skywire-cli log` command  [#1648](https://github.com/skycoin/skywire/pull/1648)
-   UI improvements  [#1647](https://github.com/skycoin/skywire/pull/1647)
-   v1.3.11  [#1645](https://github.com/skycoin/skywire/pull/1645)
-    source a .conf file with skywire-cli config gen  [#1644](https://github.com/skycoin/skywire/pull/1644)
-   UI for turning off the visor  [#1642](https://github.com/skycoin/skywire/pull/1642)
-   Update dmsghttp config  [#1632](https://github.com/skycoin/skywire/pull/1632)
-   Use `MarkFlagsMutuallyExclusive` for `config gen -rf` and `config gen -up`  [#1629](https://github.com/skycoin/skywire/pull/1629)
-   [WIP] dmsghttp updater  [#1628](https://github.com/skycoin/skywire/pull/1628)
-   Vendor dmsg@master and replace two yamux deps  [#1626](https://github.com/skycoin/skywire/pull/1626)
-   `skywire-cli log` collect transport bandwidth logging for today  [#1625](https://github.com/skycoin/skywire/pull/1625)
-   reduce `transport_manager` timeout on module shutting down  [#1624](https://github.com/skycoin/skywire/pull/1624)
-   collect surveys from online visors only  [#1623](https://github.com/skycoin/skywire/pull/1623)
-   Improvements for the UI  [#1620](https://github.com/skycoin/skywire/pull/1620)
-   Add details to mainnet rules article  [#1618](https://github.com/skycoin/skywire/pull/1618)
-   add new flag to log command  [#1617](https://github.com/skycoin/skywire/pull/1617)
-   improve log collection logic  [#1615](https://github.com/skycoin/skywire/pull/1615)
-   update dependencies  [#1614](https://github.com/skycoin/skywire/pull/1614)
-   fix GitHub Action  [#1613](https://github.com/skycoin/skywire/pull/1613)
-   `skywire-cli config gen -r` test  [#1611](https://github.com/skycoin/skywire/pull/1611)
-   Limit the Skychay UI to localhost  [#1605](https://github.com/skycoin/skywire/pull/1605)
-   Add CSRF protection to the Hypervisor API  [#1604](https://github.com/skycoin/skywire/pull/1604)
-   fix dockerhub username and token  [#1601](https://github.com/skycoin/skywire/pull/1601)
-   Fix/makefile clean target  [#1600](https://github.com/skycoin/skywire/pull/1600)
-   Merge Develop to Master  [#1599](https://github.com/skycoin/skywire/pull/1599)
-   Add dmsghttp servers  [#1597](https://github.com/skycoin/skywire/pull/1597)
-   fix release issue on mac and win  [#1593](https://github.com/skycoin/skywire/pull/1593)
-   remove .asc file from archives of release  [#1592](https://github.com/skycoin/skywire/pull/1592)
-   Fix `skywire-cli config gen -r`  [#1591](https://github.com/skycoin/skywire/pull/1591)
-   Merge develop to master  [#1589](https://github.com/skycoin/skywire/pull/1589)
-   Remove pgp key previously used for survey collection on earlier versions  [#1588](https://github.com/skycoin/skywire/pull/1588)
-   Update Command Documentation  [#1587](https://github.com/skycoin/skywire/pull/1587)
-   Fix config gen logic for fetching services & erroneous app config  [#1586](https://github.com/skycoin/skywire/pull/1586)
-   change json of service struct to transport_setup  [#1585](https://github.com/skycoin/skywire/pull/1585)
-   fix wrong variable name for config gen  [#1584](https://github.com/skycoin/skywire/pull/1584)

## 1.3.9

-   Fix `skywire-cli config gen -r`

## 1.3.8

-   Rebuild Hypervisor UI  [#1583](https://github.com/skycoin/skywire/pull/1583)
-   Change Logserver to use c.JSON method ; remove variable for endpoint name '/node-info'  [#1582](https://github.com/skycoin/skywire/pull/1582)
-   update changelog  [#1580](https://github.com/skycoin/skywire/pull/1580)
-   Add Config gen flags for survey whitelist, transport and route setup pks [#1578](https://github.com/skycoin/skywire/pull/1578)
-   Fix query of the conf service [#1578](https://github.com/skycoin/skywire/pull/1578)
-   Revise config gen logic / structure [#1578](https://github.com/skycoin/skywire/pull/1578)
-   Fix the js mime type  [#1576](https://github.com/skycoin/skywire/pull/1576)
-   Fix two new panic detected  [#1573](https://github.com/skycoin/skywire/pull/1573)
-   Health check of log collection api prerequisite for survey & transport log collection via `skywire-cli log`  [#1568](https://github.com/skycoin/skywire/pull/1568)
-   Fix typo on state name  [#1567](https://github.com/skycoin/skywire/pull/1567)
-   Log collection by secret key  [#1566](https://github.com/skycoin/skywire/pull/1566)
-   Optional combined compilation of `skywire-cli` `skywire-visor` & `setup-node` binaries  [#1565](https://github.com/skycoin/skywire/pull/1565)
-   Fix vpn start command  [#1564](https://github.com/skycoin/skywire/pull/1564)
-   Solve setupnode rpc issue  [#1563](https://github.com/skycoin/skywire/pull/1563)
-   Remove pgp encryption of the survey  [#1562](https://github.com/skycoin/skywire/pull/1562)
-   Change variable name  [#1561](https://github.com/skycoin/skywire/pull/1561)
-   add WhitelistedPKs to services struct  [#1560](https://github.com/skycoin/skywire/pull/1560)
-   Survey collection whitelist  [#1557](https://github.com/skycoin/skywire/pull/1557)
-   Dmsgpty whitelist  [#1554](https://github.com/skycoin/skywire/pull/1554)
-   `make config` directive & makefile optimizations  [#1549](https://github.com/skycoin/skywire/pull/1549)
-   Update skywire-utilities dependency   [#1546](https://github.com/skycoin/skywire/pull/1546)
-   fix `skywire-cli vpn list` [#1546](https://github.com/skycoin/skywire/pull/1546)
-   Randomize the order of survey collection with `skywire-cli log`  [#1541](https://github.com/skycoin/skywire/pull/1541)
-   Fix skywire-cli config gen -a  [#1539](https://github.com/skycoin/skywire/pull/1539)
-   Print version of golangci-lint with `make check`  [#1538](https://github.com/skycoin/skywire/pull/1538)
-   Change port logic on sudph and stcpr init - set ports for sudph and stcpr  [#1534](https://github.com/skycoin/skywire/pull/1534)
-   Stop UI requests when not needed ; avoid unnecessary logging  [#1533](https://github.com/skycoin/skywire/pull/1533)
-   Remove survey checksum  [#1532](https://github.com/skycoin/skywire/pull/1532)
-   Update README.md  [#1531](https://github.com/skycoin/skywire/pull/1531)
-   Rebuild UI  [#1530](https://github.com/skycoin/skywire/pull/1530)
-   Logs UI  [#1528](https://github.com/skycoin/skywire/pull/1528)
-   Change bin_path to apps instead build/apps  [#1526](https://github.com/skycoin/skywire/pull/1526)
-   Fix for survey on armv7 [#1524](https://github.com/skycoin/skywire/pull/1524)
-   Fix mac installer script issue  [#1523](https://github.com/skycoin/skywire/pull/1523)
-   Fix arm log store panic  [#1522](https://github.com/skycoin/skywire/pull/1522)

## 1.3.7
-   Fix deps (dependabot)  [#1521](https://github.com/skycoin/skywire/pull/1521)
-   improve transport logic  [#1519](https://github.com/skycoin/skywire/pull/1519)
-   survey issue arm7 hotfix  [#1518](https://github.com/skycoin/skywire/pull/1518)
-   Change build path of binaries to build folder  [#1516](https://github.com/skycoin/skywire/pull/1516)
-   Cli refactor  [#1515](https://github.com/skycoin/skywire/pull/1515)
-   update readme  [#1514](https://github.com/skycoin/skywire/pull/1514)
-   risc-v build  [#1513](https://github.com/skycoin/skywire/pull/1513)
-   Bump golang.org/x/text from 0.3.7 to 0.3.8  [#1511](https://github.com/skycoin/skywire/pull/1511)
-   Bump golang.org/x/net from 0.0.0-20220722155237-a158d28d115b to 0.7.0  [#1510](https://github.com/skycoin/skywire/pull/1510)
-   Bump golang.org/x/crypto from 0.0.0-20210921155107-089bfa567519 to 0.1.0  [#1509](https://github.com/skycoin/skywire/pull/1509)

## 1.3.6
-   Hot fix on Launcher panic  [#1508](https://github.com/skycoin/skywire/pull/1508)
-   improve logging  [#1507](https://github.com/skycoin/skywire/pull/1507)
-   Fix appL nil  [#1506](https://github.com/skycoin/skywire/pull/1506)
-   fix docker login  [#1503](https://github.com/skycoin/skywire/pull/1503)

## 1.3.5
-   build UI for v1.3.5  [#1502](https://github.com/skycoin/skywire/pull/1502)
-   remove `exec` and `profile`  [#1501](https://github.com/skycoin/skywire/pull/1501)
-   Update change log for patch release  [#1500](https://github.com/skycoin/skywire/pull/1500)
-   Remove the basic terminal from the UI  [#1499](https://github.com/skycoin/skywire/pull/1499)
-   Improvements for the UI code  [#1498](https://github.com/skycoin/skywire/pull/1498)
-   log collection size limit | improve survey logic  [#1496](https://github.com/skycoin/skywire/pull/1496)
-   Custom Apps  [#1495](https://github.com/skycoin/skywire/pull/1495)
-   improve survey logic  [#1489](https://github.com/skycoin/skywire/pull/1489)

## 1.3.4
-   Uncomment Profiler [#1480](https://github.com/skycoin/skywire/pull/1480)
-   update changelog  [#1480](https://github.com/skycoin/skywire/pull/1480)
-   update release pipeline to include skycoin.asc in release.  [#1480](https://github.com/skycoin/skywire/pull/1480)
-   improve survey & encrypt to skycoin.asc  [#1479](https://github.com/skycoin/skywire/pull/1479)
-   add key to repo for survey encryption  [#1478](https://github.com/skycoin/skywire/pull/1478)
-   fix for go 1.19  [#1477](https://github.com/skycoin/skywire/pull/1477)
-   rebuild ui  [#1476](https://github.com/skycoin/skywire/pull/1476)
-   Improvements for the UI  [#1471](https://github.com/skycoin/skywire/pull/1471)
-   improve on survey and log collecting  [#1470](https://github.com/skycoin/skywire/pull/1470)
-   Hide the update options  [#1469](https://github.com/skycoin/skywire/pull/1469)
-   improve log collecting logic  [#1466](https://github.com/skycoin/skywire/pull/1466)
-   Send visor version on update uptime  [#1465](https://github.com/skycoin/skywire/pull/1465)

## 1.3.3
-   remove autopeering  [#1463](https://github.com/skycoin/skywire/pull/1463)
-   rebuild ui after updates  [#1461](https://github.com/skycoin/skywire/pull/1461)
-   Use Angular Material MDC components  [#1460](https://github.com/skycoin/skywire/pull/1460)
-   move from AppVeyor to Github Action  [#1459](https://github.com/skycoin/skywire/pull/1459)
-   update changelog with recently merged PRs  [#1456](https://github.com/skycoin/skywire/pull/1456)
-   added `skywire-cli skysocksc` command ; cli interface for controlling skysocks [#1455](https://github.com/skycoin/skywire/pull/1455)
-   add ServiceTypeProxy to servicedisc types [#1454](https://github.com/skycoin/skywire/pull/1454)
-   rebuild UI  [#1453](https://github.com/skycoin/skywire/pull/1453)
-   Fix for the skysocks UI  [#1452](https://github.com/skycoin/skywire/pull/1452)

## 1.3.2
-   rebuild UI  [#1450](https://github.com/skycoin/skywire/pull/1450)
-   Update documentation  [#1448](https://github.com/skycoin/skywire/pull/1448)

## 1.3.0
-   change transport_logs folder to 755 permissions & various similar fixes  [#1447](https://github.com/skycoin/skywire/pull/1447)
-   Fix warn logs  [#1446](https://github.com/skycoin/skywire/pull/1446)
-   move survey generation to its own goroutine  [#1445](https://github.com/skycoin/skywire/pull/1445)
-   rebuild UI  [#1444](https://github.com/skycoin/skywire/pull/1444)
-   add hypervisor UI integration for managing the reward address [#1442](https://github.com/skycoin/skywire/pull/1442)
-   update mainnet rules for collecting rewards under the new system [#1443](https://github.com/skycoin/skywire/pull/1443)
-   omit all language differentiating types of miners (official, DIY) from mainnet_rules.md [#1443](https://github.com/skycoin/skywire/pull/1443)
-   omit references to the whitelist, which will be deprecated, from mainnet_rules.md [#1443](https://github.com/skycoin/skywire/pull/1443)
-   add description of reward tiers to mainnet_rules.md [#1443](https://github.com/skycoin/skywire/pull/1443)
-   add description of how the new reward system will work to mainnet_rules.md [#1443](https://github.com/skycoin/skywire/pull/1443)
-   Increment minimum required skywire version for rewards to 1.3.0 in mainnet_rules.md [#1443](https://github.com/skycoin/skywire/pull/1443)
-   Fix delete reward file [#1441](https://github.com/skycoin/skywire/pull/1441)
-   Show reward address on autoconfig [#1441](https://github.com/skycoin/skywire/pull/1441)
-   disable public autoconnect logic for `config gen -b` [#1440](https://github.com/skycoin/skywire/pull/1440)
-   Add changelog generation script [#1439](https://github.com/skycoin/skywire/pull/1439)
-   Fix GetRewardAddress API  [#1438](https://github.com/skycoin/skywire/pull/1438)
-   fix release issues  [#1432](https://github.com/skycoin/skywire/pull/1432) [#1434](https://github.com/skycoin/skywire/pull/1434) [#1433](https://github.com/skycoin/skywire/pull/1433)
-   built tag for non-systray skywire-visor  [#1429](https://github.com/skycoin/skywire/pull/1429)
-   visor test subcommand  [#1428](https://github.com/skycoin/skywire/pull/1428)
-   Update to Angular 15  [#1426](https://github.com/skycoin/skywire/pull/1426)
-   Hot fix on DNS  [#1425](https://github.com/skycoin/skywire/pull/1425)
-   update dmsg@develop  [#1423](https://github.com/skycoin/skywire/pull/1423)
-   Selected DMSG Server  [#1422](https://github.com/skycoin/skywire/pull/1422)
-   Integrated Autoconfig  [#1417](https://github.com/skycoin/skywire/pull/1417)
-   Update Angular to v14.2.11  [#1416](https://github.com/skycoin/skywire/pull/1416)
-   skywire-cli log collecting command  [#1414](https://github.com/skycoin/skywire/pull/1414)
-   App/Services showing ports subcommand `skywire-cli visor ports`  [#1412](https://github.com/skycoin/skywire/pull/1412)
-   skywire app example  [#1409](https://github.com/skycoin/skywire/pull/1409)
-   `skywire-cli doc` command & cli documentation update  [#1408](https://github.com/skycoin/skywire/pull/1408)
-   fixing skywire-cli reward freezing issue  [#1407](https://github.com/skycoin/skywire/pull/1407)
-   Improve readme documentation  [#1406](https://github.com/skycoin/skywire/pull/1406)
-   Add cli command visor ping and test  [#1405](https://github.com/skycoin/skywire/pull/1405)
-   build ui  [#1403](https://github.com/skycoin/skywire/pull/1403)
-   fix `make format check` errors  [#1401](https://github.com/skycoin/skywire/pull/1401)
-   Fix control visor apps from hv  [#1399](https://github.com/skycoin/skywire/pull/1399)
-   Bug fixes for the UI  [#1398](https://github.com/skycoin/skywire/pull/1398)
-   run as systray flag `--systray`  [#1396](https://github.com/skycoin/skywire/pull/1396)
-   fix panic and datarace  [#1394](https://github.com/skycoin/skywire/pull/1394)
-   Printing new IP after connecting to VPN in CLI  [#1393](https://github.com/skycoin/skywire/pull/1393)
-   Add display node ip field to the main config  [#1392](https://github.com/skycoin/skywire/pull/1392)
-   re-implement setting reward address  [#1391](https://github.com/skycoin/skywire/pull/1391)
-   skywire-cli terminal user interface improvements  [#1390](https://github.com/skycoin/skywire/pull/1390)
-   improve `skywire-cli vpn` subcommand  [#1389](https://github.com/skycoin/skywire/pull/1389)
-   Fix transport logging  [#1386](https://github.com/skycoin/skywire/pull/1386)
-   fix cli config priv flags  [#1384](https://github.com/skycoin/skywire/pull/1384)
-   Add param customCommand for PtyUI.Handler  [#1383](https://github.com/skycoin/skywire/pull/1383)
-   add Info field to Service struct  [#1382](https://github.com/skycoin/skywire/pull/1382)
-   Add DNS to TUN, in VPN-Client  [#1381](https://github.com/skycoin/skywire/pull/1381)
-   Improve systray VPN button initialization  [#1380](https://github.com/skycoin/skywire/pull/1380)
-   fix privacyjson  [#1379](https://github.com/skycoin/skywire/pull/1379)
-   Update transport file logging  [#1376](https://github.com/skycoin/skywire/pull/1376)
-   Update LocalIPs field in model Service  [#1375](https://github.com/skycoin/skywire/pull/1375)
-   expose dmsghttp server  [#1374](https://github.com/skycoin/skywire/pull/1374)
-   Fix negative waitgroup  [#1372](https://github.com/skycoin/skywire/pull/1372)
-   `skywire-cli config priv` subcommand  [#1369](https://github.com/skycoin/skywire/pull/1369)
-   fix absence of git in makefile  [#1368](https://github.com/skycoin/skywire/pull/1368)
-   Fix rpc error in cli for json  [#1367](https://github.com/skycoin/skywire/pull/1367)
-   Fix StartVPNCient logic  [#1366](https://github.com/skycoin/skywire/pull/1366)

## 1.2.0

### Added
- `skywire-cil visor hv` subcommand [#1390](https://github.com/skycoin/skywire/pull/1390)
- info field to Service struct [#1382](https://github.com/skycoin/skywire/pull/1382)
- `skywire-cli` subcommand `arg` under `visor app` [#1356](https://github.com/skycoin/skywire/pull/1356)
- `log_store` field to `transport` in config [#1386](https://github.com/skycoin/skywire/pull/1386)
- `type`, `location`, `rotation_interval`, field to `log_store` inside `transport` in config [#1374](https://github.com/skycoin/skywire/pull/1374)
- transport file logging to CSV [#1374](https://github.com/skycoin/skywire/pull/1374)
- `skywire-cli config priv` & `skywire-cli visor priv` subcommands and rpc [#1369](https://github.com/skycoin/skywire/issues/1369)
- dmsghttp server [#1364](https://github.com/skycoin/skywire/issues/1364)
- `display_node_ip` field to `launcher` in config [#1392](https://github.com/skycoin/skywire/pull/1392)

### Changed
- moved `skywire-cli visor` subcommands into `skywire-cil visor hv` [#1390](https://github.com/skycoin/skywire/pull/1390)
- use flags for `skywire-cli visor route` & `skywire-cli visor tp` [#1390](https://github.com/skycoin/skywire/pull/1390)
- moved `skywire-cli` subcommand `autoconnect` from `visor app` to `visor app arg` [#1356](https://github.com/skycoin/skywire/pull/1356)

### Fixed
- negative waitgroup  [#1372](https://github.com/skycoin/skywire/pull/1372)
- absence of git in makefile  [#1368](https://github.com/skycoin/skywire/pull/1368)
- rpc error in cli for json [#1367](https://github.com/skycoin/skywire/pull/1367)
- StartVPNCient logic [#1366](https://github.com/skycoin/skywire/pull/1366)

## 1.1.0

### Added

- `skywire-cli` global flag `--json` [#1346](https://github.com/skycoin/skywire/pull/1346)
- service discovery query filtering for `skywire-cli vpn list`	[#1337](https://github.com/skycoin/skywire/pull/1337)
- `skywire-cli vpn` subcommands	[#1317](https://github.com/skycoin/skywire/pull/1317)
- separate systray application which uses `skywire-cli vpn` subcommands	[#1317](https://github.com/skycoin/skywire/pull/1317)
- port of the autopeering system from skybian to the skywire source code.  [#1309](https://github.com/skycoin/skywire/pull/1309)
- `-l --hvip` and `-m --autopeer` flags for `skywire-visor` ; connect to a hypervisor by ip address.  [#1309](https://github.com/skycoin/skywire/pull/1309)
- `skywire-cli visor pk -w` flag ; http endpoint for visor public key [#1309](https://github.com/skycoin/skywire/pull/1309)
- `-y --autoconn` and `-z --ispublic` flags for `skywire-cli config gen` [#1319](https://github.com/skycoin/skywire/pull/1319)
- error packet to routes to propagate route errors [#1181](https://github.com/skycoin/skywire/issues/1181)
- `skywire-cli chvpk` subcommand to list remote hypervisor(s) a visor is currently connected to [#1306](https://github.com/skycoin/skywire/issues/1306)
- pong packet to send as a response to ping to calculate latency [#1261](https://github.com/skycoin/skywire/issues/1261)
- store UI settings per hypervisor key [#1329](https://github.com/skycoin/skywire/pull/1329)

### Changed

- `skywire-cli visor route add-rule` subcommands [#1346](https://github.com/skycoin/skywire/pull/1346)
- Autopeer on env `AUTOPEER=1`
- improve UI reaction while system is busy
- hide password options in UI if authentication is disabled
- fix freezing hypervisor UI on hypervisor disconnection [#1321](https://github.com/skycoin/skywire/issues/1321)
- fix route setup hooks to check if transport to remote is established [#1297](https://github.com/skycoin/skywire/issues/1297)
- rename network probe packet to ping [#1261](https://github.com/skycoin/skywire/issues/1261)
- added Value/Scan method to SWAddr for using in DB directly
- added new fields (ID, CreatedAT) to Service type for using in DB directly
- fixed entrypoint.sh for Dockerfile [#1336](https://github.com/skycoin/skywire/pull/1336)

### Removed

- `skywire-cli visor tp add` flag `--public` [#1346](https://github.com/skycoin/skywire/pull/1346)
- remove updater settings from UI

### Fixed
- UI update button [#1349](https://github.com/skycoin/skywire/pull/1349)

## 1.0.0

### Added

- `skywire-cli hv` subcommands for opening the various UIs or printing links to them (HVUI, VPNUI, DMSGPTYUI) [#1270](https://github.com/skycoin/skywire/pull/1270)
- added `add-rhv` and `disable-rhv` flags to `skywire-visor` for adding remote hypervisor PK and disable remote hypervisor PK(s) on config file [#1113](https://github.com/skycoin/skywire/pull/1113)
- shorthand flags for commands [#1151](https://github.com/skycoin/skywire/pull/1151)
- blue & white color scheme with coloredcobra [#1151](https://github.com/skycoin/skywire/pull/1151)
- ascii art text modal of program name to help menus [#1151](https://github.com/skycoin/skywire/pull/1151)
- `--all` flag to skywire-cli & visor to show extra flags [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen -n --stdout` write config to stdout [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen   -w, --hide` dont print the config to the terminal [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen --print` parse test ; read config from file & print [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli config gen   -a, --url` services conf (default "conf.skywire.skycoin.com") [#1151](https://github.com/skycoin/skywire/pull/1151)
- fetch service from endpoint [#1151](https://github.com/skycoin/skywire/pull/1151)
- `skywire-cli visor app` app settings command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor route` view and set rules command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor tp` view and set transports command [#1132](https://github.com/skycoin/skywire/pull/1132)
- `skywire-cli visor vpn` vpn interface command [#1132](https://github.com/skycoin/skywire/pull/1132)
- root permissions detection
- error on different version config / visor
- display update command on config version error
- support for piping config generated by skywire-cli to skywire-visor via stdin [#1147](https://github.com/skycoin/skywire/pull/1147)
- support for detecting skywire version when `go run`
- `run-vpnsrv` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-dmsghttp` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-source-dmsghttp-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv-dmsghttp` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `run-vpnsrv -dmsghttp-test` makefile directive [#1147](https://github.com/skycoin/skywire/pull/1147)
- `install-system-linux` and `install-system-linux-systray` makefile directives [#1180](https://github.com/skycoin/skywire/pull/1180)
- `skywire-cli dmsgpty list` to view of connected remote visor to hypervisor [#1250](https://github.com/skycoin/skywire/pull/1250)
- `skywire-cli dmsgpty start <pk>` to connect through dmsgpty to remote visor [#1250](https://github.com/skycoin/skywire/pull/1250)
- `make win-installer-latest` to create installer for latest version of released, not pre-release.
- `trace` log level is added
- `--log-level` flag to generate and update config by `skywire-cli`

### Changed
- remove dsmghttp migration to skywire-visor starting
- only support current version of config
- config version reflects current visor version (`1.0.0`)
- refine and restructure help commands user interface
- shorthand flags for commands
- group skywire-cli visor subcommands
- hide excess flags
- make help text fit within default 80x24 terminal
- rename `skywire-cli config gen -r --replace` flag to `-r --regen`
- remove config path from V1 struct
- remove all instance of the visor writing to the config file except via api
- remove path to dmsghttp-config.json from config
- revise versioning
- move to skyenv
- remove transports cache from visor initialization and check them before make route
- `run-source` makefile directive write config to stdout & read config from stdin
- fixed skywire-visor uses skywire-config.json (default config name) without needing to specify
- `make win-installer` need new argument `CUSTOM_VERSION` to get make installer for this version, use for pre-releases
- changed the log levels of most of the logs making info level clutter free

### Removed

- inbuilt updater ; instead use packages and the system package manger for installation and updates [#1251](https://github.com/skycoin/skywire/pull/1251)

## 0.6.0


### Added

- added `update` and `summary` as subcommand to `skywire-cli visor`
- added multiple new flag to update configuration in `skywire-cli config update`
- added shell autocompletion command to `skywire-cli` and `skywire-visor`
- added `dsmgHTTPStruct` in visorconfig pkg to usable other repos, such as `skybian`
- added `dmsghttp-config.json` which contains the `dmsg-urls` of services and info of `dmsg-servers` for both prod and test
- added `servers` filed to `dmsg` in config
- added `-d,--dmsghttp` flag to `skywire-cli config gen`
- added `dmsgdirect` client to connect to services over dmsg
- added `-f` flag to skywire-visor to configure a visor to expose hypervisor UI with default values at runtime
- added `--public-rpc` falg to `skywire-cli config gen`
- added `--vpn-server-enable` falg to `skywire-cli config gen`
- added `--os` flag to `skywire-cli config gen`
- added `--disable-apps` flag to `skywire-cli config gen`
- added `--disable-auth` and `--enable-auth` flags to `skywire-cli config gen`
- added `--best-protocol` flag to `skywire-cli config gen`
- added `skywire-cli visor vpn-ui` and `skywire-cli visor vpn-url` commands
- added dsmghttp migration to skywire-visor starting
- added network monitor PKs to skyenv

### Changed

- detecting OS in runtime removed
- skybian flag `-s` removed from `skywire-cli config gen`
- migrate updating logic to debian package model

## 0.5.0

### Added

- added persistent_transports field to the config and UI
- added stun_servers field to the config
- added is_public field to root section
- added public_autoconnect field to transport section
- added transport_setup_nodes field to transport section
- added MinHops field to V1Routing section of config
- added `skywire-cli config` subcommand
- added connection_duration field to `/api/visor/{pk}/apps/vpn-client/connections`

### Changed

- config updated to `v1.1.0`
- removed public_trusted_visor field from root section
- removed trusted_visors field from transport section
- removed authorization_file field from dmsgpty section
- changed default urls to newer shortned ones
- changed proxy_discovery_addr field to service_discovery
- updated UI
- removed `--public` flag from `skywire-cli visor add-tp` command
- removed `skywire-cli visor gen-config` and `skywire-cli visor update-config` subcommands.
- replaced stcp field to skywire-tcp in config and comments
- replaced local_address field to listening_address in config
- replaced port field to dmsg_port in config
- updated visor health status checks, no longer querying multiple external services endpoints.

## 0.2.1 - 2020.04.07

### Changed

- reverted port changes for `skysocks-client`

## 0.2.0 - 2020.04.02

### Added

- added `--retain-keys` flag to `skywire-cli visor gen-config` command
- added `--secret-key` flag to `skywire-cli visor gen-config` command
- added hypervisorUI frontend
- added default values for visor if certain fields of config are empty

### Fixed

- fixed deployment route finder HTTP request
- fixed /user endpoint not working when auth is disabled

### Changed

- changed port of hypervisorUI and applications
- replaced unix sockets for app to visor communication to tcp sockets
- reverted asynchronous sending of router packets

## 0.1.0 - 2020.04.02

First release of Skywire Mainnet.


## COMPLETE LOG
-   improve survey  [#1479](https://github.com/skycoin/skywire/pull/1479)
-   add key to repo for survey encryption  [#1478](https://github.com/skycoin/skywire/pull/1478)
-   fix for go 1.19  [#1477](https://github.com/skycoin/skywire/pull/1477)
-   rebuild ui  [#1476](https://github.com/skycoin/skywire/pull/1476)
-   Improvements for the UI  [#1471](https://github.com/skycoin/skywire/pull/1471)
-   improve on survey and log collecting  [#1470](https://github.com/skycoin/skywire/pull/1470)
-   Hide the update options  [#1469](https://github.com/skycoin/skywire/pull/1469)
-   improve log collecting logic  [#1466](https://github.com/skycoin/skywire/pull/1466)
-   Send visor version on update uptime  [#1465](https://github.com/skycoin/skywire/pull/1465)
-   remove autopeering  [#1463](https://github.com/skycoin/skywire/pull/1463)
-   rebuild ui after updates  [#1461](https://github.com/skycoin/skywire/pull/1461)
-   Use Angular Material MDC components  [#1460](https://github.com/skycoin/skywire/pull/1460)
-   move from AppVeyor to Github Action  [#1459](https://github.com/skycoin/skywire/pull/1459)
-   update changelog with recently merged PRs  [#1456](https://github.com/skycoin/skywire/pull/1456)
-   skysocksc command  [#1455](https://github.com/skycoin/skywire/pull/1455)
-   add ServiceTypeProxy to servicedisc types  [#1454](https://github.com/skycoin/skywire/pull/1454)
-   rebuild UI  [#1453](https://github.com/skycoin/skywire/pull/1453)
-   Fix for the skysocks UI  [#1452](https://github.com/skycoin/skywire/pull/1452)
-   rebuild UI  [#1450](https://github.com/skycoin/skywire/pull/1450)
-   Update documentation  [#1448](https://github.com/skycoin/skywire/pull/1448)
-   change transport_logs folder to 755 permissions  [#1447](https://github.com/skycoin/skywire/pull/1447)
-   Fix warn logs  [#1446](https://github.com/skycoin/skywire/pull/1446)
-   move survey generation to its own goroutine  [#1445](https://github.com/skycoin/skywire/pull/1445)
-   rebuild UI  [#1444](https://github.com/skycoin/skywire/pull/1444)
-   update mainnet rules for collecting rewards under the new system  [#1443](https://github.com/skycoin/skywire/pull/1443)
-   UI for managing the reward addresses  [#1442](https://github.com/skycoin/skywire/pull/1442)
-   Fix delete reward file ; show reward address on autoconfig  [#1441](https://github.com/skycoin/skywire/pull/1441)
-   disable public autoconnect logic for `config gen -b`  [#1440](https://github.com/skycoin/skywire/pull/1440)
-   update changelog & add changelog generation script  [#1439](https://github.com/skycoin/skywire/pull/1439)
-   Fix GetRewardAddress API  [#1438](https://github.com/skycoin/skywire/pull/1438)
-   mac release issue  [#1434](https://github.com/skycoin/skywire/pull/1434)
-   fix Mac/Windows release issue  [#1433](https://github.com/skycoin/skywire/pull/1433)
-   fix release issues  [#1432](https://github.com/skycoin/skywire/pull/1432)
-   built tag for non-systray skywire-visor  [#1429](https://github.com/skycoin/skywire/pull/1429)
-   Little Change on visor test subcommand  [#1428](https://github.com/skycoin/skywire/pull/1428)
-   Update to Angular 15  [#1426](https://github.com/skycoin/skywire/pull/1426)
-   [WIP] Hot fix on DNS  [#1425](https://github.com/skycoin/skywire/pull/1425)
-   update dmsg@develop  [#1423](https://github.com/skycoin/skywire/pull/1423)
-   Selected DMSG Server  [#1422](https://github.com/skycoin/skywire/pull/1422)
-   Integrated Autoconfig  [#1417](https://github.com/skycoin/skywire/pull/1417)
-   Update Angular to v14.2.11  [#1416](https://github.com/skycoin/skywire/pull/1416)
-   skywire-cli log collecting command  [#1414](https://github.com/skycoin/skywire/pull/1414)
-   [WIP] App/Services showing ports subcommand `skywire-cli visor ports`  [#1412](https://github.com/skycoin/skywire/pull/1412)
-   Feat/skywire app example  [#1409](https://github.com/skycoin/skywire/pull/1409)
-   `skywire-cli doc` command & cli documentation update  [#1408](https://github.com/skycoin/skywire/pull/1408)
-   fixing skywire-cli reward freezing issue  [#1407](https://github.com/skycoin/skywire/pull/1407)
-   Improve readme documentation  [#1406](https://github.com/skycoin/skywire/pull/1406)
-   Add cli command visor ping and test  [#1405](https://github.com/skycoin/skywire/pull/1405)
-   build ui  [#1403](https://github.com/skycoin/skywire/pull/1403)
-   fix `make format check` errors  [#1401](https://github.com/skycoin/skywire/pull/1401)
-   Fix control visor apps from hv  [#1399](https://github.com/skycoin/skywire/pull/1399)
-   Bug fixes for the UI  [#1398](https://github.com/skycoin/skywire/pull/1398)
-   run as systray flag `--systray`  [#1396](https://github.com/skycoin/skywire/pull/1396)
-   Fix/panic and datarace  [#1394](https://github.com/skycoin/skywire/pull/1394)
-   Printing new IP after connecting to VPN in CLI  [#1393](https://github.com/skycoin/skywire/pull/1393)
-   Add display node ip field to the main config  [#1392](https://github.com/skycoin/skywire/pull/1392)
-   re-implement setting reward address  [#1391](https://github.com/skycoin/skywire/pull/1391)
-   skywire-cli terminal user interface improvements  [#1390](https://github.com/skycoin/skywire/pull/1390)
-   improve `skywire-cli vpn` subcommand  [#1389](https://github.com/skycoin/skywire/pull/1389)
-   Fix transport logging  [#1386](https://github.com/skycoin/skywire/pull/1386)
-   Fix/cli config priv flags  [#1384](https://github.com/skycoin/skywire/pull/1384)
-   Add param customCommand for PtyUI.Handler  [#1383](https://github.com/skycoin/skywire/pull/1383)
-   add Info field to Service struct  [#1382](https://github.com/skycoin/skywire/pull/1382)
-   Add DNS to TUN, in VPN-Client  [#1381](https://github.com/skycoin/skywire/pull/1381)
-   Improve systray VPN button initialization  [#1380](https://github.com/skycoin/skywire/pull/1380)
-   Fix/privacyjson  [#1379](https://github.com/skycoin/skywire/pull/1379)
-   Update transport file logging  [#1376](https://github.com/skycoin/skywire/pull/1376)
-   Update LocalIPs field in model Service  [#1375](https://github.com/skycoin/skywire/pull/1375)
-   Feat/expose dmsghttp server  [#1374](https://github.com/skycoin/skywire/pull/1374)
-   Fix negative waitgroup  [#1372](https://github.com/skycoin/skywire/pull/1372)
-   `skywire-cli config priv` subcommand  [#1369](https://github.com/skycoin/skywire/pull/1369)
-   Fix/absence of git in makefile  [#1368](https://github.com/skycoin/skywire/pull/1368)
-   Fix rpc error in cli for json  [#1367](https://github.com/skycoin/skywire/pull/1367)
-   Fix StartVPNCient logic  [#1366](https://github.com/skycoin/skywire/pull/1366)
-   Fix the auth problems with the UI  [#1358](https://github.com/skycoin/skywire/pull/1358)
-   Add app arg cli subcommand  [#1356](https://github.com/skycoin/skywire/pull/1356)
-   Update/Add README.md for Mac/Win build installer script  [#1355](https://github.com/skycoin/skywire/pull/1355)
-   Fix/cli json  [#1354](https://github.com/skycoin/skywire/pull/1354)
-   forgotten build-ui  [#1350](https://github.com/skycoin/skywire/pull/1350)
-   Fix UI update button  [#1349](https://github.com/skycoin/skywire/pull/1349)
-   Update to Angular 14  [#1347](https://github.com/skycoin/skywire/pull/1347)
-   Feat/cli json output  [#1346](https://github.com/skycoin/skywire/pull/1346)
-   update appveyor.yml  [#1345](https://github.com/skycoin/skywire/pull/1345)
-   update golangci-lint & goimports-reviser ; fix `make format check` errors  [#1343](https://github.com/skycoin/skywire/pull/1343)
-   enable autopeering via environmental variable  [#1342](https://github.com/skycoin/skywire/pull/1342)
-   fix autopeering  [#1339](https://github.com/skycoin/skywire/pull/1339)
-   Update documentation  [#1338](https://github.com/skycoin/skywire/pull/1338)
-   service discovery query filtering  [#1337](https://github.com/skycoin/skywire/pull/1337)
-   Fix/dockerfile arg  [#1336](https://github.com/skycoin/skywire/pull/1336)
-   Modifying SWAddr and Service for Service Discovery PG Migration  [#1334](https://github.com/skycoin/skywire/pull/1334)
-   Fix/changelog  [#1333](https://github.com/skycoin/skywire/pull/1333)
-   Save data in the UI per Hypervisor PK  [#1329](https://github.com/skycoin/skywire/pull/1329)
-   Add changelog for various PRs  [#1328](https://github.com/skycoin/skywire/pull/1328)
-   Fix/vpn server client close logs  [#1325](https://github.com/skycoin/skywire/pull/1325)
-   fixing freezing hypervisor UI  [#1323](https://github.com/skycoin/skywire/pull/1323)
-   `skywire-cli config gen` flags  [#1319](https://github.com/skycoin/skywire/pull/1319)
-   Several improvements for the UI  [#1318](https://github.com/skycoin/skywire/pull/1318)
-   `skywire-cli vpn` subcommands + separate-systray  [#1317](https://github.com/skycoin/skywire/pull/1317)
-   Fix/disable keepalives  [#1315](https://github.com/skycoin/skywire/pull/1315)
-   get connected hypervisors  [#1313](https://github.com/skycoin/skywire/pull/1313)
-   Fix/vm naming  [#1311](https://github.com/skycoin/skywire/pull/1311)
-   fix `halt` command  [#1310](https://github.com/skycoin/skywire/pull/1310)
-   auto-peering visors to the hypervisor (skybian)  [#1309](https://github.com/skycoin/skywire/pull/1309)
-   Vendor  [#1304](https://github.com/skycoin/skywire/pull/1304)
-   Vendore new PK for network monitor  [#1302](https://github.com/skycoin/skywire/pull/1302)
-   Add Ping and Pong latency packets  [#1300](https://github.com/skycoin/skywire/pull/1300)
-   Skip auto-transport when transport available  [#1299](https://github.com/skycoin/skywire/pull/1299)
-   v1.0.0  [#1290](https://github.com/skycoin/skywire/pull/1290)
-   fix caching issue  [#1288](https://github.com/skycoin/skywire/pull/1288)
-   Fix nil pointer error on dmsghttp config with offline stun server  [#1285](https://github.com/skycoin/skywire/pull/1285)
-   Fix/vpn server offline error  [#1284](https://github.com/skycoin/skywire/pull/1284)
-   fix nil pointer error for `skywire-cli config gen --all`  [#1282](https://github.com/skycoin/skywire/pull/1282)
-   update dmsg@develop  [#1281](https://github.com/skycoin/skywire/pull/1281)
-   update dmsghttp values  [#1280](https://github.com/skycoin/skywire/pull/1280)
-   Fix/vendor utilities  [#1276](https://github.com/skycoin/skywire/pull/1276)
-   Fix/systray deps  [#1275](https://github.com/skycoin/skywire/pull/1275)
-   fix cap problem (rootless vpn-client)  [#1273](https://github.com/skycoin/skywire/pull/1273)
-   New update procedure for the UI  [#1271](https://github.com/skycoin/skywire/pull/1271)
-   add skywire-cli subcommands  [#1270](https://github.com/skycoin/skywire/pull/1270)
-   Add Windows Installer Job to AppVeyor  [#1269](https://github.com/skycoin/skywire/pull/1269)
-   Fix/false positive app err logs  [#1268](https://github.com/skycoin/skywire/pull/1268)
-   fix systray xfce  [#1267](https://github.com/skycoin/skywire/pull/1267)
-   improve public autoconnect module  [#1266](https://github.com/skycoin/skywire/pull/1266)
-   Fix/dirty fix negative latency  [#1263](https://github.com/skycoin/skywire/pull/1263)
-   Fix single vpn-server conn issue  [#1262](https://github.com/skycoin/skywire/pull/1262)
-   remove setup node from archives  [#1260](https://github.com/skycoin/skywire/pull/1260)
-   Fix/draft prerelease  [#1259](https://github.com/skycoin/skywire/pull/1259)
-   Fix vpn server  [#1258](https://github.com/skycoin/skywire/pull/1258)
-   make build-ui changes  [#1257](https://github.com/skycoin/skywire/pull/1257)
-   Improve Windows Installer Script  [#1253](https://github.com/skycoin/skywire/pull/1253)
-   Clean logs  [#1252](https://github.com/skycoin/skywire/pull/1252)
-   remove updater  [#1251](https://github.com/skycoin/skywire/pull/1251)
-   add `dmsgpty-cli` to `skywire-cli`  [#1250](https://github.com/skycoin/skywire/pull/1250)
-   fix appveyor branches issue  [#1249](https://github.com/skycoin/skywire/pull/1249)
-   AppVeyor push tag jobs  [#1248](https://github.com/skycoin/skywire/pull/1248)
-   Fix/auto transport logic  [#1247](https://github.com/skycoin/skywire/pull/1247)
-   Remove the terminal button  [#1245](https://github.com/skycoin/skywire/pull/1245)
-   MacOS Installer Package  [#1242](https://github.com/skycoin/skywire/pull/1242)
-   Fix updater [WIP]  [#1241](https://github.com/skycoin/skywire/pull/1241)
-   Remove the update and restart buttons  [#1239](https://github.com/skycoin/skywire/pull/1239)
-   Fix dmsg tracker  [#1238](https://github.com/skycoin/skywire/pull/1238)
-   Fix stun client datarace  [#1237](https://github.com/skycoin/skywire/pull/1237)
-   Improvements for the VPN UI status  [#1235](https://github.com/skycoin/skywire/pull/1235)
-   update cli & visor documentation  [#1232](https://github.com/skycoin/skywire/pull/1232)
-   Docs/systray  [#1228](https://github.com/skycoin/skywire/pull/1228)
-   Fix vpn-server close err  [#1227](https://github.com/skycoin/skywire/pull/1227)
-   manipulate goreleaser and appveyor for release from appveyor  [#1226](https://github.com/skycoin/skywire/pull/1226)
-   Fix the VPN UI  [#1225](https://github.com/skycoin/skywire/pull/1225)
-   `--binpath` flag  [#1223](https://github.com/skycoin/skywire/pull/1223)
-   Fix vpn reconnecting status  [#1222](https://github.com/skycoin/skywire/pull/1222)
-   make build-ui for rc4  [#1218](https://github.com/skycoin/skywire/pull/1218)
-   Fix wrong status  [#1217](https://github.com/skycoin/skywire/pull/1217)
-   Make the UI work with the new app statuses  [#1215](https://github.com/skycoin/skywire/pull/1215)
-   Make app-status generic  [#1213](https://github.com/skycoin/skywire/pull/1213)
-   Fix/vpn server close  [#1212](https://github.com/skycoin/skywire/pull/1212)
-   Fix/app state  [#1210](https://github.com/skycoin/skywire/pull/1210)
-   Fix vpn status  [#1208](https://github.com/skycoin/skywire/pull/1208)
-   Fix the VPN status in the UI  [#1204](https://github.com/skycoin/skywire/pull/1204)
-   Fix systray nil pointer and data race  [#1203](https://github.com/skycoin/skywire/pull/1203)
-   Fix nil pointer dereference in Proc on windows  [#1201](https://github.com/skycoin/skywire/pull/1201)
-   Fix config gen -x flag  [#1200](https://github.com/skycoin/skywire/pull/1200)
-   Build UI for RC2  [#1195](https://github.com/skycoin/skywire/pull/1195)
-   Fix config fallback in cli  [#1193](https://github.com/skycoin/skywire/pull/1193)
-   Update changelog  [#1191](https://github.com/skycoin/skywire/pull/1191)
-   Get gorleaser ready for systray app  [#1189](https://github.com/skycoin/skywire/pull/1189)
-   Fix/vpn stats  [#1184](https://github.com/skycoin/skywire/pull/1184)
-   Fix Systray on Linux  [#1183](https://github.com/skycoin/skywire/pull/1183)
-   add `install-system-linux` makefile directives  [#1180](https://github.com/skycoin/skywire/pull/1180)
-   Improve Windows Installer  [#1179](https://github.com/skycoin/skywire/pull/1179)
-   fix visor uses default config with no arguments  [#1176](https://github.com/skycoin/skywire/pull/1176)
-   Fix/module error  [#1175](https://github.com/skycoin/skywire/pull/1175)
-   Use logging package from skycoin for retrier  [#1173](https://github.com/skycoin/skywire/pull/1173)
-   Add early shutdown  [#1171](https://github.com/skycoin/skywire/pull/1171)
-   fix permissions check  [#1170](https://github.com/skycoin/skywire/pull/1170)
-   Fix/close vpn conn gracefully  [#1168](https://github.com/skycoin/skywire/pull/1168)
-   Switch systray repo  [#1166](https://github.com/skycoin/skywire/pull/1166)
-   Move ut client from internal to pkg  [#1164](https://github.com/skycoin/skywire/pull/1164)
-   own goroutine for dmsg trackers  [#1160](https://github.com/skycoin/skywire/pull/1160)
-   Fix config update  [#1159](https://github.com/skycoin/skywire/pull/1159)
-   Add a netifc field in the UI for configuring the VPN server  [#1158](https://github.com/skycoin/skywire/pull/1158)
-   Fix version check  [#1157](https://github.com/skycoin/skywire/pull/1157)
-   fixing VPN server problem with multiple network interface  [#1156](https://github.com/skycoin/skywire/pull/1156)
-   Change `-p --pkg` flags for config gen and visor  [#1155](https://github.com/skycoin/skywire/pull/1155)
-   various small fixes  [#1151](https://github.com/skycoin/skywire/pull/1151)
-   Minor fixes and updates to CLI  [#1148](https://github.com/skycoin/skywire/pull/1148)
-   add makefile directives  [#1147](https://github.com/skycoin/skywire/pull/1147)
-   make exported ParseOptions fileds  [#1146](https://github.com/skycoin/skywire/pull/1146)
-   remove transports cache system  [#1144](https://github.com/skycoin/skywire/pull/1144)
-   Set Status=3 for Connecting of VPN-Client App  [#1141](https://github.com/skycoin/skywire/pull/1141)
-   fix dmsg imports  [#1137](https://github.com/skycoin/skywire/pull/1137)
-   Switch from internal `skyenv` to skywire-utilities `skyenv`  [#1136](https://github.com/skycoin/skywire/pull/1136)
-   Add setup node error to whitelist  [#1135](https://github.com/skycoin/skywire/pull/1135)
-   Group skywire-cli visor subcommands  [#1132](https://github.com/skycoin/skywire/pull/1132)
-   add Stop() method to public visor initialization  [#1131](https://github.com/skycoin/skywire/pull/1131)
-   remove migration from binary  [#1129](https://github.com/skycoin/skywire/pull/1129)
-   Improvements for the links in the app list  [#1128](https://github.com/skycoin/skywire/pull/1128)
-   Update to Angular 13  [#1127](https://github.com/skycoin/skywire/pull/1127)
-   Use repo skywire-utilities  [#1126](https://github.com/skycoin/skywire/pull/1126)
-   Cleanup util   [#1125](https://github.com/skycoin/skywire/pull/1125)
-   VPN Control Buttons in Systray  [#1124](https://github.com/skycoin/skywire/pull/1124)
-   Fix Vendoring  [#1122](https://github.com/skycoin/skywire/pull/1122)
-   Refactor internal packages  [#1116](https://github.com/skycoin/skywire/pull/1116)
-   fix dmsghttp-config.json file path in skywire config  [#1114](https://github.com/skycoin/skywire/pull/1114)
-   Feature/new skywire visor flag  [#1113](https://github.com/skycoin/skywire/pull/1113)
-   Use retrier from the dmsg package netutil  [#1111](https://github.com/skycoin/skywire/pull/1111)
-   Fix logic of `-o` flag in generate config  [#1108](https://github.com/skycoin/skywire/pull/1108)
-   Update/config ver  [#1107](https://github.com/skycoin/skywire/pull/1107)
-   Add BUILDTAG | Increase version to 0.6.0  [#1104](https://github.com/skycoin/skywire/pull/1104)
-   Replace prod values of dmsghttp-config file  [#1103](https://github.com/skycoin/skywire/pull/1103)
-   Comment out redundant release upload which breaks checksums  [#1102](https://github.com/skycoin/skywire/pull/1102)
-   add network monitor value to skyenv  [#1101](https://github.com/skycoin/skywire/pull/1101)
-   migrate to dmsghttp on binaries  [#1092](https://github.com/skycoin/skywire/pull/1092)
-   Fix/dmsghttp config missed  [#1091](https://github.com/skycoin/skywire/pull/1091)
-   Update documentation & other small fixes  [#1087](https://github.com/skycoin/skywire/pull/1087)
-   skywire-cli flags for vpn ui and url  [#1086](https://github.com/skycoin/skywire/pull/1086)
-   Add wintun.dll  [#1081](https://github.com/skycoin/skywire/pull/1081)
-   Fix delete inactive client log  [#1078](https://github.com/skycoin/skywire/pull/1078)
-   Change the API for getting the VPN IP  [#1077](https://github.com/skycoin/skywire/pull/1077)
-   Migrate update logic to Debian package  [#1076](https://github.com/skycoin/skywire/pull/1076)
-   --best-protocol flag  [#1069](https://github.com/skycoin/skywire/pull/1069)
-   Fix close log  [#1068](https://github.com/skycoin/skywire/pull/1068)
-   Fix MakeHTTPTransport  [#1065](https://github.com/skycoin/skywire/pull/1065)
-   remove host-keeper service  [#1063](https://github.com/skycoin/skywire/pull/1063)
-   Windows installer  [#1060](https://github.com/skycoin/skywire/pull/1060)
-   add &#39;run-source&#39; makefile directive  [#1058](https://github.com/skycoin/skywire/pull/1058)
-   new/update flags on skywire-cli  [#1052](https://github.com/skycoin/skywire/pull/1052)
-   Fix  header SW-PublicIP  [#1049](https://github.com/skycoin/skywire/pull/1049)
-   minor docker image push fix  [#1048](https://github.com/skycoin/skywire/pull/1048)
-   fixing docker image push  [#1043](https://github.com/skycoin/skywire/pull/1043)
-   upgrade chi  [#1042](https://github.com/skycoin/skywire/pull/1042)
-   Fix/dmsghttp public ip  [#1041](https://github.com/skycoin/skywire/pull/1041)
-   Fix EOF error on stratup visor  [#1039](https://github.com/skycoin/skywire/pull/1039)
-   Check IP before call delBindSTCPR  [#1038](https://github.com/skycoin/skywire/pull/1038)
-   added HostKeeper url to old configs  [#1037](https://github.com/skycoin/skywire/pull/1037)
-   Feature/host keeper  [#1034](https://github.com/skycoin/skywire/pull/1034)
-   Fix dmsghttp datarace  [#1033](https://github.com/skycoin/skywire/pull/1033)
-   fix rate limit error on checking for update  [#1032](https://github.com/skycoin/skywire/pull/1032)
-   Fix/dmsghttp eof  [#1031](https://github.com/skycoin/skywire/pull/1031)
-   Fix/data race  [#1029](https://github.com/skycoin/skywire/pull/1029)
-   Fix panic  [#1028](https://github.com/skycoin/skywire/pull/1028)
-   [WIP] VPN stats  [#1026](https://github.com/skycoin/skywire/pull/1026)
-   Update dmsg  [#1025](https://github.com/skycoin/skywire/pull/1025)
-   Fix panic  [#1024](https://github.com/skycoin/skywire/pull/1024)
-   turn off build-ui on windows appveyor (timeout)  [#1022](https://github.com/skycoin/skywire/pull/1022)
-   update CHANGELOG.md  [#1017](https://github.com/skycoin/skywire/pull/1017)
-   Start visor with hypervisor UI  [#1016](https://github.com/skycoin/skywire/pull/1016)
-   Fix nil pointer in vpn client  [#1015](https://github.com/skycoin/skywire/pull/1015)
-   Fix/syslog  [#1013](https://github.com/skycoin/skywire/pull/1013)
-   Autogenerate changelog on release  [#1012](https://github.com/skycoin/skywire/pull/1012)
-   Fix datarace on network monitor  [#1011](https://github.com/skycoin/skywire/pull/1011)
-   Advanced autoconnection  [#1010](https://github.com/skycoin/skywire/pull/1010)
-   move dmsghttp struct to pkg  [#1009](https://github.com/skycoin/skywire/pull/1009)
-   Put stcp at the end of the transport types list  [#1008](https://github.com/skycoin/skywire/pull/1008)
-   add BuildTag for MacOS  [#1006](https://github.com/skycoin/skywire/pull/1006)
-   Use local servers to generate config file  [#1003](https://github.com/skycoin/skywire/pull/1003)
-   fix(visor.summary): data race condition  [#1002](https://github.com/skycoin/skywire/pull/1002)
-   fix(makefile): pipe stderr to /dev/null  [#1001](https://github.com/skycoin/skywire/pull/1001)
-   Autocomplete  [#1000](https://github.com/skycoin/skywire/pull/1000)
-   Fixbug/panic on visor shutdown  [#997](https://github.com/skycoin/skywire/pull/997)
-   Connect to services over dmsghttp  [#995](https://github.com/skycoin/skywire/pull/995)
-   Update UI dependencies  [#994](https://github.com/skycoin/skywire/pull/994)
-   Parity between UI and CLI  [#981](https://github.com/skycoin/skywire/pull/981)
-   fixes dmsg showing 00000 after successful reconnection  [#980](https://github.com/skycoin/skywire/pull/980)
-   Add Debug log and fix retrier log  [#978](https://github.com/skycoin/skywire/pull/978)
-   Fix skywire verson in goreleaser  [#973](https://github.com/skycoin/skywire/pull/973)
-   Add the build tag to the UI  [#969](https://github.com/skycoin/skywire/pull/969)
-   Fix autoconnect retrial logic being too aggressive  [#968](https://github.com/skycoin/skywire/pull/968)
-   fixes trailing slash issue  [#965](https://github.com/skycoin/skywire/pull/965)
-   Make the updater work with problematic visors  [#960](https://github.com/skycoin/skywire/pull/960)
-   Fix/change update interval  [#959](https://github.com/skycoin/skywire/pull/959)
-   Release v0.5.0  [#957](https://github.com/skycoin/skywire/pull/957)
-   Fix overwriting of release from AppVeyor  [#956](https://github.com/skycoin/skywire/pull/956)
-   Remove ui-build and lint targets from AppVeyor to increase performance  [#952](https://github.com/skycoin/skywire/pull/952)
-   fix app stopping error status  [#949](https://github.com/skycoin/skywire/pull/949)
-   Add language portuguese  [#947](https://github.com/skycoin/skywire/pull/947)
-   Feature/improve redialing public autoconnect  [#945](https://github.com/skycoin/skywire/pull/945)
-   Change test config URLs  [#944](https://github.com/skycoin/skywire/pull/944)
-   Improvements for the vpn client UI  [#939](https://github.com/skycoin/skywire/pull/939)
-   Update public autoconnect default value  [#936](https://github.com/skycoin/skywire/pull/936)
-   Fix/dmsgtracker test  [#934](https://github.com/skycoin/skywire/pull/934)
-   Health endpoint: 3 states =&gt; connecting, healthy, and unhealthy  [#933](https://github.com/skycoin/skywire/pull/933)
-   Fix SetPublicAutoconnect  [#931](https://github.com/skycoin/skywire/pull/931)
-   Update goreleaser.yml to build only armv6  [#930](https://github.com/skycoin/skywire/pull/930)
-   Fix GetPersistentTransports  [#929](https://github.com/skycoin/skywire/pull/929)
-   Fix cli ls-apps  [#928](https://github.com/skycoin/skywire/pull/928)
-   Fix goreleaser  [#927](https://github.com/skycoin/skywire/pull/927)
-   Fix release process  [#926](https://github.com/skycoin/skywire/pull/926)
-   Add RPC func  [#925](https://github.com/skycoin/skywire/pull/925)
-   Improvements for the manager UI  [#924](https://github.com/skycoin/skywire/pull/924)
-   Add stcpr dependency to PublicVisor  [#922](https://github.com/skycoin/skywire/pull/922)
-   Fix/improve health  [#919](https://github.com/skycoin/skywire/pull/919)
-   Feature/debian installer  [#917](https://github.com/skycoin/skywire/pull/917)
-   Feature/improve health status  [#916](https://github.com/skycoin/skywire/pull/916)
-   Public Autoconnect [API | Summary]  [#915](https://github.com/skycoin/skywire/pull/915)
-   Feature/config subcommand  [#914](https://github.com/skycoin/skywire/pull/914)
-   remove readmegen package usage  [#913](https://github.com/skycoin/skywire/pull/913)
-   Feature/update config names  [#912](https://github.com/skycoin/skywire/pull/912)
-   add build_tag to summary API endpoint  [#911](https://github.com/skycoin/skywire/pull/911)
-   Bandwidth received fix  [#909](https://github.com/skycoin/skywire/pull/909)
-   AppStats: Connection Duration Addition to API  [#908](https://github.com/skycoin/skywire/pull/908)
-   Remove travis  [#907](https://github.com/skycoin/skywire/pull/907)
-   Add language portuguese  [#906](https://github.com/skycoin/skywire/pull/906)
-   Fix the discovery service URL  [#903](https://github.com/skycoin/skywire/pull/903)
-   Fix vpn client  [#901](https://github.com/skycoin/skywire/pull/901)
-   Fix/shutdown race  [#897](https://github.com/skycoin/skywire/pull/897)
-   Cleanup Makefile targets remove systray release targets  [#894](https://github.com/skycoin/skywire/pull/894)
-   Remove Transport Discovery heatbeat  [#891](https://github.com/skycoin/skywire/pull/891)
-   Mac installer  [#889](https://github.com/skycoin/skywire/pull/889)
-   Remove stcpr heartbeat  [#888](https://github.com/skycoin/skywire/pull/888)
-   Fix/vpn panic  [#887](https://github.com/skycoin/skywire/pull/887)
-   Fix nil pointer dereference  [#886](https://github.com/skycoin/skywire/pull/886)
-   Remove heartbeat | Add Deregister  [#884](https://github.com/skycoin/skywire/pull/884)
-   Dmsg delete entry on shutdown  [#883](https://github.com/skycoin/skywire/pull/883)
-   Improve systray icon loading  [#882](https://github.com/skycoin/skywire/pull/882)
-   Update Angular and fix problems  [#880](https://github.com/skycoin/skywire/pull/880)
-   travis: added auto deploy release on tag  [#878](https://github.com/skycoin/skywire/pull/878)
-   Delete vpn env test  [#876](https://github.com/skycoin/skywire/pull/876)
-   Fix Persistent transports cancel issue  [#874](https://github.com/skycoin/skywire/pull/874)
-   Update service discovery references  [#872](https://github.com/skycoin/skywire/pull/872)
-   Docker fix skysocks client  [#871](https://github.com/skycoin/skywire/pull/871)
-   Add NAT info to the UI  [#870](https://github.com/skycoin/skywire/pull/870)
-   Fix creating transports from the UI  [#869](https://github.com/skycoin/skywire/pull/869)
-   Revert base to alpine  [#868](https://github.com/skycoin/skywire/pull/868)
-   Revert alpine to 3.13  [#867](https://github.com/skycoin/skywire/pull/867)
-   Add the persistent transports to the UI  [#866](https://github.com/skycoin/skywire/pull/866)
-   add dmsgpty to skywire-cli  [#863](https://github.com/skycoin/skywire/pull/863)
-   Is public  [#859](https://github.com/skycoin/skywire/pull/859)
-   Feature/refactor init  [#858](https://github.com/skycoin/skywire/pull/858)
-   Update mainnet_rules.md  [#857](https://github.com/skycoin/skywire/pull/857)
-   Add stcpr heartbeat  [#856](https://github.com/skycoin/skywire/pull/856)
-   AppVeyor  [#854](https://github.com/skycoin/skywire/pull/854)
-   Add the Skybian version to the UI  [#853](https://github.com/skycoin/skywire/pull/853)
-   skybian defaults from `skywire-cli visor gen-config -s`  [#852](https://github.com/skycoin/skywire/pull/852)
-   Fix data race  [#850](https://github.com/skycoin/skywire/pull/850)
-   add Skybian build version to summary of visor API and debugging  [#849](https://github.com/skycoin/skywire/pull/849)
-   Fix nil pointer exception  [#847](https://github.com/skycoin/skywire/pull/847)
-   Feature/remove redialing  [#842](https://github.com/skycoin/skywire/pull/842)
-   add escaping flags to `skywire-cli exec` documentation  [#841](https://github.com/skycoin/skywire/pull/841)
-   Fix/remove env test  [#838](https://github.com/skycoin/skywire/pull/838)
-   Fix/launcher discovery  [#836](https://github.com/skycoin/skywire/pull/836)
-   Unknown version fix  [#835](https://github.com/skycoin/skywire/pull/835)
-   Bugfix/panic on shutdown  [#833](https://github.com/skycoin/skywire/pull/833)
-   remove gateway.go  [#827](https://github.com/skycoin/skywire/pull/827)
-   Add stun NAT type check for sudph  [#825](https://github.com/skycoin/skywire/pull/825)
-   Update documentation  [#824](https://github.com/skycoin/skywire/pull/824)
-   Update uptime tracker and service discovery Backport  [#822](https://github.com/skycoin/skywire/pull/822)
-   Complete Visor Logs  [#820](https://github.com/skycoin/skywire/pull/820)
-   Update mainnet_rules.md  [#819](https://github.com/skycoin/skywire/pull/819)
-   Fix improve transport setup logic issue  [#818](https://github.com/skycoin/skywire/pull/818)
-   backport release config changes  [#815](https://github.com/skycoin/skywire/pull/815)
-   Release/v0.4.2  [#814](https://github.com/skycoin/skywire/pull/814)
-   Fix stcpr transport establishment issues  [#813](https://github.com/skycoin/skywire/pull/813)
-   Feature/snet rewrite  [#811](https://github.com/skycoin/skywire/pull/811)
-   Change perm of pid and logstore  [#810](https://github.com/skycoin/skywire/pull/810)
-   Fix data race in hypervisor  [#809](https://github.com/skycoin/skywire/pull/809)
-   Fix nil pointer panic in transport setup  [#807](https://github.com/skycoin/skywire/pull/807)
-   Fix/recompile frontend  [#805](https://github.com/skycoin/skywire/pull/805)
-   Config refactor  [#802](https://github.com/skycoin/skywire/pull/802)
-   Remove Config Whitelist dmsgpty  [#801](https://github.com/skycoin/skywire/pull/801)
-   Change rt retryduration from 10s to 2s  [#800](https://github.com/skycoin/skywire/pull/800)
-   fixes data race on logstore  [#799](https://github.com/skycoin/skywire/pull/799)
-   Update service discovery update interval  [#797](https://github.com/skycoin/skywire/pull/797)
-   Change file perm  [#796](https://github.com/skycoin/skywire/pull/796)
-   Feature/docker refactorings  [#794](https://github.com/skycoin/skywire/pull/794)
-   Improvements for the VPN client  [#792](https://github.com/skycoin/skywire/pull/792)
-   [WIP] NewJSONFileWhitelist =&gt; NewConfigFileWhitelist  [#789](https://github.com/skycoin/skywire/pull/789)
-   Update README.md  [#787](https://github.com/skycoin/skywire/pull/787)
-   Fix a bug in the app list  [#784](https://github.com/skycoin/skywire/pull/784)
-   [WIP] Improvements for the VPN client  [#782](https://github.com/skycoin/skywire/pull/782)
-   Update golangci to use revive  [#781](https://github.com/skycoin/skywire/pull/781)
-   Add min hops manipulation endpoint  [#780](https://github.com/skycoin/skywire/pull/780)
-   Windows build  [#779](https://github.com/skycoin/skywire/pull/779)
-   Fix/shutdown data races  [#777](https://github.com/skycoin/skywire/pull/777)
-   Netutil fixes  [#772](https://github.com/skycoin/skywire/pull/772)
-   Update mainnet_rules.md  [#769](https://github.com/skycoin/skywire/pull/769)
-   Update mainnet_rules.md  [#768](https://github.com/skycoin/skywire/pull/768)
-   Add a view logs option to he UI  [#766](https://github.com/skycoin/skywire/pull/766)
-   Fixes for the UI code  [#765](https://github.com/skycoin/skywire/pull/765)
-   Setup node VM  [#763](https://github.com/skycoin/skywire/pull/763)
-   Systray launcher  [#761](https://github.com/skycoin/skywire/pull/761)
-   Refactor Summaries  [#759](https://github.com/skycoin/skywire/pull/759)
-   Remove discord  [#756](https://github.com/skycoin/skywire/pull/756)
-   Feature/makefile cleanup  [#753](https://github.com/skycoin/skywire/pull/753)
-   Feature/push master dockerhub  [#752](https://github.com/skycoin/skywire/pull/752)
-   Solve nil pointer derefrence  [#750](https://github.com/skycoin/skywire/pull/750)
-   push to dockerhub on push to develop or master  [#749](https://github.com/skycoin/skywire/pull/749)
-   Update mainnet_rules.md  [#744](https://github.com/skycoin/skywire/pull/744)
-   Feature/public visors advertising  [#743](https://github.com/skycoin/skywire/pull/743)
-   Fix a problem with the time since last update  [#742](https://github.com/skycoin/skywire/pull/742)
-   Update mainnet_rules.md  [#741](https://github.com/skycoin/skywire/pull/741)
-   Makefile updates  [#739](https://github.com/skycoin/skywire/pull/739)
-   Replaced go:generate with go:embed  [#738](https://github.com/skycoin/skywire/pull/738)
-   added armv6 support to goreleaser  [#736](https://github.com/skycoin/skywire/pull/736)
-   Feature/runtime logs  [#735](https://github.com/skycoin/skywire/pull/735)
-   Improvements for the Skychat UI  [#734](https://github.com/skycoin/skywire/pull/734)
-   Remove the secure setting from the skysocks config  [#730](https://github.com/skycoin/skywire/pull/730)
-   Add router related config to the manager  [#729](https://github.com/skycoin/skywire/pull/729)
-   Fix for the updater UI  [#728](https://github.com/skycoin/skywire/pull/728)
-   Update mainnet_rules.md  [#726](https://github.com/skycoin/skywire/pull/726)
-   Init refactoring  [#724](https://github.com/skycoin/skywire/pull/724)
-   Update mainnet_rules.md  [#723](https://github.com/skycoin/skywire/pull/723)
-   Travis config, Makefile cleanup  [#722](https://github.com/skycoin/skywire/pull/722)
-   Show the IP in the UI  [#721](https://github.com/skycoin/skywire/pull/721)
-   Show the correct app arguments in the UI  [#720](https://github.com/skycoin/skywire/pull/720)
-   Fix/travis builds visor  [#718](https://github.com/skycoin/skywire/pull/718)
-   Display local IP  [#716](https://github.com/skycoin/skywire/pull/716)
-   Increase backoff factor and limit  [#715](https://github.com/skycoin/skywire/pull/715)
-   update german language  [#713](https://github.com/skycoin/skywire/pull/713)
-   Set goreleaser draft option to true  [#711](https://github.com/skycoin/skywire/pull/711)
-   Add exponential backoff for VPN client reconnection  [#709](https://github.com/skycoin/skywire/pull/709)
-   Ignore darwin_arm64 target for release  [#708](https://github.com/skycoin/skywire/pull/708)
-   Replace rakyll/static with native embedding  [#706](https://github.com/skycoin/skywire/pull/706)
-   Add handshake timeout  [#704](https://github.com/skycoin/skywire/pull/704)
-   Fix deadlock in managed tp&#39;s `updateStatus`  [#700](https://github.com/skycoin/skywire/pull/700)
-   Improvements for the visor list  [#699](https://github.com/skycoin/skywire/pull/699)
-   Fixes for the modal windows bottom margin  [#698](https://github.com/skycoin/skywire/pull/698)
-   Feature/transport setup  [#697](https://github.com/skycoin/skywire/pull/697)
-   Fix bool flag config representation  [#695](https://github.com/skycoin/skywire/pull/695)
-   Build from vendor  [#690](https://github.com/skycoin/skywire/pull/690)
-   Fix remote throughput calculation  [#689](https://github.com/skycoin/skywire/pull/689)
-   Feature/transport labels  [#686](https://github.com/skycoin/skywire/pull/686)
-   Move `run` command from `vpn` to `osutil`, add `RunWithResult`  [#685](https://github.com/skycoin/skywire/pull/685)
-   Move `Updated` field from `Status` to `EntryWithStatus`  [#684](https://github.com/skycoin/skywire/pull/684)
-   Add separate timeout for /health requests done  [#683](https://github.com/skycoin/skywire/pull/683)
-   Remove unused func and debug logs  [#681](https://github.com/skycoin/skywire/pull/681)
-   Skywire v0.4.0  [#680](https://github.com/skycoin/skywire/pull/680)
-   README cleanup  [#679](https://github.com/skycoin/skywire/pull/679)
-   Fix hanging health  [#677](https://github.com/skycoin/skywire/pull/677)
-   Parallelize health requests  [#671](https://github.com/skycoin/skywire/pull/671)
-   Call `ip` instead of `route` to fetch active network interface  [#669](https://github.com/skycoin/skywire/pull/669)
-   Feature/launch browser on run  [#668](https://github.com/skycoin/skywire/pull/668)
-   Feature/extra summary  [#666](https://github.com/skycoin/skywire/pull/666)
-   Allow to set a label when creating a transport with the UI  [#664](https://github.com/skycoin/skywire/pull/664)
-   Fix/check empty password  [#663](https://github.com/skycoin/skywire/pull/663)
-   Feature/add min hops config  [#656](https://github.com/skycoin/skywire/pull/656)
-   Improvements for the manager  [#655](https://github.com/skycoin/skywire/pull/655)
-   Update mainnet_rules.md  [#654](https://github.com/skycoin/skywire/pull/654)
-   Make the UI show the last version of the selected channel  [#652](https://github.com/skycoin/skywire/pull/652)
-   Systray application  [#651](https://github.com/skycoin/skywire/pull/651)
-   Additional app stats  [#649](https://github.com/skycoin/skywire/pull/649)
-   Update mainnet_rules.md  [#648](https://github.com/skycoin/skywire/pull/648)
-   Show an alert when openning the logs of a stopped app  [#647](https://github.com/skycoin/skywire/pull/647)
-   Show more info about the routes in the manager  [#645](https://github.com/skycoin/skywire/pull/645)
-   Improvements for how the manager gets the data  [#644](https://github.com/skycoin/skywire/pull/644)
-   Fix VPN client duplicates  [#642](https://github.com/skycoin/skywire/pull/642)
-   Swap Prometheus with Victoria Metrics  [#640](https://github.com/skycoin/skywire/pull/640)
-   Update mainnet_rules.md  [#639](https://github.com/skycoin/skywire/pull/639)
-   Fix/dup syscall portability  [#638](https://github.com/skycoin/skywire/pull/638)
-   Make app be restarted only once if some arg changed  [#637](https://github.com/skycoin/skywire/pull/637)
-   Fix app startup issues after changing the args via UI  [#635](https://github.com/skycoin/skywire/pull/635)
-   static compilation w/musl  [#634](https://github.com/skycoin/skywire/pull/634)
-   Fix panic on server shutdown  [#633](https://github.com/skycoin/skywire/pull/633)
-   fix update cmd  [#632](https://github.com/skycoin/skywire/pull/632)
-   Shared app state  [#630](https://github.com/skycoin/skywire/pull/630)
-   Add version field to the service discovery request.  [#628](https://github.com/skycoin/skywire/pull/628)
-   Mark visors with problematic services in the visor list  [#626](https://github.com/skycoin/skywire/pull/626)
-   Add update-config command to visor-cli.  [#621](https://github.com/skycoin/skywire/pull/621)
-   Feature/hypervisor new app flags  [#618](https://github.com/skycoin/skywire/pull/618)
-   Add the killswitch and secure options to the manager  [#617](https://github.com/skycoin/skywire/pull/617)
-   Fix VPN server  [#616](https://github.com/skycoin/skywire/pull/616)
-   Fix refreshing of state of the app.  [#615](https://github.com/skycoin/skywire/pull/615)
-   Fix some TODO&#39;s  [#613](https://github.com/skycoin/skywire/pull/613)
-   Fix/web ui terminal hypervisor  [#612](https://github.com/skycoin/skywire/pull/612)
-   Add killswitch flag, automatic reconnection  [#608](https://github.com/skycoin/skywire/pull/608)
-   Show the correct error msg in the updater  [#604](https://github.com/skycoin/skywire/pull/604)
-   [WIP] VPN desktop client  [#603](https://github.com/skycoin/skywire/pull/603)
-   Add endpoints for bulk deletes.  [#602](https://github.com/skycoin/skywire/pull/602)
-   Fix delays in keepalive packet tests  [#601](https://github.com/skycoin/skywire/pull/601)
-   Secure VPN  [#598](https://github.com/skycoin/skywire/pull/598)
-   Add NetworkProbe to switch case expression.  [#597](https://github.com/skycoin/skywire/pull/597)
-   Remove the logout option from the manager if not needed  [#596](https://github.com/skycoin/skywire/pull/596)
-   Add line breakers to logs in VPN client.  [#595](https://github.com/skycoin/skywire/pull/595)
-   Remove FillDefaults to keep hypervisor parsed settings  [#594](https://github.com/skycoin/skywire/pull/594)
-   Feature/allow passing hypervisor pk  [#593](https://github.com/skycoin/skywire/pull/593)
-   Update mainnet_rules.md  [#592](https://github.com/skycoin/skywire/pull/592)
-   Rebuild UI for develop  [#591](https://github.com/skycoin/skywire/pull/591)
-   Change how AuthGuardService works  [#589](https://github.com/skycoin/skywire/pull/589)
-   Increase uptime update delay, and use v3 endpoint  [#587](https://github.com/skycoin/skywire/pull/587)
-   package defaults with gen-config -p  [#585](https://github.com/skycoin/skywire/pull/585)
-   Fix a nil transport dereference  [#584](https://github.com/skycoin/skywire/pull/584)
-   Fix lack of dmsg information for hypervisor in the UI  [#583](https://github.com/skycoin/skywire/pull/583)
-   Fix locking order.  [#580](https://github.com/skycoin/skywire/pull/580)
-   Make the manager work with the integrated hypervisor  [#577](https://github.com/skycoin/skywire/pull/577)
-   Add boltdb hook  [#573](https://github.com/skycoin/skywire/pull/573)
-   Merge v0.4.0 into develop  [#570](https://github.com/skycoin/skywire/pull/570)
-   Fix nonce mismatch issues  [#569](https://github.com/skycoin/skywire/pull/569)
-   Remove helloworld app.  [#566](https://github.com/skycoin/skywire/pull/566)
-   Update README.md  [#561](https://github.com/skycoin/skywire/pull/561)
-   Fix JSON unmarshal issue  [#560](https://github.com/skycoin/skywire/pull/560)
-   Update README.md  [#559](https://github.com/skycoin/skywire/pull/559)
-   Fix empty request body error  [#558](https://github.com/skycoin/skywire/pull/558)
-   Merge develop into master  [#556](https://github.com/skycoin/skywire/pull/556)
-   Rebuild frontend  [#555](https://github.com/skycoin/skywire/pull/555)
-   Improvements for configuring the VPN from the manager  [#552](https://github.com/skycoin/skywire/pull/552)
-   Output hostname in Discord hook  [#549](https://github.com/skycoin/skywire/pull/549)
-   Add build information for deployment  [#548](https://github.com/skycoin/skywire/pull/548)
-   Differentiate v0.3.0 visors  [#547](https://github.com/skycoin/skywire/pull/547)
-   Change http.ServeMux to chi  [#546](https://github.com/skycoin/skywire/pull/546)
-   Fix the font used  [#545](https://github.com/skycoin/skywire/pull/545)
-   Make the manager work in 0.4.0  [#544](https://github.com/skycoin/skywire/pull/544)
-   Update dmsg vendor  [#542](https://github.com/skycoin/skywire/pull/542)
-   Add Discord start/stop logs  [#537](https://github.com/skycoin/skywire/pull/537)
-   Run vpn server non root  [#535](https://github.com/skycoin/skywire/pull/535)
-   Make VPN client runnable without root  [#534](https://github.com/skycoin/skywire/pull/534)
-   Update dmsg vendor  [#531](https://github.com/skycoin/skywire/pull/531)
-   Fix router tests  [#530](https://github.com/skycoin/skywire/pull/530)
-   Fix a bug in the visor details page of the manager  [#529](https://github.com/skycoin/skywire/pull/529)
-   Implement callbacks needed for VPN client  [#526](https://github.com/skycoin/skywire/pull/526)
-   Rebuild frontend  [#525](https://github.com/skycoin/skywire/pull/525)
-   Make VPN work properly over stcpr  [#523](https://github.com/skycoin/skywire/pull/523)
-   UpdateAppArg removes app argument if its value is empty  [#522](https://github.com/skycoin/skywire/pull/522)
-   Add password options to the UI of the proxy and vpn client apps  [#520](https://github.com/skycoin/skywire/pull/520)
-   Fix concurrent map writes panic  [#515](https://github.com/skycoin/skywire/pull/515)
-   Attach started  [#510](https://github.com/skycoin/skywire/pull/510)
-   Adapt route encryption for old visors  [#507](https://github.com/skycoin/skywire/pull/507)
-   Show the update links in the manager  [#506](https://github.com/skycoin/skywire/pull/506)
-   Updated vendor, improved Makefile and updated compiled static files.  [#505](https://github.com/skycoin/skywire/pull/505)
-   Update dmsg vendor  [#504](https://github.com/skycoin/skywire/pull/504)
-   Mark my todos, fix one  [#498](https://github.com/skycoin/skywire/pull/498)
-   Fix missing port in address resolver response  [#495](https://github.com/skycoin/skywire/pull/495)
-   Fix rfclient retrial logic  [#494](https://github.com/skycoin/skywire/pull/494)
-   VPN improvements  [#491](https://github.com/skycoin/skywire/pull/491)
-   Update mainnet_rules.md  [#489](https://github.com/skycoin/skywire/pull/489)
-   Fix bug with handshake error type  [#488](https://github.com/skycoin/skywire/pull/488)
-   Return non-zero status code if added transport is not up  [#487](https://github.com/skycoin/skywire/pull/487)
-   Add an option for removing all offline transports  [#486](https://github.com/skycoin/skywire/pull/486)
-   Include OSX builds in TravisCI and update OSX version  [#485](https://github.com/skycoin/skywire/pull/485)
-   Exclude OSX build in Travis temporarily  [#484](https://github.com/skycoin/skywire/pull/484)
-   Rebuild frontend  [#483](https://github.com/skycoin/skywire/pull/483)
-   Enable linter in TravisCI  [#481](https://github.com/skycoin/skywire/pull/481)
-   Fix hypervisor update status check  [#480](https://github.com/skycoin/skywire/pull/480)
-   Allow to configure the vpn apps from the manager UI  [#479](https://github.com/skycoin/skywire/pull/479)
-   Standardize skywire directories  [#478](https://github.com/skycoin/skywire/pull/478)
-   Improvements for the updater UI  [#477](https://github.com/skycoin/skywire/pull/477)
-   Add release URL to update information  [#475](https://github.com/skycoin/skywire/pull/475)
-   Add VPN apps to config on migration from V0 to V1  [#474](https://github.com/skycoin/skywire/pull/474)
-   Fix setting proxy settings  [#471](https://github.com/skycoin/skywire/pull/471)
-   Fix VPN client remote startup  [#470](https://github.com/skycoin/skywire/pull/470)
-   Make app exit on VPN server failure  [#468](https://github.com/skycoin/skywire/pull/468)
-   Rebuild frontend  [#466](https://github.com/skycoin/skywire/pull/466)
-   Add Discord hook to logger (develop)  [#463](https://github.com/skycoin/skywire/pull/463)
-   Add Discord hook to logger (master)  [#462](https://github.com/skycoin/skywire/pull/462)
-   Multiple improvements for the manager  [#460](https://github.com/skycoin/skywire/pull/460)
-   Backport master changes into develop  [#459](https://github.com/skycoin/skywire/pull/459)
-   Fix project paths after repository migration  [#458](https://github.com/skycoin/skywire/pull/458)
-   Remove the left menu bar from the manager  [#457](https://github.com/skycoin/skywire/pull/457)
-   Fix module name  [#456](https://github.com/skycoin/skywire/pull/456)
-   Update to Angular 10  [#455](https://github.com/skycoin/skywire/pull/455)
-   Fix visor RPC timeouts  [#454](https://github.com/skycoin/skywire/pull/454)
-   Improvements for the update procedure  [#453](https://github.com/skycoin/skywire/pull/453)
-   Visor UI  [#451](https://github.com/skycoin/skywire/pull/451)
-   Fix health endpoint timeout  [#448](https://github.com/skycoin/skywire/pull/448)
-   Route encryption  [#444](https://github.com/skycoin/skywire/pull/444)
-   WebSocket API for updater  [#443](https://github.com/skycoin/skywire/pull/443)
-   Set interval to 10 sec  [#442](https://github.com/skycoin/skywire/pull/442)
-   Fix ARM 32-bit panics  [#439](https://github.com/skycoin/skywire/pull/439)
-   Improvements for the UI/UX of the manager  [#436](https://github.com/skycoin/skywire/pull/436)
-   Automate trusted visor functionality  [#434](https://github.com/skycoin/skywire/pull/434)
-   Fix SUDPH bug  [#433](https://github.com/skycoin/skywire/pull/433)
-   Add DMSG info to the manager  [#427](https://github.com/skycoin/skywire/pull/427)
-   Health checks for services  [#426](https://github.com/skycoin/skywire/pull/426)
-   Update the UI theme  [#424](https://github.com/skycoin/skywire/pull/424)
-   Refactor direct transports and address resolver client  [#421](https://github.com/skycoin/skywire/pull/421)
-   Ensure hypervisor PKs are added to dmsgpty whitelist on initi.  [#420](https://github.com/skycoin/skywire/pull/420)
-   Hypervisor dmsg tab.  [#415](https://github.com/skycoin/skywire/pull/415)
-   Allow to update all visors  [#414](https://github.com/skycoin/skywire/pull/414)
-   Setup node metrics.  [#412](https://github.com/skycoin/skywire/pull/412)
-   Add a copy button in the visor list  [#406](https://github.com/skycoin/skywire/pull/406)
-   Fix a problem with the app names in the manager  [#403](https://github.com/skycoin/skywire/pull/403)
-   Allow visor to start in case of address resolver or tp disc failure  [#402](https://github.com/skycoin/skywire/pull/402)
-   Implementation of UDP hole punch transport  [#400](https://github.com/skycoin/skywire/pull/400)
-   Added German Language  [#399](https://github.com/skycoin/skywire/pull/399)
-   Implement a UDP transport  [#397](https://github.com/skycoin/skywire/pull/397)
-   Rename proxy discovery to service discovery  [#393](https://github.com/skycoin/skywire/pull/393)
-   Adding trusted visors from config on startup  [#392](https://github.com/skycoin/skywire/pull/392)
-   Fix VPN apps  [#390](https://github.com/skycoin/skywire/pull/390)
-   Set up formatting imports using goimports-reviser  [#384](https://github.com/skycoin/skywire/pull/384)
-   Improve reliability and logging of setup node.  [#381](https://github.com/skycoin/skywire/pull/381)
-   Updating improvements  [#380](https://github.com/skycoin/skywire/pull/380)
-   Move buildinfo to dmsg repo  [#377](https://github.com/skycoin/skywire/pull/377)
-   Fix transport manager hangs and various improvements.  [#376](https://github.com/skycoin/skywire/pull/376)
-   Make proxy disc generic  [#374](https://github.com/skycoin/skywire/pull/374)
-   Appevent module implementation.   [#371](https://github.com/skycoin/skywire/pull/371)
-   Add package directive to Makefile  [#366](https://github.com/skycoin/skywire/pull/366)
-   Versioned visor configs.  [#364](https://github.com/skycoin/skywire/pull/364)
-   Improve visor config and startup/shutdown logic.  [#360](https://github.com/skycoin/skywire/pull/360)
-   Feature/retain keys  [#359](https://github.com/skycoin/skywire/pull/359)
-   Appserver improvements.  [#357](https://github.com/skycoin/skywire/pull/357)
-   VPN improvements  [#356](https://github.com/skycoin/skywire/pull/356)
-   Add app API docs  [#353](https://github.com/skycoin/skywire/pull/353)
-   Fix hypervisor disconnection issue.  [#349](https://github.com/skycoin/skywire/pull/349)
-   Improvements for the snackbar  [#348](https://github.com/skycoin/skywire/pull/348)
-   Fix &#39;invalid cross-device link&#39; error  [#342](https://github.com/skycoin/skywire/pull/342)
-   Added &quot;be&quot; for sentence clarity  [#341](https://github.com/skycoin/skywire/pull/341)
-   Proxy discovery.  [#340](https://github.com/skycoin/skywire/pull/340)
-   Rebuild UI for release  [#334](https://github.com/skycoin/skywire/pull/334)
-   Sanitize the URLs used  [#332](https://github.com/skycoin/skywire/pull/332)
-   Remove unused field in hypervisor config  [#331](https://github.com/skycoin/skywire/pull/331)
-   rm unnecessary line  [#330](https://github.com/skycoin/skywire/pull/330)
-   Fix/use vendor  [#329](https://github.com/skycoin/skywire/pull/329)
-   Doc cleanup  [#327](https://github.com/skycoin/skywire/pull/327)
-   Additional features for configuring skysocks-client in the manager  [#326](https://github.com/skycoin/skywire/pull/326)
-   Backport changes to develop  [#325](https://github.com/skycoin/skywire/pull/325)
-   Fix release name template for ARM  [#324](https://github.com/skycoin/skywire/pull/324)
-   Fix moving files between different drives and filesystems  [#322](https://github.com/skycoin/skywire/pull/322)
-   Added &#39;stats&#39; to proxydisc.  [#320](https://github.com/skycoin/skywire/pull/320)
-   Proxy discovery client.  [#318](https://github.com/skycoin/skywire/pull/318)
-   Improvements for the manager  [#315](https://github.com/skycoin/skywire/pull/315)
-   Implement a holepunch transport  [#313](https://github.com/skycoin/skywire/pull/313)
-   Update mainnet_rules.md  [#312](https://github.com/skycoin/skywire/pull/312)
-   Added app config fields to RPC.Apps call.  [#311](https://github.com/skycoin/skywire/pull/311)
-   Added &#39;is_up&#39; to transport status CLI.  [#310](https://github.com/skycoin/skywire/pull/310)
-   Develop  [#309](https://github.com/skycoin/skywire/pull/309)
-   Change json.NewDecoder to json.Unmarshal in binaries  [#308](https://github.com/skycoin/skywire/pull/308)
-   Fix/skysocks client port  [#307](https://github.com/skycoin/skywire/pull/307)
-   Update mainnet_rules.md  [#306](https://github.com/skycoin/skywire/pull/306)
-   Update mainnet_rules.md  [#305](https://github.com/skycoin/skywire/pull/305)
-   Add mainnet rules  [#304](https://github.com/skycoin/skywire/pull/304)
-   Fix go.sum  [#301](https://github.com/skycoin/skywire/pull/301)
-   Update dmsg version  [#300](https://github.com/skycoin/skywire/pull/300)
-   Fix go.sum  [#299](https://github.com/skycoin/skywire/pull/299)
-   Merge v0.1.0  [#298](https://github.com/skycoin/skywire/pull/298)
-   Backport master changes into develop  [#297](https://github.com/skycoin/skywire/pull/297)
-   Update dmsg version  [#296](https://github.com/skycoin/skywire/pull/296)
-   Comment out failing tests  [#295](https://github.com/skycoin/skywire/pull/295)
-   Change ports for skychat, skysocks, hypervisor API/UI  [#294](https://github.com/skycoin/skywire/pull/294)
-   Embed static files in hypervisor binary.  [#293](https://github.com/skycoin/skywire/pull/293)
-   Revert asynchronous transport handling  [#292](https://github.com/skycoin/skywire/pull/292)
-   Revert sending router packets asynchronously  [#291](https://github.com/skycoin/skywire/pull/291)
-   Rebuilt frontend  [#288](https://github.com/skycoin/skywire/pull/288)
-   Expose skyenv module  [#287](https://github.com/skycoin/skywire/pull/287)
-   Add hypervisor to goreleaser  [#286](https://github.com/skycoin/skywire/pull/286)
-   Fix setup node PKs  [#283](https://github.com/skycoin/skywire/pull/283)
-   Change default setup node PK  [#282](https://github.com/skycoin/skywire/pull/282)
-   Update the Spanish translation of the manager  [#281](https://github.com/skycoin/skywire/pull/281)
-   VPN apps  [#278](https://github.com/skycoin/skywire/pull/278)
-   Apply readme generator  [#276](https://github.com/skycoin/skywire/pull/276)
-   Fix a problem while navigating with the manager  [#275](https://github.com/skycoin/skywire/pull/275)
-   Make the manager work with http while using the test server  [#274](https://github.com/skycoin/skywire/pull/274)
-   Update the Spanish translation  [#273](https://github.com/skycoin/skywire/pull/273)
-   Show the visor version on the manager  [#272](https://github.com/skycoin/skywire/pull/272)
-   Add tests for the manager to Travis CI  [#271](https://github.com/skycoin/skywire/pull/271)
-   Fix panic on migration of old key pair  [#267](https://github.com/skycoin/skywire/pull/267)
-   Backport fixes from master to develop  [#265](https://github.com/skycoin/skywire/pull/265)
-   Added --secret-key,-s flag to gen-config.  [#263](https://github.com/skycoin/skywire/pull/263)
-   Avoid HTTP body for GET request in route finder  [#259](https://github.com/skycoin/skywire/pull/259)
-   Make the session cookie work with the terminal  [#258](https://github.com/skycoin/skywire/pull/258)
-   Fix dmsgpty in manager UI  [#257](https://github.com/skycoin/skywire/pull/257)
-   Generate PubKey from SecKey if only SecKey is set in Visor config  [#256](https://github.com/skycoin/skywire/pull/256)
-   Fix incorrectly renamed &quot;Uptime&quot; to &quot;UptimeTracker&quot;  [#254](https://github.com/skycoin/skywire/pull/254)
-   [READY] Add configuration options for some apps in the manager  [#253](https://github.com/skycoin/skywire/pull/253)
-   Make the manager open the terminal with the same protocol  [#252](https://github.com/skycoin/skywire/pull/252)
-   Delete config.json  [#250](https://github.com/skycoin/skywire/pull/250)
-   Improve the error messages the manager shows  [#249](https://github.com/skycoin/skywire/pull/249)
-   Use default values if required visor config fields are empty  [#246](https://github.com/skycoin/skywire/pull/246)
-   Fix manager UI login for HTTP hypervisor&#39;s API  [#243](https://github.com/skycoin/skywire/pull/243)
-   Add `--retain-keys` flag  [#242](https://github.com/skycoin/skywire/pull/242)
-   Remove workaround for tests  [#240](https://github.com/skycoin/skywire/pull/240)
-   Integrate manager UI with the hypervisor  [#236](https://github.com/skycoin/skywire/pull/236)
-   Backport Manager UI change  [#235](https://github.com/skycoin/skywire/pull/235)
-   remove docs and integration folder  [#234](https://github.com/skycoin/skywire/pull/234)
-   Fix a prod build problem with the manager  [#232](https://github.com/skycoin/skywire/pull/232)
-   Replace unix pipes with TCP sockets  [#231](https://github.com/skycoin/skywire/pull/231)
-   Update README.md  [#229](https://github.com/skycoin/skywire/pull/229)
-   Put apps in a separate directory  [#228](https://github.com/skycoin/skywire/pull/228)
-   Send router packets asynchronously  [#223](https://github.com/skycoin/skywire/pull/223)
-   Change transport type preference  [#222](https://github.com/skycoin/skywire/pull/222)
-   Fix go.mod  [#221](https://github.com/skycoin/skywire/pull/221)
-   Fix go.mod  [#220](https://github.com/skycoin/skywire/pull/220)
-   Merge Milestone 2  [#219](https://github.com/skycoin/skywire/pull/219)
-   Improvements for the updater on the manager  [#218](https://github.com/skycoin/skywire/pull/218)
-   Implement endpoint for checking if visor update is available  [#217](https://github.com/skycoin/skywire/pull/217)
-   Add the manager UI  [#216](https://github.com/skycoin/skywire/pull/216)
-   Remove logging of every packet contents  [#215](https://github.com/skycoin/skywire/pull/215)
-   Fix update endpoint.  [#214](https://github.com/skycoin/skywire/pull/214)
-   Verify public key before requesting routes  [#211](https://github.com/skycoin/skywire/pull/211)
-   Updated hypervisor to work with TLS and updated to latest dmsg.  [#208](https://github.com/skycoin/skywire/pull/208)
-   Renamed `loop` to `routegroup` for hypervisor endpoints, function names, struct types and comments.  [#207](https://github.com/skycoin/skywire/pull/207)
-   Fix SettlementHS test  [#203](https://github.com/skycoin/skywire/pull/203)
-   Improved logging.  [#202](https://github.com/skycoin/skywire/pull/202)
-   Remove some panics  [#200](https://github.com/skycoin/skywire/pull/200)
-   Fix linter and tests on 32-bit architectures  [#199](https://github.com/skycoin/skywire/pull/199)
-   Jdknives patch 1  [#198](https://github.com/skycoin/skywire/pull/198)
-   Fix route removal bug  [#193](https://github.com/skycoin/skywire/pull/193)
-   Split visor.Start  [#191](https://github.com/skycoin/skywire/pull/191)
-   Fix SettlementHS test  [#190](https://github.com/skycoin/skywire/pull/190)
-   Update Makefile  [#189](https://github.com/skycoin/skywire/pull/189)
-   Fix settlement handshake register transport logging.  [#188](https://github.com/skycoin/skywire/pull/188)
-   Fix a panic in pathutil  [#187](https://github.com/skycoin/skywire/pull/187)
-   Add a workaround for tests panic in Go 1.14  [#183](https://github.com/skycoin/skywire/pull/183)
-   Fix route re-creation on rule removal through hypervisor API  [#179](https://github.com/skycoin/skywire/pull/179)
-   Fix restart.Context behavior  [#177](https://github.com/skycoin/skywire/pull/177)
-   Fix IntermediaryForwardRule panic  [#169](https://github.com/skycoin/skywire/pull/169)
-   Integrate m3 dsmgpty with hypervisor and various fixes.  [#166](https://github.com/skycoin/skywire/pull/166)
-   Update/dmsg serve  [#165](https://github.com/skycoin/skywire/pull/165)
-   Update dmsg to latest @mainnet-milestone2.  [#162](https://github.com/skycoin/skywire/pull/162)
-   Improve logging on app stop  [#156](https://github.com/skycoin/skywire/pull/156)
-   Rename CONTRIBUTE.md  [#154](https://github.com/skycoin/skywire/pull/154)
-   Updating mechanism for mainnet  [#153](https://github.com/skycoin/skywire/pull/153)
-   Fix rule expiration  [#152](https://github.com/skycoin/skywire/pull/152)
-   Delete skywire-visor  [#151](https://github.com/skycoin/skywire/pull/151)
-   Finish renaming Node to Visor  [#150](https://github.com/skycoin/skywire/pull/150)
-   Fix several app restarting issues  [#149](https://github.com/skycoin/skywire/pull/149)
-   Add build options to missing Makefile targets  [#142](https://github.com/skycoin/skywire/pull/142)
-   Version command  [#140](https://github.com/skycoin/skywire/pull/140)
-   Fix router tests  [#139](https://github.com/skycoin/skywire/pull/139)
-   Rename node to visor  [#136](https://github.com/skycoin/skywire/pull/136)
-   Improve route group logging, fix route group closing procedure  [#135](https://github.com/skycoin/skywire/pull/135)
-   Fix infinite keep alive loop  [#134](https://github.com/skycoin/skywire/pull/134)
-   Fix: Ensure transports are deregistered and removed when rm-tp is run.  [#133](https://github.com/skycoin/skywire/pull/133)
-   Document hypervisor auth states.  [#131](https://github.com/skycoin/skywire/pull/131)
-   Fix broken hypervisor after merging  [#128](https://github.com/skycoin/skywire/pull/128)
-   Hypervisor improvements  [#126](https://github.com/skycoin/skywire/pull/126)
-   Connect hypervisor and visor over dmsg  [#124](https://github.com/skycoin/skywire/pull/124)
-   Rename messaging to dmsg  [#118](https://github.com/skycoin/skywire/pull/118)
-   Fix dmsgpty  [#117](https://github.com/skycoin/skywire/pull/117)
-   fix update to uptime tracker  [#116](https://github.com/skycoin/skywire/pull/116)
-   [M2] Integrate the yamux version of DMSG  [#115](https://github.com/skycoin/skywire/pull/115)
-   Remove socket files  [#113](https://github.com/skycoin/skywire/pull/113)
-   Backport hypervisor fixes  [#112](https://github.com/skycoin/skywire/pull/112)
-   Improve app configurability via hypervisor and make it persistent  [#111](https://github.com/skycoin/skywire/pull/111)
-   Listen on STCP  [#110](https://github.com/skycoin/skywire/pull/110)
-   Backport transport deregistration logic  [#109](https://github.com/skycoin/skywire/pull/109)
-   Backport master fix to milestone2  [#106](https://github.com/skycoin/skywire/pull/106)
-   Skysocks rename  [#105](https://github.com/skycoin/skywire/pull/105)
-   Remove therealssh  [#101](https://github.com/skycoin/skywire/pull/101)
-   Fix default visor/hypervisor start  [#90](https://github.com/skycoin/skywire/pull/90)
-   [M2] Fix default route group timeout  [#89](https://github.com/skycoin/skywire/pull/89)
-   Add keep-alive packet propagation between nodes  [#87](https://github.com/skycoin/skywire/pull/87)
-   Implement visor restart from hypervisor  [#80](https://github.com/skycoin/skywire/pull/80)
-   Dmsg Hypervisor PR  [#79](https://github.com/skycoin/skywire/pull/79)
-   Feature/default stcp listen  [#77](https://github.com/skycoin/skywire/pull/77)
-   change from slice to map  [#70](https://github.com/skycoin/skywire/pull/70)
-   add uptime tracker URL  [#69](https://github.com/skycoin/skywire/pull/69)
-   Fix timeouts for proxy client  [#65](https://github.com/skycoin/skywire/pull/65)
-   Fix wrong use of middleware context in hypervisor.  [#63](https://github.com/skycoin/skywire/pull/63)
-   Stop hypervisor StartApp from hanging.  [#62](https://github.com/skycoin/skywire/pull/62)
-   Added &#39;online&#39; field to hypervisor get node(s) response.  [#60](https://github.com/skycoin/skywire/pull/60)
-   [WIP] Add the manager UI  [#59](https://github.com/skycoin/skywire/pull/59)
-   Remove realssh  [#56](https://github.com/skycoin/skywire/pull/56)
-   Remove transport from discovery on transport deregister  [#54](https://github.com/skycoin/skywire/pull/54)
-   Reconnect proxy if yamux session failed  [#53](https://github.com/skycoin/skywire/pull/53)
-   [WIP] Fix app2, router2 tests  [#51](https://github.com/skycoin/skywire/pull/51)
-   Milestone 2.  [#49](https://github.com/skycoin/skywire/pull/49)
-   Invalidate Hypervisor session after password change.  [#46](https://github.com/skycoin/skywire/pull/46)
-   Integration of dmsgpty in visor.  [#45](https://github.com/skycoin/skywire/pull/45)
-   Test stcp with nettest  [#42](https://github.com/skycoin/skywire/pull/42)
-   fix typos in log messages  [#40](https://github.com/skycoin/skywire/pull/40)
-   Add flag to select deployment and update default deployment  [#38](https://github.com/skycoin/skywire/pull/38)
-   Update README to include stcp config documentation.  [#34](https://github.com/skycoin/skywire/pull/34)
-   Fix/hypervisor endpoints  [#20](https://github.com/skycoin/skywire/pull/20)
-   udpated hypervisor readme  [#17](https://github.com/skycoin/skywire/pull/17)
-   Remove transport from discovery on transport deregister  [#11](https://github.com/skycoin/skywire/pull/11)
-   Feature/messaging to dmsg  [#9](https://github.com/skycoin/skywire/pull/9)
-   Fix production env.  [#7](https://github.com/skycoin/skywire/pull/7)
-   Feature/route finder single route  [#4](https://github.com/skycoin/skywire/pull/4)
-   Feature/dmsg hypervisor  [#3](https://github.com/skycoin/skywire/pull/3)
-   Mainnet milestone1  [#1](https://github.com/skycoin/skywire/pull/1)
