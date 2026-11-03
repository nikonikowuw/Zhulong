package openapi

// UpsertPersonRequest represents the request body for creating or updating a person.
type UpsertPersonRequest struct {
	Name string `json:"name" binding:"required,min=1,max=64"`
}
