package spatial_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/spatial"
	"go.uber.org/zap"
)

func setupTestHandler(t *testing.T) (*gin.Engine, *spatial.Service, database.DBProvider, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	mapsDir := filepath.Join(tempDir, "maps")
	comp := spatial.NewSpatialComponents(dbStore, mapsDir, zap.NewNop())
	// The maps directory is created by the domain start hook (constructors are pure).
	if err := comp.Lifecycle.Start(context.Background()); err != nil {
		t.Fatalf("start spatial lifecycle: %v", err)
	}

	r := gin.New()
	api := r.Group("/api/v1")
	comp.ProtectedRoutes.RegisterRoutes(api)
	comp.UploadRoutes.RegisterRoutes(api)

	cleanup := func() {
		_ = dbStore.Close()
	}
	return r, comp.Service, dbStore, cleanup
}

func TestHandler_SpatialEndpoints(t *testing.T) {
	router, _, dbProvider, cleanup := setupTestHandler(t)
	defer cleanup()

	// Seed a camera
	seedCamera := "INSERT INTO cameras (id, name, enabled) VALUES ('cam-test', '测试相机', 1)"
	if err := dbProvider.DB().Exec(seedCamera).Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}

	// 1. Upload map
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "西区园区图")
	part, _ := writer.CreateFormFile("file", "west.png")
	_, _ = part.Write(createTestPNG(1280, 720))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/spatial/maps", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d: %s", w.Code, w.Body.String())
	}

	var uploadResp struct {
		Data spatial.CanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	mapID := uploadResp.Data.ID
	if mapID == "" || uploadResp.Data.Name != "西区园区图" {
		t.Fatalf("unexpected upload data: %+v", uploadResp.Data)
	}

	// 2. List maps
	req = httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	// 3. Get map image
	req = httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps/"+mapID+"/image", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for map image, got: %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Errorf("expected image/png content type, got: %s", w.Header().Get("Content-Type"))
	}

	// 4. Update map name
	patchBody, _ := json.Marshal(spatial.UpdateCanvasMapRequest{Name: "西区新图"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/spatial/maps/"+mapID, bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for patch name, got: %d", w.Code)
	}

	// 5. Save positions
	saveReq, _ := json.Marshal(spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{
			{CameraID: "cam-test", X: 0.3, Y: 0.4, Angle: 120},
		},
	})
	req = httptest.NewRequest(http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions", bytes.NewReader(saveReq))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for save positions, got: %d: %s", w.Code, w.Body.String())
	}

	// 6. Get positions
	req = httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps/"+mapID+"/positions", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for get positions, got: %d", w.Code)
	}
	var posResp struct {
		Data []spatial.CameraPositionInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &posResp); err != nil {
		t.Fatalf("unmarshal pos response: %v", err)
	}
	if len(posResp.Data) != 1 || posResp.Data[0].CameraID != "cam-test" {
		t.Fatalf("unexpected positions: %+v", posResp.Data)
	}

	// 7. Query trajectory (empty result check)
	now := time.Now().UTC()
	startStr := now.Add(-time.Hour).Format(time.RFC3339)
	endStr := now.Format(time.RFC3339)
	trajURL := "/api/v1/spatial/trajectory?mapId=" + mapID +
		"&targetType=vehicle&plate=粤B99999" +
		"&startTime=" + startStr +
		"&endTime=" + endStr
	req = httptest.NewRequest(http.MethodGet, trajURL, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for trajectory query, got: %d: %s", w.Code, w.Body.String())
	}
	var trajResp struct {
		Data spatial.TrajectoryResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &trajResp); err != nil {
		t.Fatalf("unmarshal trajectory: %v", err)
	}
	if trajResp.Data.TargetKey != "粤B99999" || len(trajResp.Data.Nodes) != 0 {
		t.Fatalf("unexpected empty trajectory response: %+v", trajResp.Data)
	}

	// 8. Delete map
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/spatial/maps/"+mapID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for delete map, got: %d", w.Code)
	}
}

