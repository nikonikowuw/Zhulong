package auth

import "time"

// AuthStatusResponse describes whether the administrator account has been initialized.
type AuthStatusResponse struct {
	Initialized bool `json:"initialized"`
}

// LogoutResponse acknowledges that the session cookie was cleared.
type LogoutResponse struct {
	LoggedOut bool `json:"loggedOut"`
}

// UserResponse describes the authenticated user identity returned to clients.
type UserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}
