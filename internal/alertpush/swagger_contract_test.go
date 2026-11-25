package alertpush

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/swaggo/swag"
	"go.uber.org/zap"

	_ "github.com/nikonikowuw/Zhulong/internal/apidocs"
)

type alertpushSwaggerSpec struct {
	BasePath    string                                     `json:"basePath"`
	Paths       map[string]map[string]swaggerOperationSpec `json:"paths"`
	Definitions map[string]swaggerDefinitionSpec           `json:"definitions"`
}

type swaggerOperationSpec struct {
	Produces  []string       `json:"produces"`
	Responses map[string]any `json:"responses"`
}

type swaggerDefinitionSpec struct {
	Properties map[string]any `json:"properties"`
}

type alertpushRoute struct {
	method string
	path   string
}

func normalizeAlertPushPath(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		switch {
		case strings.HasPrefix(seg, ":"):
			segments[i] = "{" + strings.TrimPrefix(seg, ":") + "}"
		case strings.HasPrefix(seg, "*"):
			segments[i] = "{" + strings.TrimPrefix(seg, "*") + "}"
		}
	}
	return strings.Join(segments, "/")
}

func TestAlertPushContract_RoutesAndSchemaMatchSwagger(t *testing.T) {
	gin.SetMode(gin.TestMode)

	specRaw, err := swag.ReadDoc()
	if err != nil {
		t.Fatalf("swag.ReadDoc: %v", err)
	}
	var spec alertpushSwaggerSpec
	if err := json.Unmarshal([]byte(specRaw), &spec); err != nil {
		t.Fatalf("decode swagger json: %v", err)
	}
	if spec.BasePath != "/api/v1" {
		t.Fatalf("swagger basePath = %q, want /api/v1", spec.BasePath)
	}

	// Mount actual handlers under /api/v1.
	router := gin.New()
	api := router.Group("/api/v1")
	h := NewHandler(nil, nil, zap.NewNop())
	h.RegisterRoutes(api)
	smh := NewSignedMediaHandler(NewEmptyURLSigner(), nil, zap.NewNop())
	smh.RegisterRoutes(api)

	mounted := make(map[alertpushRoute]bool)
	for _, r := range router.Routes() {
		mounted[alertpushRoute{
			method: r.Method,
			path:   normalizeAlertPushPath(r.Path),
		}] = true
	}

	documented := make(map[alertpushRoute]bool)
	for path, ops := range spec.Paths {
		isAlertPush := strings.HasPrefix(path, "/alert-destinations") ||
			strings.HasPrefix(path, "/media/signed/")
		if !isAlertPush {
			continue
		}
		for method := range ops {
			documented[alertpushRoute{
				method: strings.ToUpper(method),
				path:   path,
			}] = true
		}
	}

	for r := range mounted {
		if !documented[r] {
			t.Errorf("mounted route %s %s is missing from Swagger annotations", r.method, r.path)
		}
	}
	for r := range documented {
		if !mounted[r] {
			t.Errorf("documented route %s %s is not mounted by alertpush handlers", r.method, r.path)
		}
	}

	// Verify key status codes and response schemas are kept up to date.
	signedGet, ok := spec.Paths["/media/signed/{path}"]["get"]
	if !ok {
		t.Fatal("missing GET /media/signed/{path} in swagger")
	}
	for _, code := range []string{"200", "206", "302", "403", "404", "416", "429", "500", "503"} {
		if _, ok := signedGet.Responses[code]; !ok {
			t.Errorf("GET /media/signed/{path} missing response status %s in swagger", code)
		}
	}
	if _, ok := spec.Paths["/media/signed/{path}"]["head"]; !ok {
		t.Error("missing HEAD /media/signed/{path} in swagger")
	}

	testPost, ok := spec.Paths["/alert-destinations/test"]["post"]
	if !ok {
		t.Fatal("missing POST /alert-destinations/test in swagger")
	}
	for _, code := range []string{"200", "400", "401", "404", "422", "429", "500"} {
		if _, ok := testPost.Responses[code]; !ok {
			t.Errorf("POST /alert-destinations/test missing response status %s in swagger", code)
		}
	}

	for _, path := range []string{
		"/alert-destinations",
		"/alert-destinations/{id}",
		"/alert-destinations/{id}/enable",
		"/alert-destinations/{id}/disable",
	} {
		for method, op := range spec.Paths[path] {
			if _, ok := op.Responses["401"]; !ok {
				t.Errorf("%s %s missing 401 response in swagger", strings.ToUpper(method), path)
			}
		}
	}

	// Verify newly added schema properties exist in generated definitions.
	respDef := spec.Definitions["alertpush.AlertDestinationResponse"]
	if _, ok := respDef.Properties["signingSecretSet"]; !ok {
		t.Error("alertpush.AlertDestinationResponse missing signingSecretSet in swagger")
	}
	secretDef := spec.Definitions["alertpush.AlertDestinationSecretInfo"]
	if _, ok := secretDef.Properties["signingSecret"]; !ok {
		t.Error("alertpush.AlertDestinationSecretInfo missing signingSecret in swagger")
	}
	updDef := spec.Definitions["alertpush.UpdateAlertDestinationRequest"]
	if _, ok := updDef.Properties["channelType"]; !ok {
		t.Error("alertpush.UpdateAlertDestinationRequest missing channelType in swagger")
	}
}
