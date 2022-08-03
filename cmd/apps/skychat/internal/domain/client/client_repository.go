package client

type ClientRepository interface {
	New() (Client, error)
	GetClient() (*Client, error)
	SetClient(c Client) error
}
