package entities

// User is the identity behind a JWT. PasswordHash never leaves the server.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Permissions  []string
}
