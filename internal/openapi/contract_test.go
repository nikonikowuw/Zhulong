package openapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "github.com/nikonikowuw/Zhulong/internal/apidocs"
	"github.com/swaggo/swag"
)

// swaggerSpec is the subset of the generated spec this contract test reads. Parsing the
// whole document through swag keeps the test tied to the artifact the release ships
// rather than to a hand-maintained list of paths.
type swaggerSpec struct {
	BasePath string                    `json:"basePath"`
	Paths    map[string]map[string]any `json:"paths"`
}

// route is one (method, path) pair in a comparable form.
type route struct {
	method string
	path   string
}

// normalizedPath strips the router group prefix and rewrites Gin's ":param" segments into
// the OpenAPI "{param}" form, so the two sources can be compared directly.
func normalizedPath(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return strings.Join(segments, "/")
}

// openAPIRoutes walks the same registration the server uses and returns every route that
// belongs to this package.
func openAPIRoutes(t *testing.T, env *openAPITestEnv) map[route]bool {
	t.Helper()

	routes := make(map[route]bool)
	for _, registered := range env.router.Routes() {
		path := normalizedPath(registered.Path)
		if !strings.HasPrefix(path, "/openapi/") {
			continue
		}
		routes[route{method: registered.Method, path: path}] = true
	}
	return routes
}

// swaggerOpenAPIRoutes returns the OpenAPI routes declared in the generated spec.
func swaggerOpenAPIRoutes(t *testing.T) (string, map[route]bool) {
	t.Helper()

	spec, err := swag.ReadDoc()
	if err != nil {
		t.Fatalf("read generated swagger document: %v", err)
	}

	var parsed swaggerSpec
	if err := json.Unmarshal([]byte(spec), &parsed); err != nil {
		t.Fatalf("decode generated swagger document: %v", err)
	}

	routes := make(map[route]bool)
	for path, operations := range parsed.Paths {
		if !strings.HasPrefix(path, "/openapi/") {
			continue
		}
		for method := range operations {
			routes[route{method: strings.ToUpper(method), path: path}] = true
		}
	}
	return parsed.BasePath, routes
}

// TestContract_RoutesMatchSwagger keeps the generated documentation and the mounted
// router from drifting apart. A handler added without an annotation — or an annotation
// left behind by a removed endpoint — makes the shipped Swagger UI describe an API that
// does not exist.
func TestContract_RoutesMatchSwagger(t *testing.T) {
	env := newOpenAPITestEnv(t)

	basePath, documented := swaggerOpenAPIRoutes(t)
	if basePath != "/api/v1" {
		t.Fatalf("swagger basePath = %q, want /api/v1 (normalizedPath assumes it)", basePath)
	}

	mounted := openAPIRoutes(t, env)

	for r := range mounted {
		if !documented[r] {
			t.Errorf("%s %s is mounted but missing from the Swagger annotations", r.method, r.path)
		}
	}
	for r := range documented {
		if !mounted[r] {
			t.Errorf("%s %s is documented but not mounted", r.method, r.path)
		}
	}
}

// TestContract_DocumentedRoutesAreServed resolves each path an integrator is told to
// call against the mounted router. It catches an annotation pointing at the wrong method
// or a path shape the router never registered.
func TestContract_DocumentedRoutesAreServed(t *testing.T) {
	env := newOpenAPITestEnv(t)
	mounted := openAPIRoutes(t, env)

	documented := []route{
		{method: "POST", path: "/openapi/persons/{personId}"},
		{method: "GET", path: "/openapi/persons/{personId}"},
		{method: "DELETE", path: "/openapi/persons/{personId}"},
		{method: "POST", path: "/openapi/persons/{personId}/faces"},
		{method: "DELETE", path: "/openapi/persons/{personId}/faces"},
		{method: "DELETE", path: "/openapi/persons/{personId}/faces/{faceId}"},
		{method: "PUT", path: "/openapi/persons/{personId}/faces/{faceId}/primary"},
	}

	for _, entry := range documented {
		if !routeExists(mounted, entry) {
			t.Errorf("%s %s is not mounted", entry.method, entry.path)
		}
	}
}

// TestContract_DocsMatchImplementation keeps the third-party guide honest. The guide is a
// deliverable in its own right, and every path it tells a caller to use has to exist.
func TestContract_DocsMatchImplementation(t *testing.T) {
	env := newOpenAPITestEnv(t)
	mounted := openAPIRoutes(t, env)

	guidePath := filepath.Join("..", "..", "docs", "openapi", "person-face.md")
	guide, err := os.ReadFile(guidePath)
	if err != nil {
		t.Fatalf("read %s: %v", guidePath, err)
	}

	// Every "/openapi/..." literal in the guide must resolve to a mounted route once its
	// {placeholders} are translated back to the Gin form.
	var found []route
	for _, candidate := range extractOpenAPIPaths(string(guide)) {
		if routeExists(mounted, candidate) {
			found = append(found, candidate)
			continue
		}
		t.Errorf("guide references %s %s, which is not mounted", candidate.method, candidate.path)
	}
	if len(found) == 0 {
		t.Fatalf("no /openapi paths found in %s; the contract check is not looking at the guide", guidePath)
	}

	// Each mounted route has to appear in the guide, or a caller cannot discover it.
	for r := range mounted {
		documented := false
		for _, candidate := range found {
			if candidate == r {
				documented = true
				break
			}
		}
		if !documented {
			t.Errorf("%s %s has no entry in %s", r.method, r.path, guidePath)
		}
	}
}

