# Manager UI smoke check

Loads the built bundle in a real browser and asserts that Angular bootstraps and
renders a component tree, with no errors of its own.

    cd static/skywire-manager-src && npm run build
    BROWSER_KIND=firefox BROWSER_BIN=/usr/bin/waterfox \
      node ci-scripts/ui-smoke/check.js

## Why it exists

`ng build`, `ng lint` and the unit specs all pass on a bundle that is broken the
moment a browser loads it. That is not hypothetical: migrating skycoin's wallet
to the esbuild builder broke it three ways — a blank page from how JSON imports
resolve, translations that never loaded, and a downlevel the browser rejected —
and none of the three was caught by a build, a lint or a spec. Only opening the
page found them.

## What it does not need

A hypervisor. Every failure above happens before the first API call succeeds, so
this runs against a static copy of `dist` and treats the failing `/api` requests
as expected. What it can therefore assert is that the bundle loads, that Angular
bootstraps, and that the routed component renders — not that any data appears.

It exercises `#/login`, which is the one route that renders without a backend.

## Not wired into CI

It needs a browser binary. The `ui` job in `.github/workflows/test.yml` already
installs Chrome for the karma specs, so adding it there is a small step; it is
left out until someone wants it gating merges.
