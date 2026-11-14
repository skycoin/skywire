# SSS3 standard for Go

SSS3 is the Structured State System standard, version 3.1, from the KCG game
projects. It is written for C#. The files here are its Go adaptation, which
this repository follows. Where SSS and Go idiom conflict, Go idiom wins.

- `sss-standards-golang.txt` maps each SSS rule to Go and sets the K1 to K10
  kernel.
- `README.txt` and `standards-golang.txt` extend that to the wider KCG game
  standards as KG1 to KG16.
- `domains-golang.txt` defines the `c<layer>-<domain>-<system>` descriptors
  every Go file carries. [../code-descriptors.md](../code-descriptors.md) is
  the current mapping for this repository and
  `scripts/check-descriptors.sh` enforces it.
- `skywire-application.txt` assesses skywire against the standard.
- `golangci-additions.txt` lists linter settings that would enforce parts of it.

The C# originals are not included. Paths in the text files that point at them
refer to the source corpus, not to this repository.
