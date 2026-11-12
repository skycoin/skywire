================================================================================
KCG GAME STANDARDS — Go (golang) Adaptation
================================================================================

version:     1.0
date:        2026-07-06
applies-to:  Go modules (github.com/skycoin/skywire and siblings)
source:      derived from ../kcg-game-standards/ — ~120 plain-text C#/Unity
             game-engine standards (principle-*, pattern-*, standards-*,
             antipattern-*, code-rules/*), the b0.standards corpus for the
             kcg-* repositories.
extends:     sss-standards-golang.txt in this directory
             sss-standards-golang.txt (the earlier, narrower SSS-only Go
             adaptation). This adaptation is a SUPERSET: it covers the full
             game-standards corpus, not just the 16 SSS rules + 71 code-rules.
             The K1..K10 kernel there is carried forward and extended here as
             KG1..KG16.

--------------------------------------------------------------------------------
WHAT THIS IS
--------------------------------------------------------------------------------

The KCG game standards are a C#/.NET, data-oriented-design, Unity/ECS
game-engine house style. They assume: one type = one .csproj assembly =
one namespace, generational-ID object pools, a per-frame simulation tick,
rendering/perceptual-color pipelines, manual object lifetime, no GC. A large
fraction is therefore either (a) already guaranteed by the Go compiler and
toolchain, (b) portable in spirit but expressed differently in idiomatic Go,
or (c) actively anti-idiomatic in Go and must NOT be transliterated.

This adaptation translates the corpus into Go terms with three verdicts:

  ADOPT       ports cleanly; often ALREADY enforced by go build / go vet /
              gofmt / golangci-lint / the race detector.
  ADAPT       the portable kernel survives, but the C# mechanism is dropped
              for the Go idiom; the conflict is documented, not "fixed."
  N-A-FOR-GO  game/C#/Unity-specific, or the direct opposite of correct Go.

Guiding principle (unchanged from the SSS adaptation):
  **idiomatic Go beats transliterated C#.** Where a standard and Go idiom
  conflict, Go wins, and the conflict is written down here — not patched into
  the codebase.

The single most important structural fact: **Go's compiler already enforces
the crown jewels of this corpus for free.** Import cycles are a hard build
error, so every "layering / state-must-not-import-state / no-back-reference /
assembly-isolation / import-depth" rule is subsumed by `go build` — a stronger
guarantee than the C# standards get from convention + grep. What the compiler
does NOT give free is import *direction* (A may import B but not vice-versa
when no cycle exists); that is the one place a linter (depguard) adds value.

--------------------------------------------------------------------------------
FILES IN THIS DIRECTORY
--------------------------------------------------------------------------------

  README.txt                     this file.

  standards-golang.txt           THE adaptation. Section 0 philosophy; §1 the
                                 Go quality kernel (KG1..KG16); §2 the full
                                 per-file verdict table across all six clusters;
                                 §3 what the Go toolchain enforces for free;
                                 §4 the direct-conflict list (rules that are
                                 anti-idiomatic in Go); §5 review procedure.

  domains-golang.txt             answers Synth's follow-up: the domain list for
                                 skywire (8 domains (com/net/rte/cxo/dsc/app/vis/rwd) over
                                 a c0 `com` base — "just a few"), the 3-letter
                                 prefixes, the c<layer>-<domain>-<system> scheme,
                                 the layer×domain matrix, and how to carry the
                                 c1/c2 prefix in a Go package doc comment WITHOUT
                                 fighting the compiler/linter (with the live
                                 gofmt syntax error proving why the name can't
                                 hold it).

  skywire-application.txt        "apply to the codebase if needed": the honest
                                 assessment of what skywire already satisfies
                                 (most of it, by Go construction) and the small
                                 set of genuinely actionable, low-risk gaps —
                                 plus the ONE concrete finding
                                 (Proc->ProcManager back-reference) and the
                                 optional linter additions, with their risk.

  golangci-additions.txt         a concrete, copy-pasteable .golangci.yml
                                 stanza (depguard layering rules,
                                 gochecknoglobals, gochecknoinits, gomodguard)
                                 that MECHANIZES the ~6 principles that are
                                 otherwise review-only — presented as a
                                 proposal to evaluate, not a mandate.

--------------------------------------------------------------------------------
METHOD (how this was produced)
--------------------------------------------------------------------------------

The ~120-file corpus was split into six thematic clusters and each read in
full against the skywire codebase:

  A  core architecture principles (single-writer, layering, locality,
     no-singleton, determinism, context/frame purity, systems-isolation)
  B  anti-interface / data-driven-design (the "OOP is cancer" thesis,
     interface-abuse/chaining, injectable-vs-hardcoded dispatch)
  C  portable patterns (context-struct, gather-compute-apply, worker-pool,
     modular-monolith, async-to-CSP, controller/state separation, pipelines)
  D  naming & coding standards (boolean naming, banned verbs, id naming,
     init-vs-load, raw-int casts, Fof/Fon, language usage)
  E  build / assembly / organization (assembly naming, colocation,
     import-depth, validation-gates, dependency wrapping, http typing)
  F  manager / ownership / state-import (manager definition, back-reference
     ban, no-direct-entity-pointers, pointer-at-rest, layering tiers)

The deeply game/Unity-specific files (rendering luminance/hue/color/shader/
monitor-curve/perceptual pipelines, strict-ECS, Entitas migration, behavior
trees, spatial hashing, frame-local caches, object-pool hashing, event-pool/
queue containers, AMO muscle-sensor-controller biology, camera domains, asset/
hot loading, task-metadata) were bucketed wholesale as N-A-FOR-GO — they have
no analogue in an event-driven network daemon. See standards-golang.txt §2.G.

================================================================================
END
================================================================================
