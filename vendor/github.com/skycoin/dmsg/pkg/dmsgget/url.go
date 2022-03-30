package dmsgget

import (
	"errors"
	"net/url"

	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

// Errors related to URLs.
var (
	ErrNoURLs                   = errors.New("no URLs provided")
	ErrMultipleURLsNotSupported = errors.New("multiple URLs is not yet supported")
)

// URL represents a dmsg http URL.
type URL struct {
	dmsg.Addr
	url.URL
}

// Fill fills the internal fields from an URL string.
func (du *URL) Fill(str string) error {
	u, err := url.Parse(str)
	if err != nil {
		return err
	}

	if u.Scheme == "" {
		return errors.New("URL is missing a scheme")
	}

	if u.Host == "" {
		return errors.New("URL is missing a host")
	}

	du.URL = *u
	return du.Addr.Set(u.Host)
}