// extractOpenAPIPaths finds "<METHOD> .../openapi/..." mentions in the guide. Tables and
// code blocks both use the same shape: a method token followed by a path.
func extractOpenAPIPaths(guide string) []route {
	methods := map[string]bool{
		"GET":    true,
		"POST":   true,
		"PUT":    true,
		"DELETE": true,
	}

	var routes []route
	seen := make(map[route]bool)
	for _, line := range strings.Split(guide, "\n") {
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r == '`' || r == '|' || r == ' ' || r == '\t' || r == '*' || r == '"'
		})
		for i, field := range fields {
			method := strings.TrimSuffix(strings.ToUpper(field), ":")
			if !methods[method] || i+1 >= len(fields) {
				continue
			}
			path := fields[i+1]
			if !strings.Contains(path, "/openapi/") {
				continue
			}
			// Drop a query string or trailing punctuation picked up from prose.
			if idx := strings.IndexAny(path, "?#"); idx >= 0 {
				path = path[:idx]
			}
			if idx := strings.Index(path, "/openapi/"); idx >= 0 {
				path = path[idx:]
			}
			candidate := route{method: method, path: path}
			if !seen[candidate] {
				seen[candidate] = true
				routes = append(routes, candidate)
			}
		}
	}
	return routes
}

// routeExists reports whether mounted holds a route matching candidate, treating
// "{placeholder}" and ":param" segments as equivalent positions.
func routeExists(mounted map[route]bool, candidate route) bool {
	if mounted[candidate] {
		return true
	}
	for r := range mounted {
		if r.method != candidate.method {
			continue
		}
		if samePathShape(r.path, candidate.path) {
			return true
		}
	}
	return false
}

// samePathShape compares two paths segment by segment. A placeholder segment on either
// side matches whatever the other side holds at that position, which is what lets a
// documented concrete example ("/persons/EMP-8080") be checked against the mounted
// template ("/persons/:personId").
func samePathShape(left, right string) bool {
	leftSegments := strings.Split(left, "/")
	rightSegments := strings.Split(right, "/")
	if len(leftSegments) != len(rightSegments) {
		return false
	}
	for i := range leftSegments {
		leftSegment, rightSegment := leftSegments[i], rightSegments[i]
		if isPlaceholderSegment(leftSegment) || isPlaceholderSegment(rightSegment) {
			continue
		}
		if leftSegment != rightSegment {
			return false
		}
	}
	return true
}

// isPlaceholderSegment reports whether path is a variable segment in either notation:
// Gin's ":personId" or OpenAPI's "{personId}".
func isPlaceholderSegment(segment string) bool {
	return strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "{")
}

// TestContract_ResponseFieldsMatchProjection asserts the JSON field names the guide
// documents. The guide is the only place a third-party integrator reads the payload
// shape, so a renamed field must fail here rather than in their parser.
func TestContract_ResponseFieldsMatchProjection(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-FIELDS", "Fields")
	if rec := env.uploadFace(t, "EMP-FIELDS", "", "a.jpg", mockJPEGBytes); rec.Code != 201 {
		t.Fatalf("upload: got %d: %s", rec.Code, rec.Body.String())
	}

	rec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FIELDS", nil))
	var person map[string]any
	decodeData(t, rec, &person)

	wantPersonFields := []string{
		"personId", "name", "primaryFaceId", "faces", "createdAt", "updatedAt",
	}
	requireFields(t, "person", person, wantPersonFields)

	faces, ok := person["faces"].([]any)
	if !ok || len(faces) != 1 {
		t.Fatalf("faces = %v, want one face", person["faces"])
	}
	face, ok := faces[0].(map[string]any)
	if !ok {
		t.Fatalf("face = %v, want an object", faces[0])
	}
	requireFields(t, "face", face, []string{"faceId", "isPrimary", "createdAt"})

	// The internal file name encodes the person row id and has no consumer here.
	if _, present := face["imagePath"]; present {
		t.Errorf("face projection exposes the internal imagePath")
	}
}

// requireFields fails when obj's keys differ from want, in either direction.
func requireFields(t *testing.T, label string, obj map[string]any, want []string) {
	t.Helper()

	got := make([]string, 0, len(obj))
	for key := range obj {
		got = append(got, key)
	}
	sort.Strings(got)

	expected := append([]string(nil), want...)
	sort.Strings(expected)

	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("%s fields = %v, want %v", label, got, expected)
	}
}

// TestContract_EmptyCollectionsSerializeAsArrays guards the null-versus-empty contract
// the guide states for every response collection.
func TestContract_EmptyCollectionsSerializeAsArrays(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-EMPTY", "Empty")

	rec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-EMPTY", nil))
	if strings.Contains(rec.Body.String(), `"faces":null`) {
		t.Fatalf("empty face list serialized as null: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"faces":[]`) {
		t.Fatalf("empty face list did not serialize as []: %s", rec.Body.String())
	}
}
