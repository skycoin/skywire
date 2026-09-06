# smoke-test.star — visible policy for the live integration test.
# Every dial logs that the policy fired, so we can confirm in the
# visor log that policy.Decide is actually being called.
# Skychat dials get mux=2 as the visible side-effect; everything
# else passes through unchanged.

def decide_route(ctx, candidates):
    logging.info("smoke-test fired for app=" + ctx.app + " peer=" + ctx.peer_pk)
    if ctx.app == "skychat":
        return RouteSpec(mux = 2)
    return RouteSpec()
