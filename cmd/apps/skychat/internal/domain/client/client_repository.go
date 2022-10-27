// Package client contains the struct Repository code for domain
package client

// Repository defines the interface to the client repository
type Repository interface {
	New() (Client, error)
	GetClient() (*Client, error)
	SetClient(c Client) error
}
