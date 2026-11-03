package apitoken

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

type identityKeyType struct{}

var identityKey = identityKeyType{}

// SetIdentity stores the authenticated Identity into the Gin context.
func SetIdentity(c *gin.Context, id Identity) {
	c.Set(string(rune(0))+"apitoken:identity", id)
}

// GetIdentity retrieves the authenticated Identity from the Gin context.
func GetIdentity(c *gin.Context) (Identity, bool) {
	val, ok := c.Get(string(rune(0)) + "apitoken:identity")
	if !ok {
		return Identity{}, false
	}
	id, ok := val.(Identity)
	return id, ok
}

// parseBearer extracts the token string from an Authorization header.
func parseBearer(authHeader string, headerValues []string) (string, bool) {
	if len(headerValues) != 1 {
		return "", false
	}
	trimmed := strings.TrimSpace(authHeader)
	if len(trimmed) < 7 {
		return "", false
	}
	if !strings.EqualFold(trimmed[:7], "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(trimmed[7:])
	if token == "" {
		return "", false
	}
	return token, true
}

func writeUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	httputil.WriteError(c, apperr.Unauthenticated("UNAUTHORIZED", "Invalid or missing Bearer token", nil))
	c.Abort()
}

func recordRejected(auditSvc *audit.Service, c *gin.Context, reason string) {
	if auditSvc == nil {
		return
	}
	auditSvc.Record(audit.Entry{
		IP:       c.ClientIP(),
		Username: audit.UsernameAnonymous,
		Action:   ActionTokenAuthRejected,
		Target:   "openapi",
		Detail:   fmt.Sprintf("reject=%s", reason),
		Status:   audit.StatusFailed,
		ErrorMsg: "Unauthorized",
	})
}

// RequireBearer returns a Gin middleware that enforces Bearer token authentication for OpenAPI routes.
func RequireBearer(svc *Service, logger *zap.Logger, auditSvc *audit.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, ok := parseBearer(c.GetHeader("Authorization"), c.Request.Header.Values("Authorization"))
		if !ok {
			recordRejected(auditSvc, c, RejectMalformed)
			writeUnauthorized(c)
			return
		}

		identity, err := svc.Authenticate(c.Request.Context(), rawToken)
		if errors.Is(err, ErrUnauthorized) {
			recordRejected(auditSvc, c, RejectUnknownOrRevoked)
			writeUnauthorized(c)
			return
		}
		if err != nil {
			httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Internal server error", err))
			c.Abort()
			return
		}

		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), audit.Actor{
			IP:       c.ClientIP(),
			Username: "api_token:" + identity.Name,
			Source:   audit.SourceAPI,
		}))

		SetIdentity(c, identity)
		c.Next()
	}
}
