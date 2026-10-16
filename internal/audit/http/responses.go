package audithttp

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
)

// AuditLogResponse represents the external API presentation for an audit log record.
type AuditLogResponse struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	IP        string    `json:"ip"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	Status    string    `json:"status"`
	ErrorMsg  string    `json:"errorMsg"`
}

func toAuditLogResponse(l audit.AuditLog) AuditLogResponse {
	return AuditLogResponse{
		ID:        l.ID,
		CreatedAt: l.CreatedAt.UTC(),
		IP:        l.IP,
		Username:  l.Username,
		Action:    l.Action,
		Target:    l.Target,
		Detail:    l.Detail,
		Status:    l.Status,
		ErrorMsg:  l.ErrorMsg,
	}
}

func toAuditLogResponses(logs []audit.AuditLog) []AuditLogResponse {
	if len(logs) == 0 {
		return []AuditLogResponse{}
	}
	responses := make([]AuditLogResponse, len(logs))
	for i, l := range logs {
		responses[i] = toAuditLogResponse(l)
	}
	return responses
}
