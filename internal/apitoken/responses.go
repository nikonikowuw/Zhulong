package apitoken

import "time"

// APITokenInfo represents the outward projection of an API token.
type APITokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	MaskedKey  string     `json:"maskedKey"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

func newAPITokenInfo(t *APIToken) APITokenInfo {
	if t == nil {
		return APITokenInfo{}
	}
	var lastUsed *time.Time
	if t.LastUsedAt != nil {
		utc := t.LastUsedAt.UTC()
		lastUsed = &utc
	}
	var revoked *time.Time
	if t.RevokedAt != nil {
		utc := t.RevokedAt.UTC()
		revoked = &utc
	}
	return APITokenInfo{
		ID:         t.ID,
		Name:       t.Name,
		MaskedKey:  t.MaskedKey,
		CreatedAt:  t.CreatedAt.UTC(),
		LastUsedAt: lastUsed,
		RevokedAt:  revoked,
	}
}

// APITokenListResponse wraps the list of API tokens. Items is always non-nil.
type APITokenListResponse struct {
	Items []APITokenInfo `json:"items"`
}

// APITokenCreateResponse returns the token details along with the one-time plaintext token.
type APITokenCreateResponse struct {
	APITokenInfo
	Token string `json:"token"`
}

// APITokenRevokeResponse is the response payload when revoking an API token.
type APITokenRevokeResponse struct {
	Revoked bool `json:"revoked"`
}
