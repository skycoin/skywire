# Specification Conventions

These conventions apply to every document under `specifications/`. They follow
the structure of the libp2p specifications and the key word usage of IETF
BCP 14.

## Scope

A specification states what the implementation on `develop` does, in enough
detail that a second implementation could interoperate with it. It does not
contain tutorials, troubleshooting, operator advice, motivation, history,
roadmaps or planned features.

The Go code is the reference. Every data format, constant and default names
the Go package and identifier that defines it, so a reader can check the
specification against the source.

## Key words

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in these
documents are to be interpreted as described in BCP 14 [RFC 2119] [RFC 8174]
when, and only when, they appear in all capitals, as shown here.

Use them only for requirements that affect interoperability. Describe
implementation behaviour that a peer cannot observe in plain present tense.

## Document layout

```
# <Component>

| Status   | Implemented                         |
|----------|-------------------------------------|
| Revision | <YYYY-MM-DD>, skywire <commit>      |
| Code     | `pkg/...`, `cmd/...`                |

## Abstract
## Overview
## Addressing
## Data Formats
## Interfaces
### HTTP API
### CXO Feeds
### RPC
## Behaviour
## Security Considerations
## References
```

Omit a section that does not apply. Add component specific sections between
Interfaces and Behaviour when needed.

- **Abstract** is one paragraph: what the component is and what it provides.
- **Overview** places the component in the network: who calls it, what it
  calls, and which other components can answer the same question.
- **Addressing** states how the component is reached. Deployment services are
  served over dmsg HTTP at `dmsg://<service-pk>:80`. They are not reachable
  over plain HTTP on the internet.
- **Data Formats** gives each structure as a field table (JSON name, type,
  required, meaning) with the Go type it comes from.
- **Interfaces** describes each HTTP endpoint with method and path,
  authentication, request, response and every status code the handler
  returns. CXO feeds give the dmsg port, the root path layout and the publish
  cadence. RPC methods give the request and response types.
- **Behaviour** states lifetimes, limits, ordering and algorithms. Constants
  go in a table with their value and Go identifier.
- **Security Considerations** states what is authenticated, what is trusted
  and what is exposed.

## Style

Write plain technical sentences in the present tense. Avoid "we" and "you".
Keep colons, semicolons and dashes to a minimum. Mark examples as
informative. Do not quote log messages or describe log output.
