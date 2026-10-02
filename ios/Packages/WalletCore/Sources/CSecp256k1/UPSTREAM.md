# CSecp256k1

bitcoin-core/secp256k1 **v0.8.0** (commit
`6e2c8bc4ecdc6e71dbe7a368f360d8d453ce435d`), MIT (`COPYING`), copied
unedited by `ios/scripts/import-secp256k1.sh`. This file is the only one here
that is not upstream's.

What is here is what the library build compiles: `src/secp256k1.c` and the
two precomputed tables, plus every file they include under the defines in
`../../Package.swift` (the recovery module and nothing else optional), as
`clang -MM` lists it.

Check that nothing was edited (needs network to github.com):

    ios/scripts/import-secp256k1.sh --verify

Move to a new release: change `TAG` and `COMMIT` in the script, run it without
`--verify`, then `swift test` in `ios/Packages/WalletCore` and review the diff.