// uploadTestMap posts a valid PNG base map and returns its ID.
func uploadTestMap(t *testing.T, router *gin.Engine) string {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "校验底图"); err != nil {
		t.Fatalf("write name field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "check.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(createTestPNG(64, 48)); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/spatial/maps", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload map: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data spatial.CanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	return resp.Data.ID
}

// TestHandler_ValidationRejectsOutOfRangePositions 守护 BatchSavePositionsRequest
// 的 `dive`：没有它，元素级 gte/lte 根本不会执行，x=1.5 / angle=370 会被静默接受
// 并在 service 里被改写成别的值。
func TestHandler_ValidationRejectsOutOfRangePositions(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	mapID := uploadTestMap(t, router)

	cases := []struct {
		name string
		body string
	}{
		{
			name: "x beyond 1.0",
			body: `{"positions":[{"cameraId":"cam-test","x":1.5,"y":0.5,"angle":90}]}`,
		},
		{
			name: "negative y",
			body: `{"positions":[{"cameraId":"cam-test","x":0.5,"y":-0.1,"angle":90}]}`,
		},
		{
			name: "angle at 360 (range is [0,360))",
			body: `{"positions":[{"cameraId":"cam-test","x":0.5,"y":0.5,"angle":360}]}`,
		},
		{
			name: "missing cameraId",
			body: `{"positions":[{"x":0.5,"y":0.5,"angle":90}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api/v1/spatial/maps/" + mapID + "/positions"
			req := httptest.NewRequest(http.MethodPut, url, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestHandler_SavePositionsDistinguishesAbsentFromEmpty 守护「同一份空语义不能被当成两种意思」：
// 缺失 positions 是坏请求，空数组是「解绑全部」。
func TestHandler_SavePositionsDistinguishesAbsentFromEmpty(t *testing.T) {
	router, _, dbProvider, cleanup := setupTestHandler(t)
	defer cleanup()

	seedCamera := "INSERT INTO cameras (id, name, enabled) VALUES ('cam-test', '测试相机', 1)"
	if err := dbProvider.DB().Exec(seedCamera).Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	mapID := uploadTestMap(t, router)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("absent positions must be rejected, got %d: %s", w.Code, w.Body.String())
	}
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp.Code != "POSITIONS_REQUIRED" {
		t.Fatalf("expected POSITIONS_REQUIRED, got %s", errResp.Code)
	}

	// Save one position, then explicitly unbind everything with an empty array.
	req = httptest.NewRequest(http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions",
		bytes.NewBufferString(`{"positions":[{"cameraId":"cam-test","x":0.5,"y":0.5,"angle":90}]}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save one position: %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions",
		bytes.NewBufferString(`{"positions":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("empty array must be accepted, got %d: %s", w.Code, w.Body.String())
	}
	var saveResp struct {
		Data spatial.SaveCameraPositionsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("unmarshal save response: %v", err)
	}
	if saveResp.Data.Count != 0 || saveResp.Data.MapID != mapID {
		t.Fatalf("unexpected save response: %+v", saveResp.Data)
	}
}

// TestHandler_MapImageCarriesSandboxHeaders 守护上传的 SVG 不能在应用同源下执行脚本。
func TestHandler_MapImageCarriesSandboxHeaders(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "矢量底图"); err != nil {
		t.Fatalf("write name field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "plan.svg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="240"></svg>`)
	if _, err := part.Write(svg); err != nil {
		t.Fatalf("write svg: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/spatial/maps", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload svg: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Data spatial.CanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	if created.Data.Width != 320 || created.Data.Height != 240 {
		t.Fatalf("svg intrinsic size not persisted: %+v", created.Data)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps/"+created.Data.ID+"/image", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get image: expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("map image must be served with a sandboxing CSP")
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected nosniff, got %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got == "public, max-age=86400" {
		t.Errorf("an authenticated map image must not be publicly cacheable, got %q", got)
	}
}

// TestHandler_ErrorsAreLocalized 守护 business locale 注册：未注册的码会回落成
// 泛化的 REQUEST_FAILED，中文用户只能看到「请求失败」。
func TestHandler_ErrorsAreLocalized(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spatial/maps/missing-map", nil)
	req.Header.Set("Accept-Language", "zh-Hans")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if resp.Code != "MAP_NOT_FOUND" {
		t.Fatalf("expected MAP_NOT_FOUND, got %s", resp.Code)
	}
	if resp.Message != "底图不存在" {
		t.Fatalf("expected the localized catalog text, got %q", resp.Message)
	}
}

// TestHandler_TrajectoryRejectsInvertedRange 守护时间区间顺序在 service 层就挡住。
func TestHandler_TrajectoryRejectsInvertedRange(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	mapID := uploadTestMap(t, router)
	now := time.Now().UTC()
	url := "/api/v1/spatial/trajectory?mapId=" + mapID +
		"&targetType=vehicle&plate=B12345" +
		"&startTime=" + now.Format(time.RFC3339) +
		"&endTime=" + now.Add(-time.Hour).Format(time.RFC3339)

	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
