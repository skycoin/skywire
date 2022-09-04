package direct

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"

	"github.com/skycoin/dmsg/pkg/disc"
)

// GetClientEntry gives all client entries
func GetClientEntry(pks cipher.PubKeys, servers []*disc.Entry) (clients []*disc.Entry) {
	srvPKs := make([]cipher.PubKey, 0)
	for _, entry := range servers {
		srvPKs = append(srvPKs, entry.Static)
	}

	for _, pk := range pks {
		client := &disc.Entry{
			Static: pk,
			Client: &disc.Client{
				DelegatedServers: srvPKs,
			},
		}
		clients = append(clients, client)
	}
	return clients
}

// GetAllEntries gives all the entries
func GetAllEntries(pks cipher.PubKeys, servers []*disc.Entry) (entries []*disc.Entry) {
	client := GetClientEntry(pks, servers)
	entries = append(client, servers...)
	return entries
}
