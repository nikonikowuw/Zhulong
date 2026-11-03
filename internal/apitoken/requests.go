package apitoken

// CreateAPITokenRequest represents the request payload to generate a new API token.
type CreateAPITokenRequest struct {
	Name string `json:"name" binding:"required"`
}
