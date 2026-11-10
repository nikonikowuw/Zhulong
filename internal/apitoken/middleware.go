package apitoken

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const identityContextKey = "\x00apitoken:identity"

// SetIdentity stores the authenticated Identity into the Gin context.
func SetIdentity(c *gin.Context, id Identity) {
	c.Set(identityContextKey, id)
}

// GetIdentity retrieves the authenticated Identity from the Gin context.
func GetIdentity(c *gin.Context) (Identity, bool) {
	val, ok := c.Get(identityContextKey)
	if !ok {
		return Identity{}, false
	}
	id, ok := val.(Identity)
	return id, ok
}

// RequireBearer returns a Gin middleware that enforces Bearer token authentication for OpenAPI routes.
func RequireBearer(svc *Service, _ *zap.Logger, auditSvc *audit.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, ok := parseBearer(c.Request.Header.Values("Authorization"))
		if !ok {
			recordRejected(auditSvc, c, rejectMalformed)
			writeUnauthorized(c)
			return
		}

		identity, err := svc.Authenticate(c.Request.Context(), rawToken)
		if errors.Is(err, errUnauthorized) {
			recordRejected(auditSvc, c, rejectUnknownOrRevoked)
			writeUnauthorized(c)
			return
		}
		if err != nil {
			httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Internal server error", err))
			c.Abort()
			return
		}

		ctx := audit.WithActor(c.Request.Context(), audit.Actor{
			IP:       c.ClientIP(),
			Username: "api_token:" + identity.Name,
			Source:   audit.SourceAPI,
		})
		c.Request = c.Request.WithContext(ctx)

		SetIdentity(c, identity)
		c.Next()
	}
}

// parseBearer extracts the token string from Authorization header values.
func parseBearer(headerValues []string) (string, bool) {
	if len(headerValues) != 1 {
		return "", false
	}
	trimmed := strings.TrimSpace(headerValues[0])
	if len(trimmed) < 7 || !strings.EqualFold(trimmed[:7], "Bearer ") {
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
		Detail:   "reject=" + reason,
		Status:   audit.StatusFailed,
		ErrorMsg: "Unauthorized",
	})
}
