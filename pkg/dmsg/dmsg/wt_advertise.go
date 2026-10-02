// Package dmsg pkg/dmsg/dmsg/wt_advertise.go c1-net-dmsg
//
// What a server advertises about its WebTransport endpoint. Untagged, unlike
// the WebTransport server itself (wt.go), because callers that only advertise
// — a visor configuring a dmsg server it hosts — build for every target.
package dmsg

// WTPath is the HTTP/3 path the WebTransport endpoint is served on. The
// advertised Server.AddressWT URL includes it (e.g. "https://host:port/dmsg").
const WTPath = "/dmsg"

// AdvertiseWT publishes url, the certificate hash a client pins, and the hash
// of the certificate after the next rotation, as this server's WebTransport
// endpoint, for a server whose sessions arrive through ServeWTSession.
func (s *Server) AdvertiseWT(url string, certHash, nextCertHash [32]byte) {
	s.setAdvertisedWT(url, certHash, nextCertHash)
}
