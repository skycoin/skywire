package vt

// Minimal port of src/common/services/OscLinkService.ts — registers
// OSC 8 hyperlinks and hands out ids that live in the cell extended
// attributes. The per-line link bookkeeping of the original (used for
// hover invalidation) is left to the renderer layer.

// OscLink is a hyperlink created via OSC 8.
type OscLink struct {
	ID  string
	URI string
}

// OscLinkService stores OSC 8 hyperlinks by id.
type OscLinkService struct {
	links  map[int]OscLink
	nextID int
}

// NewOscLinkService creates an empty link registry.
func NewOscLinkService() *OscLinkService {
	return &OscLinkService{links: map[int]OscLink{}, nextID: 1}
}

// RegisterLink stores a link and returns its numeric id (stored in
// ExtendedAttrs.URLID).
func (s *OscLinkService) RegisterLink(id, uri string) int {
	linkID := s.nextID
	s.nextID++
	s.links[linkID] = OscLink{ID: id, URI: uri}
	return linkID
}

// GetLinkData returns the link for an id.
func (s *OscLinkService) GetLinkData(linkID int) (OscLink, bool) {
	l, ok := s.links[linkID]
	return l, ok
}
