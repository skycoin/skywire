package store

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

type postgresStore struct {
	log    *logging.Logger
	client *gorm.DB
	cache  map[string]int64
	closeC chan struct{}
}

func (s *postgresStore) RegisterTransport(_ context.Context, sEntry *transport.SignedEntry) error {
	entry := sEntry.Entry

	var tpRecord Transport
	tpRecord.EdgeA = entry.Edges[0].Hex()
	tpRecord.EdgeB = entry.Edges[1].Hex()
	tpRecord.TransportID = entry.ID.String()
	tpRecord.Type = string(entry.Type)
	tpRecord.Label = string(entry.Label)

	return s.client.Save(&tpRecord).Error
}

func (s *postgresStore) DeregisterTransport(ctx context.Context, id uuid.UUID) error { //nolint
	return s.client.Where("transport_id = ?", id).Delete(&Transport{}).Error
}

func (s *postgresStore) Close() {
	close(s.closeC)
}
