package httputil

import (
	"reflect"

	"github.com/gin-gonic/gin"
)

// HandleJSON wraps a handler that receives a JSON body and returns a response payload or error.
func HandleJSON[Req any, Resp any](
	fn func(c *gin.Context, req Req) (Resp, error),
) gin.HandlerFunc {
	reqType := reflect.TypeOf((*Req)(nil)).Elem()
	return func(c *gin.Context) {
		var req Req
		if err := c.ShouldBindJSON(&req); err != nil {
			HandleBindError(c, err, reqType)
			return
		}

		resp, err := fn(c, req)
		if c.IsAborted() {
			return
		}
		if err != nil {
			WriteError(c, err)
			return
		}
		if !c.Writer.Written() {
			Success(c, resp)
		}
	}
}

// Handle wraps a handler that takes no request body and returns a response payload or error.
func Handle[Resp any](
	fn func(c *gin.Context) (Resp, error),
) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp, err := fn(c)
		if c.IsAborted() {
			return
		}
		if err != nil {
			WriteError(c, err)
			return
		}
		if !c.Writer.Written() {
			Success(c, resp)
		}
	}
}
