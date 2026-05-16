# RFC: Cross-Visor Admin/Roster Gossip for Skychat Groups

Tracking issues:
[#2636](https://github.com/skycoin/skywire/issues/2636) (this RFC's home),
[#2639](https://github.com/skycoin/skywire/issues/2639) (owner-SPOF design discussion the gossip layer unblocks).

Status: **Draft.** Captures the design space; implementation lives behind
a separate set of PRs.

## Background

Skychat groups today (`cmd/apps/skychat/group/`) carry their content
on CXO TreeStore feeds — every member publishes to their own feed
(`messages/<seq>`), every peer subscribes to the others. The
content-plane is fully federated post-#2580: any member's send
reaches any subscriber over CXO regardless of who is online.

**The membership and admin lists are not federated.** When a new
member joins via invite, their local `Record` is populated from the
invite link, which carries only:

```
group_id, owner_pk, port, mode, [aes_key]
```

The invite link does **not** carry the current roster or admin set.
The joining member's local view starts at `members = [owner, self]`
and `admins = [owner]`, regardless of how many peers the owner has
actually authorized.

Reality observed during the 2026-05-16 3-agent coordination:

- @Gamma rejoined `claude-coordination` after #2625 was fixed. Their
  `cli skychat group info` showed `MEMBERS=2`, despite the group
  having four real members. Future content messages from Beta /
  postfix-host **did** reach Gamma's subscriber (the CXO publisher
  fans them out unconditionally), but Gamma's UI / local-roster-
  derived logic didn't know those PKs were legitimate members.

- @Beta tried `group add` to extend the roster for `027087fe40…`
  from their side and hit `"only admins can edit roster"`. Their
  post-rejoin local record had `admins = [Alpha-PK]` only; Alpha's
  subsequent `group promote Beta` and `group promote Gamma` didn't
  propagate either, so even though Beta and Gamma were admins on
  the owner's record, they couldn't act on it locally.

The owner is the single source of truth for both lists, and the
single point of failure for mutating either. If the owner is
offline, no roster change can happen anywhere.

## Goals

1. **Members and admins are eventually-consistent across every
   live member**, without operator-side reconciliation, without
   manual invite-link re-issuance, and without owner involvement
   on every roster change.

2. **Roster-derived UI** (`cli skychat group info`, hypervisor UI)
   shows the *real* member set on every visor that is currently
   subscribed to the group, not just the owner-issued snapshot
   embedded in the joining member's invite.

3. **Admin authority is transitive** in the cryptographic sense:
   an action signed by any current admin is accepted by every
   visor that has converged on the same admin set, without
   requiring a network round-trip to the owner.

4. **Backward-compatible with existing invites and joined groups.**
   Pre-RFC clients that don't speak the gossip protocol continue
   to function on the content-plane; they just don't get roster
   updates.

## Non-goals

- **Content-plane changes.** Messages already federate fine; this
  RFC doesn't touch `messages/<seq>` or `mirror/`.

- **Removing the owner.** The owner is still the bootstrap root of
  authority and the source of the invite link. They just stop
  being the sole authority for roster mutation.

- **Byzantine fault tolerance.** A malicious admin who signs a
  conflicting roster mutation is a separate problem worth its own
  RFC (CRDT-with-veto, weighted voting, etc.). This RFC assumes
  admins are mutually trusted.

- **Real-time consistency.** Eventual consistency is good enough:
  the typical roster mutation rate is well under one per minute,
  and the existing `OnUpdate` subscriber callback fires within
  the CXO root-publish cadence (~seconds).

## Design

### Two new CXO feed paths

Within each member's existing publisher feed, alongside the
existing `messages/<seq>` and `mirror/` trees, this RFC adds:

```
roster/<seq>          # signed roster mutations from this visor
admin/<seq>           # signed admin-set mutations from this visor
```

`seq` is a monotonic integer per-feed, scoped to the (group, path)
pair. The existing CXO TreeStore primitives provide ordering and
content-addressing for free.

### Mutation envelopes

Each leaf at `roster/<seq>` is a CBOR-encoded envelope:

```go
type RosterMutation struct {
    GroupID   uuid.UUID
    Op        RosterOp      // ADD | REMOVE
    PeerPK    cipher.PubKey
    ParentSeq uint64        // last seq this mutation observed before issuing
    IssuedAt  time.Time
    IssuerPK  cipher.PubKey // == this visor's PK == feed publisher
    Signature [64]byte      // ed25519(IssuerPK, hash(group_id, op, peer_pk, parent_seq, issued_at))
}
```

Likewise for `admin/<seq>` with `Op = PROMOTE | DEMOTE`. The
signature ensures a malicious peer cannot forge a mutation
attributed to another admin.

### Reconciliation rule

On receiving a mutation, every visor independently:

1. **Authorize.** Look up `IssuerPK` in the *currently-applied*
   admin set. If absent, the mutation is dropped (logged at debug,
   not error — out-of-order delivery is normal during partition).
2. **Apply.** Add / remove the peer from the local roster (or
   admin set). The operation is idempotent: a duplicate ADD on a
   peer already in the set is a no-op; a REMOVE on a peer not in
   the set is a no-op.
3. **Re-evaluate.** If the mutation removed an admin, walk any
   later mutations issued *by that admin* and consider them void.
   Per-mutation `ParentSeq` makes this O(1) per check: a mutation
   issued at parent_seq=N is voided if the issuer was removed at
   any later seq ≤ N.

This converges deterministically because:
- the signature binds each mutation to its issuer's parent_seq
- removal of an admin retroactively voids their later mutations
- additions are commutative; removals are idempotent
- the founder's PK is hard-coded as an admin from the invite and
  cannot be removed (founder is the root of trust)

### Bootstrap

On `group join <invite-link>`:

1. Initialize `members = [owner, self]`, `admins = [owner]` from
   the invite (current behavior).
2. Subscribe to the owner's `roster/`, `admin/`, plus existing
   `messages/` and `mirror/`.
3. As mutations arrive (the owner has been re-broadcasting the
   current state on each `group add` / `promote` since the gossip
   layer landed), apply them per the rule above.
4. Once any *other* admin's PK appears in the local admin set,
   also subscribe to their `roster/` and `admin/` feeds — they
   are now authoritative sources too.

The bootstrap is self-healing: an offline-rejoined member catches
up by re-reading the existing CXO TreeStore on the publishers it
subscribes to (this is where the design depends on #2637's
seq-anchored replay landing — without it, a long-offline rejoin
sees only the inbox-ring tail).

### Invite link unchanged

The invite link continues to carry the minimal state
(`owner_pk + group_id + port + mode + aes_key`). The roster /
admin set is hydrated post-join via the gossip channels. This
keeps invite-link URL length bounded and the existing pre-RFC
URL format stable.

## Implementation outline

Roughly in order. Each bullet is a separate PR.

1. **Mutation types + signing** in `cmd/apps/skychat/group/`:
   add `RosterMutation` / `AdminMutation` types with CBOR
   encoding and ed25519 sign / verify. Pure types, no wire.

2. **Publisher emit** on `Manager.AddMember` / `RemoveMember` /
   `PromoteAdmin` / `DemoteAdmin`: build the mutation envelope,
   sign it, `pub.Put("roster/<seq>", body)`. Existing CXO
   plumbing carries it to subscribers without further work.

3. **Subscriber apply** in `Session.OnUpdate` (the existing
   per-peerSub callback wired in #2606): pattern-match the path
   on `roster/` or `admin/`, decode + verify + apply per the
   reconciliation rule. Update the local `Record` so
   `Manager.Get` reflects the new state.

4. **Local record persistence** of the resulting roster / admin
   set on disk so a restart doesn't lose convergence state.
   Already in scope today via `groupStore.SetRecord`; the new
   types just slot in.

5. **CLI surface unchanged**. `group add` / `promote` / `demote`
   continue to be the operator-facing entry points; the visor's
   handling becomes "issue a signed mutation" instead of
   "update local state only."

6. **`group info` reflects gossip state** automatically once
   step 3 lands — `MEMBERS` and `ADMINS` are read from the
   local record, which is now the converged-via-gossip view.

7. **Hypervisor UI** absorbs the gossiped state with no
   server-side change; the existing `GroupInfo` payload (after
   #2628 added per-peer last-inbound) already surfaces the
   roster.

## Migration / compatibility

Pre-RFC clients (no `roster/` or `admin/` feeds) interoperate
with post-RFC clients:

- **Pre-RFC owner, post-RFC member**: the owner's mutations are
  invisible to the new feeds; the member's local state stays at
  the invite snapshot until either the owner upgrades or a
  newer admin runs `group add` / `promote` and propagates via
  gossip.
- **Post-RFC owner, pre-RFC member**: the member's local state
  is still bootstrapped from the invite; they miss subsequent
  roster changes until they upgrade.
- **Both pre-RFC**: pre-RFC behavior, no regression.
- **Both post-RFC**: gossip converges normally.

A `--gossip-required` flag on `group create` can later enforce
post-RFC peers only, but the default policy is *permissive* —
let mixed-version groups limp along during rollout.

## Open questions

1. **Founder PK in the admin set forever?** The current proposal
   makes the founder un-demotable. Alternative: allow a 2-of-N
   admin signature to demote the founder (DAO-flavored). The
   simpler rule wins for v1; revisit after deployment exposes
   a failure mode that demands it.

2. **Anti-replay for mutation signatures.** `ParentSeq` plus the
   `IssuedAt` timestamp should suffice if clocks are loose-but-
   monotonic across visors. Worst case: an admin tries to replay
   a stale mutation; the reconciler sees the seq is already
   covered and drops it. Worth a CRDT-style "tombstone" set
   for explicit removal mutations? Defer — the seq-anchored
   replay should subsume it.

3. **Garbage collection.** Each visor accumulates `roster/<seq>`
   and `admin/<seq>` entries forever. After convergence, older
   entries are reachable but unused. A per-feed retention
   policy (`max-roster-history-entries: 1000`) can prune
   gracefully without breaking new-joiner bootstrap, as long as
   the *current* snapshot is reconstructable from a smaller
   tail. Probably need a periodic `roster/snapshot/<gen>`
   leaf that summarizes the state as of a given generation;
   new joiners read the latest snapshot first, then forward
   from there.

4. **Cross-feed timing.** A `PROMOTE` on `admin/` and the
   `roster/<seq>` issued by the newly-promoted admin can arrive
   out of order on any given subscriber. The reconciler must
   buffer un-applicable mutations long enough for their
   prerequisite to arrive. Current proposal: drop with a debug
   log on receipt; re-evaluate at every subsequent `admin/`
   update. Adds at-most one publish-cycle of latency to
   converge; trade-off is simpler than a generic
   dependency-graph evaluator.

5. **CRDT vs. eventual-consistency framing.** The signed
   `parent_seq` is intentionally lighter-weight than a real
   CRDT (no Lamport-clock merge function); it's an
   "operation-based eventual consistency with admin-set as the
   gate." If a future failure mode forces formal CRDT
   semantics (concurrent contradictory mutations from two
   admins), revisit.

## Owner-SPOF (#2639) — what this RFC resolves

Today's owner-SPOF is concrete:

- Roster mutation: blocks if owner is offline.
- Admin promotion: blocks if owner is offline.
- New invite issuance: blocks if owner is offline (`buildInvite`
  refuses unless caller is admin, and post-rejoin "admin" is
  always the owner).

Post-RFC, the first two unblock automatically. The third
(invite issuance) needs follow-up: any current admin should be
able to mint a fresh invite — the invite already carries the
owner's PK, port, and AES key, none of which change when an
admin issues. Filed as a sibling follow-up.

## Status / next steps

1. Land this RFC (PR open against `docs/`).
2. Issue tracker discussion on the open questions, especially
   garbage collection and cross-feed timing.
3. Beta to scope step-1 (mutation types + signing) as the first
   implementation PR after the design lands.
4. Gamma's #2637 (seq-anchored replay) is a hard prereq for the
   bootstrap step — convergent state requires reading the full
   gossip log on rejoin.
