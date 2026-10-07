package disc

import "context"

// ServersFunc lists the registered dmsg servers from a copy the caller holds,
// or reports false to have the discovery asked instead.
type ServersFunc func(ctx context.Context) ([]*Entry, bool)

type cxoServersClient struct {
	APIClient
	servers ServersFunc
}

// NewCXOServersClient fronts primary's AllServers with servers, such as a
// subscription to dmsg discovery's feed. Everything else, and AllServers when
// servers has no answer, goes to primary.
func NewCXOServersClient(primary APIClient, servers ServersFunc) APIClient {
	if servers == nil {
		return primary
	}
	return &cxoServersClient{APIClient: primary, servers: servers}
}

func (c *cxoServersClient) AllServers(ctx context.Context) ([]*Entry, error) {
	if entries, ok := c.servers(ctx); ok {
		return entries, nil
	}
	return c.APIClient.AllServers(ctx)
}
