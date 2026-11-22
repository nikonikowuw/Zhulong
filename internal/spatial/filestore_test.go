package spatial_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/spatial"
	"go.uber.org/zap"
)

func TestSpatial_FileStoreSVGSandboxHeadersAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gin.SetMode(gin.TestMode)

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})

	memStore := filestore.NewMemoryDriver()
	store := spatial.NewStore(dbStore)
	svc := spatial.NewServiceWithStore(
		store,
		t.TempDir(),
		memStore,
		zap.NewNop(),
	)
	handler := spatial.NewHandler(svc, zap.NewNop())

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	svgBody := `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480"><rect width="640" height="480"/></svg>`
	created, err := svc.CreateMap(
		ctx,
		"CampusSVG",
		strings.NewReader(svgBody),
		"campus.svg",
	)
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	if created.Width != 640 || created.Height != 480 {
		t.Fatalf("unexpected SVG dimensions: %+v", created)
	}

	remoteKey := "maps/" + created.ID + ".svg"
	if ok, existsErr := memStore.Exists(ctx, remoteKey); existsErr != nil || !ok {
		t.Fatalf(
			"expected SVG stored at %q in filestore, ok=%v err=%v",
			remoteKey,
			ok,
			existsErr,
		)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps/"+created.ID+"/image", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 serving SVG map, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("missing SVG CSP sandbox header: %q", rec.Header().Get("Content-Security-Policy"))
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte(svgBody)) {
		t.Fatalf("unexpected served SVG body: %q", rec.Body.String())
	}

	if err := svc.DeleteMap(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMap: %v", err)
	}
	if ok, existsErr := memStore.Exists(ctx, remoteKey); existsErr != nil || ok {
		t.Fatalf("expected SVG deleted from filestore, ok=%v err=%v", ok, existsErr)
	}
}
