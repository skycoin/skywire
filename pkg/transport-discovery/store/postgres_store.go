package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

type postgresStore struct {
	log    *logging.Logger
	client *gorm.DB
	cache  map[string]int64
	closeC chan struct{}
}

// NewPostgresStore creates new sransports postgres store.
func NewPostgresStore(logger *logging.Logger, cl *gorm.DB) (TransportStore, error) {
	// automigrate
	if err := cl.AutoMigrate(Transport{}); err != nil {
		logger.Warn("failed to complete automigrate process")
	}

	s := &postgresStore{
		log:    logger,
		client: cl,
		cache:  make(map[string]int64),
		closeC: make(chan struct{}),
	}
	return s, nil
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

func (s *postgresStore) GetTransportByID(_ context.Context, id uuid.UUID) (*transport.Entry, error) {
	var tpRecord Transport
	if err := s.client.Where("transport_id = ?", id).First(&tpRecord).Error; err != nil {
		return nil, ErrTransportNotFound
	}

	entry, err := makeEntry(tpRecord)
	if err != nil {
		return nil, err
	}

	return &entry, nil
}

func (s *postgresStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	var tpRecords []Transport
	if err := s.client.Where("edge_a = ? OR edge_b = ?", pk.Hex(), pk.Hex()).Find(&tpRecords).Error; err != nil {
		return nil, ErrTransportNotFound
	}

	var entries []*transport.Entry

	for _, tpRecord := range tpRecords {
		entry, err := makeEntry(tpRecord)
		if err != nil {
			return nil, err
		}
		entries = append(entries, &entry)
	}

	return entries, nil
}

func (s *postgresStore) GetNumberOfTransports(context.Context) (map[network.Type]int, error) {
	var tpRecords []Transport
	response := map[network.Type]int{
		network.STCP:  0,
		network.STCPR: 0,
		network.SUDPH: 0,
		network.DMSG:  0,
	}
	if err := s.client.Find(&tpRecords).Error; err != nil {
		return response, err
	}
	for _, record := range tpRecords {
		response[network.Type(record.Type)]++
	}
	return response, nil
}

func (s *postgresStore) GetAllTransports(context.Context) ([]*transport.Entry, error) {
	var tpRecords []Transport
	if err := s.client.Find(&tpRecords).Error; err != nil {
		return nil, ErrTransportNotFound
	}

	var entries []*transport.Entry

	for _, tpRecord := range tpRecords {
		entry, err := makeEntry(tpRecord)
		if err != nil {
			return nil, err
		}
		entries = append(entries, &entry)
	}

	return entries, nil
}

func (s *postgresStore) Close() {
	close(s.closeC)
}

func makeEntry(record Transport) (transport.Entry, error) {
	cipher1 := cipher.PubKey{}
	if err := cipher1.UnmarshalText([]byte(record.EdgeA)); err != nil {
		return transport.Entry{}, err
	}

	cipher2 := cipher.PubKey{}
	if err := cipher2.UnmarshalText([]byte(record.EdgeB)); err != nil {
		return transport.Entry{}, err
	}

	entry := transport.Entry{}
	entry.Label = transport.Label(record.Label)
	entry.Type = network.Type(record.Type)
	entry.ID = uuid.MustParse(record.TransportID)
	entry.Edges = [2]cipher.PubKey{cipher1, cipher2}

	return entry, nil
}

// Transport is model (structure) for transports table
type Transport struct { //TODO (mohammed): good to use transport.Entry model here
	ID          uint `gorm:"primarykey"`
	CreatedAt   time.Time
	TransportID string `gorm:"unique"`
	EdgeA       string
	EdgeB       string
	Type        string
	Label       string
}
