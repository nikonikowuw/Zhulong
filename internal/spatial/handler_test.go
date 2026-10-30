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
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}
	return r, comp.Service, dbStore, cleanup
}

// doRequest fires one request through the router and returns the recorder.
func doRequest(t *testing.T, router *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// doJSON fires one JSON request against the API group.
func doJSON(t *testing.T, router *gin.Engine, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return doRequest(t, router, req)
}

// seedCamera inserts the camera every position test calibrates.
func seedCamera(t *testing.T, dbProvider database.DBProvider) {
	t.Helper()
	seed := "INSERT INTO cameras (id, name, enabled) VALUES ('cam-test', '测试相机', 1)"
	if err := dbProvider.DB().Exec(seed).Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}
}

// uploadMapFile posts a multipart base map. An empty name omits the field entirely,
// so a test can assert the missing-name contract.
func uploadMapFile(
	t *testing.T,
	router *gin.Engine,
	name string,
	filename string,
	content []byte,
) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if name != "" {
		if err := writer.WriteField("name", name); err != nil {
			t.Fatalf("write name field: %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/spatial/maps", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return doRequest(t, router, req)
}

// uploadTestMap posts a valid PNG base map and returns its ID.
func uploadTestMap(t *testing.T, router *gin.Engine) string {
	t.Helper()

	w := uploadMapFile(t, router, "校验底图", "check.png", createTestPNG(t, 64, 48))
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

// TestHandler_UploadListAndRenameMap walks the map lifecycle: create one map through
// the upload endpoint, see it in the list, rename it, then delete it.
func TestHandler_UploadListAndRenameMap(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	w := uploadMapFile(t, router, "西区园区图", "west.png", createTestPNG(t, 1280, 720))
	if w.Code != http.StatusCreated {
		t.Fatalf("upload map: expected 201, got %d: %s", w.Code, w.Body.String())
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

	w = doJSON(t, router, http.MethodGet, "/api/v1/spatial/maps", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list maps: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Data []spatial.CanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(listResp.Data) != 1 || listResp.Data[0].ID != mapID {
		t.Fatalf("unexpected map list: %+v", listResp.Data)
	}

	patchBody, err := json.Marshal(spatial.UpdateCanvasMapRequest{Name: "西区新图"})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	w = doJSON(t, router, http.MethodPatch, "/api/v1/spatial/maps/"+mapID, string(patchBody))
	if w.Code != http.StatusOK {
		t.Fatalf("patch name: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var patchResp struct {
		Data spatial.CanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &patchResp); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	if patchResp.Data.Name != "西区新图" {
		t.Fatalf("rename did not persist: %+v", patchResp.Data)
	}

	w = doJSON(t, router, http.MethodDelete, "/api/v1/spatial/maps/"+mapID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete map: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var delResp struct {
		Data spatial.DeleteCanvasMapResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &delResp); err != nil {
		t.Fatalf("unmarshal delete response: %v", err)
	}
	if delResp.Data.ID != mapID {
		t.Fatalf("delete reported %q, want %q", delResp.Data.ID, mapID)
	}
}

// TestHandler_UploadRejectsMissingName 守护名称校验的单点归属：handler 不再预检空名称，
// 缺名上传必须仍由 service 的 normalizeMapName 拒成 422 INVALID_MAP_NAME。
func TestHandler_UploadRejectsMissingName(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	w := uploadMapFile(t, router, "", "west.png", createTestPNG(t, 32, 32))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if resp.Code != "INVALID_MAP_NAME" {
		t.Fatalf("expected INVALID_MAP_NAME, got %s", resp.Code)
	}
}

// TestHandler_SaveAndGetPositions covers the calibration round trip.
func TestHandler_SaveAndGetPositions(t *testing.T) {
	router, _, dbProvider, cleanup := setupTestHandler(t)
	defer cleanup()

	seedCamera(t, dbProvider)
	mapID := uploadTestMap(t, router)

	w := doJSON(t, router, http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions",
		`{"positions":[{"cameraId":"cam-test","x":0.3,"y":0.4,"angle":120}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save positions: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var saveResp struct {
		Data spatial.SaveCameraPositionsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("unmarshal save response: %v", err)
	}
	if saveResp.Data.Count != 1 || saveResp.Data.MapID != mapID {
		t.Fatalf("unexpected save response: %+v", saveResp.Data)
	}

	w = doJSON(t, router, http.MethodGet, "/api/v1/spatial/maps/"+mapID+"/positions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get positions: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var posResp struct {
		Data []spatial.CameraPositionInfo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &posResp); err != nil {
		t.Fatalf("unmarshal positions: %v", err)
	}
	if len(posResp.Data) != 1 || posResp.Data[0].CameraID != "cam-test" {
		t.Fatalf("unexpected positions: %+v", posResp.Data)
	}
}

// TestHandler_QueryTrajectoryReturnsEmptyForUnknownPlate keeps the empty-result path
// covered now that the monolithic endpoint test is gone: a calibrated map with no
// matching capture must answer 200 with zero nodes, not 404.
func TestHandler_QueryTrajectoryReturnsEmptyForUnknownPlate(t *testing.T) {
	router, _, dbProvider, cleanup := setupTestHandler(t)
	defer cleanup()

	seedCamera(t, dbProvider)
	mapID := uploadTestMap(t, router)
	w := doJSON(t, router, http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions",
		`{"positions":[{"cameraId":"cam-test","x":0.5,"y":0.5,"angle":0}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save positions: %d: %s", w.Code, w.Body.String())
	}

	now := time.Now().UTC()
	url := "/api/v1/spatial/trajectory?mapId=" + mapID +
		"&targetType=vehicle&plate=粤B99999" +
		"&startTime=" + now.Add(-time.Hour).Format(time.RFC3339) +
		"&endTime=" + now.Format(time.RFC3339)

	w = doJSON(t, router, http.MethodGet, url, "")
	if w.Code != http.StatusOK {
		t.Fatalf("trajectory query: expected 200, got %d: %s", w.Code, w.Body.String())
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
			w := doJSON(t, router, http.MethodPut, "/api/v1/spatial/maps/"+mapID+"/positions", tc.body)
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

	seedCamera(t, dbProvider)
	mapID := uploadTestMap(t, router)
	positionsURL := "/api/v1/spatial/maps/" + mapID + "/positions"

	w := doJSON(t, router, http.MethodPut, positionsURL, `{}`)
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
	w = doJSON(t, router, http.MethodPut, positionsURL,
		`{"positions":[{"cameraId":"cam-test","x":0.5,"y":0.5,"angle":90}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save one position: %d: %s", w.Code, w.Body.String())
	}

	w = doJSON(t, router, http.MethodPut, positionsURL, `{"positions":[]}`)
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

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="240"></svg>`)
	w := uploadMapFile(t, router, "矢量底图", "plan.svg", svg)
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

	w = doJSON(t, router, http.MethodGet, "/api/v1/spatial/maps/"+created.Data.ID+"/image", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get image: expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("expected image/svg+xml content type, got %q", got)
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
	w := doRequest(t, router, req)

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

	w := doJSON(t, router, http.MethodGet, url, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
