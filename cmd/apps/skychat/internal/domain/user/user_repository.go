// Package user contains the code required by user of the chat app
package user

// Repository is the interface to the user repository
type Repository interface {
	GetUser() (*User, error)
	SetUser(u *User) error
}
