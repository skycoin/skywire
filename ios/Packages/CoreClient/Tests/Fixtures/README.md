# CoreClient test fixtures

## `routes/`

What a real lite core answers on each visor API route the app uses, one
exchange per file: `{method, path, status, headers, body}`, the body verbatim.
`ios/scripts/record-api-fixtures.sh` writes them. It runs a throwaway core
(`make build-mobile`, the phone's `config gen` argv, a fresh identity) and calls
the routes in the order a first launch reaches them.

The tests serve these through `FakeVisor`: an exchange is found by its exact
request line, behind the server's session rules (session cookie, CSRF on
mutations, account creation, password check). The per-visor paths therefore
carry the recording's public key, and the tests address the visor by it.

Before the files are written, the script replaces what identifies the
recording machine: its public and LAN addresses and host name become
`203.0.113.10`, `100.64.0.10` and `host.example.ts.net`, and its temporary
directory becomes `/data/skywire`. The service-discovery list keeps its first
five entries. Re-record after an API change and review the diff.
