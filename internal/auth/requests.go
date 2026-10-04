package auth

// InitAdminRequest defines the payload for setting up the initial administrator.
type InitAdminRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=32"`
	Password        string `json:"password" binding:"required,min=8,max=64"`
	ConfirmPassword string `json:"confirmPassword" binding:"required"`
}

// LoginRequest defines the payload for administrator authentication.
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}
