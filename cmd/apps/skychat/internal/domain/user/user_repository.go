package user

type UserRepository interface {
	NewUser() (User, error)
	GetUser() (*User, error)
	SetUser(u *User) error
}
