package spatial_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/spatial"
	"go.uber.org/zap"
)

func createTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func setupTestService(t *testing.T) (*spatial.Service, database.DBProvider, func()) {
	t.Helper()
	svc, dbProvider, _, cleanup := setupTestServiceWithDir(t)
	return svc, dbProvider, cleanup
}

// setupTestServiceWithDir also exposes the maps directory, so a test can assert
// that a rejected upload leaves no file behind.
func setupTestServiceWithDir(t *testing.T) (*spatial.Service, database.DBProvider, string, func()) {
	t.Helper()
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	mapsDir := filepath.Join(tempDir, "maps")
	// The directory is the start hook's job, not the constructor's.
	if err := os.MkdirAll(mapsDir, 0o755); err != nil {
		t.Fatalf("create maps dir: %v", err)
	}
	store := spatial.NewStore(dbStore)
	svc := spatial.NewService(store, mapsDir, zap.NewNop())

	cleanup := func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}
	return svc, dbStore, mapsDir, cleanup
}

func TestService_CreateAndGetMap(t *testing.T) {
	ctx := context.Background()
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	pngBytes := createTestPNG(t, 800, 600)

	// 1. Invalid name
	_, err := svc.CreateMap(ctx, "   ", bytes.NewReader(pngBytes), "map.png")
	if err == nil {
		t.Error("expected error for empty name, got nil")
	}

	// 2. Unsupported format
	_, err = svc.CreateMap(ctx, "测试图", bytes.NewReader(pngBytes), "map.exe")
	if err == nil {
		t.Error("expected error for invalid ext, got nil")
	}

	// 3. Valid creation
	created, err := svc.CreateMap(ctx, "主园区平面图", bytes.NewReader(pngBytes), "map.png")
	if err != nil {
		t.Fatalf("CreateMap failed: %v", err)
	}
	if created.Name != "主园区平面图" || created.Width != 800 || created.Height != 600 {
		t.Errorf("unexpected created map response: %+v", created)
	}

	// 4. GetMapFile
	filePath, mimeType, err := svc.GetMapFile(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMapFile failed: %v", err)
	}
	if filePath == "" || mimeType != "image/png" {
		t.Errorf("unexpected map file path or mime: %s, %s", filePath, mimeType)
	}

	// 5. List
	list, err := svc.ListMaps(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListMaps failed: %v, len: %d", err, len(list))
	}

	// 6. Delete
	if err := svc.DeleteMap(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMap failed: %v", err)
	}
	_, _, err = svc.GetMapFile(ctx, created.ID)
	if err == nil {
		t.Error("expected error after delete, got nil")
	}
}

func TestService_PositionsAndTrajectory(t *testing.T) {
	ctx := context.Background()
	svc, dbProvider, cleanup := setupTestService(t)
	defer cleanup()

	// Seed cameras
	if err := dbProvider.DB().Exec(`
		INSERT INTO cameras (id, name, enabled) VALUES 
		('cam_a', '东门入口', 1),
		('cam_b', '园区环道', 1),
		('cam_c', '地库闸机', 1)
	`).Error; err != nil {
		t.Fatalf("seed cameras failed: %v", err)
	}

	// Create map
	pngBytes := createTestPNG(t, 1920, 1080)
	createdMap, err := svc.CreateMap(ctx, "全景图", bytes.NewReader(pngBytes), "plan.png")
	if err != nil {
		t.Fatalf("CreateMap failed: %v", err)
	}

	// Save positions
	_, err = svc.SavePositions(ctx, createdMap.ID, spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{
			{CameraID: "cam_a", X: 0.1, Y: 0.2, Angle: 45},
			{CameraID: "cam_b", X: 0.5, Y: 0.5, Angle: 90},
			{CameraID: "cam_c", X: 0.8, Y: 0.8, Angle: 180},
		},
	})
	if err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}

	positions, err := svc.GetPositions(ctx, createdMap.ID)
	if err != nil || len(positions) != 3 {
		t.Fatalf("GetPositions failed: %v, len=%d", err, len(positions))
	}

	// Seed vehicle captures
	now := time.Now().UTC().Truncate(time.Second)
	t1 := now.Add(-10 * time.Minute)
	t2 := t1.Add(10 * time.Second) // same camera, within dwell window (10s <= 30s)
	t3 := t1.Add(2 * time.Minute)  // next camera cam_b

	if err := dbProvider.DB().Exec(`
		INSERT INTO captures (
			id, camera_id, target_type, score, crop_path,
			image_path, attributes_json, pts, captured_at
		) VALUES 
		('c1', 'cam_a', 'vehicle', 0.80, 'crops/c1.jpg', 'images/c1.jpg', '{"plate_number":"粤B88888"}', 100, ?),
		('c2', 'cam_a', 'vehicle', 0.95, 'crops/c2.jpg', 'images/c2.jpg', '{"plate_number":"粤B88888"}', 200, ?),
		('c3', 'cam_b', 'vehicle', 0.88, 'crops/c3.jpg', 'images/c3.jpg', '{"plate_number":"粤B88888"}', 300, ?)
	`, t1, t2, t3).Error; err != nil {
		t.Fatalf("seed captures failed: %v", err)
	}

	// Query vehicle trajectory
	traj, err := svc.QueryTrajectory(ctx, spatial.TrajectoryQuery{
		MapID:      createdMap.ID,
		TargetType: "vehicle",
		Plate:      "粤B88888",
		StartTime:  now.Add(-30 * time.Minute),
		EndTime:    now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("QueryTrajectory failed: %v", err)
	}

	if len(traj.Nodes) != 2 {
		t.Fatalf("expected 2 aggregated nodes, got %d", len(traj.Nodes))
	}

	// First node: cam_a, dwellSec = 10s, best score = 0.95, best crop = crops/c2.jpg
	node1 := traj.Nodes[0]
	if node1.CameraID != "cam_a" || node1.Seq != 1 {
		t.Errorf("unexpected node 1 camera/seq: %s, %d", node1.CameraID, node1.Seq)
	}
	if node1.DwellSec != 10 {
		t.Errorf("expected dwellSec 10, got %d", node1.DwellSec)
	}
	if node1.Score != 0.95 || node1.SnapshotPath != "crops/c2.jpg" {
		t.Errorf("expected best score 0.95 and c2 crop, got %f, %s", node1.Score, node1.SnapshotPath)
	}

	// Second node: cam_b, dwellSec = 0
	node2 := traj.Nodes[1]
	if node2.CameraID != "cam_b" || node2.Seq != 2 {
		t.Errorf("unexpected node 2 camera/seq: %s, %d", node2.CameraID, node2.Seq)
	}
	if node2.DwellSec != 0 {
		t.Errorf("expected dwellSec 0, got %d", node2.DwellSec)
	}

	// Seed person recognitions
	if err := dbProvider.DB().Exec(`
		INSERT INTO algorithms (id, name, version, task_type, category, target_arch, target_device) 
		VALUES ('algo_face', '人脸识别算法', '1.0.0', 'perception', 'face', 'x86_64', 'cpu')
	`).Error; err != nil {
		t.Fatalf("seed algorithm failed: %v", err)
	}
	if err := dbProvider.DB().Exec(`
		INSERT INTO persons (id, person_id, name) 
		VALUES ('p_007', 'EMP_007', '张三')
	`).Error; err != nil {
		t.Fatalf("seed person failed: %v", err)
	}

	pTime1 := now.Add(-5 * time.Minute)
	pTime2 := pTime1.Add(15 * time.Second) // same camera within dwell
	pTime3 := pTime1.Add(3 * time.Minute)  // cam_c

	if err := dbProvider.DB().Exec(`
		INSERT INTO recognitions (
			id, camera_id, algorithm_id, pts, person_id,
			person_name, matched, similarity, face_image_path, image_path, captured_at
		) VALUES 
		('r1', 'cam_a', 'algo_face', 1000, 'p_007', '张三', 1, 0.85, 'faces/r1.jpg', 'images/r1.jpg', ?),
		('r2', 'cam_a', 'algo_face', 2000, 'p_007', '张三', 1, 0.92, 'faces/r2.jpg', 'images/r2.jpg', ?),
		('r3', 'cam_c', 'algo_face', 3000, 'p_007', '张三', 1, 0.89, 'faces/r3.jpg', 'images/r3.jpg', ?),
		('r4', 'cam_b', 'algo_face', 4000, 'p_007', '张三', 0, 0.99, 'faces/r4.jpg', 'images/r4.jpg', ?)
	`, pTime1, pTime2, pTime3, pTime3).Error; err != nil {
		t.Fatalf("seed recognitions failed: %v", err)
	}

	// Query person trajectory
	personTraj, err := svc.QueryTrajectory(ctx, spatial.TrajectoryQuery{
		MapID:      createdMap.ID,
		TargetType: "person",
		PersonID:   "p_007",
		StartTime:  now.Add(-30 * time.Minute),
		EndTime:    now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("QueryTrajectory for person failed: %v", err)
	}

	// r4 is an unmatched hit on cam_b: it must not become a trajectory node.
	if len(personTraj.Nodes) != 2 {
		t.Fatalf("expected 2 aggregated person nodes (unmatched hit excluded), got %d", len(personTraj.Nodes))
	}
	if personTraj.Nodes[0].CameraID != "cam_a" || personTraj.Nodes[0].DwellSec != 15 {
		t.Errorf("unexpected person node 1: %+v", personTraj.Nodes[0])
	}
	if personTraj.Nodes[0].Details != "张三" || personTraj.Nodes[0].SnapshotPath != "faces/r2.jpg" {
		t.Errorf("unexpected person node 1 details or snapshot: %+v", personTraj.Nodes[0])
	}
	if personTraj.Nodes[1].CameraID != "cam_c" {
		t.Errorf("unexpected person node 2: %+v", personTraj.Nodes[1])
	}
}

// requireApperrCode asserts the domain error carries the expected stable code.
func requireApperrCode(t *testing.T, err error, code string) {
	t.Helper()
	var domainErr *apperr.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *apperr.Error with code %s, got %v", code, err)
	}
	if domainErr.Code != code {
		t.Fatalf("expected code %s, got %s (%v)", code, domainErr.Code, err)
	}
}

func TestService_CreateMapGeneratesULIDAndReadsSVGSize(t *testing.T) {
	ctx := context.Background()
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 900"></svg>`)
	created, err := svc.CreateMap(ctx, "楼栋平面图", bytes.NewReader(svg), "floor.svg")
	if err != nil {
		t.Fatalf("CreateMap(svg) failed: %v", err)
	}
	if !idutil.IsValid(created.ID) {
		t.Fatalf("map primary key must be a ULID, got %q", created.ID)
	}
	// viewBox is the only size information an SVG may carry; inventing 1920x1080
	// here would put every camera marker on the wrong spot.
	if created.Width != 1200 || created.Height != 900 {
		t.Fatalf("unexpected svg intrinsic size: %dx%d", created.Width, created.Height)
	}
}

func TestService_CreateMapRejectsUnreadableImageAndCleansUp(t *testing.T) {
	ctx := context.Background()
	svc, _, mapsDir, cleanup := setupTestServiceWithDir(t)
	defer cleanup()

	_, err := svc.CreateMap(ctx, "坏图", bytes.NewReader([]byte("not an image")), "broken.png")
	requireApperrCode(t, err, "IMAGE_SIZE_UNREADABLE")

	entries, readErr := os.ReadDir(mapsDir)
	if readErr != nil {
		t.Fatalf("read maps dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a rejected upload must not leave files behind, found %d", len(entries))
	}
}

func TestService_SavePositionsSemantics(t *testing.T) {
	ctx := context.Background()
	svc, db, cleanup := setupTestService(t)
	defer cleanup()

	seedCameras := `INSERT INTO cameras (id, name, enabled) VALUES ('cam_a', '东门', 1), ('cam_b', '西门', 1)`
	if err := db.DB().Exec(seedCameras).Error; err != nil {
		t.Fatalf("seed cameras: %v", err)
	}
	m, err := svc.CreateMap(ctx, "园区图", bytes.NewReader(createTestPNG(t, 100, 80)), "a.png")
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	// An absent list is a malformed request, not a request to unbind everything.
	_, err = svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{})
	requireApperrCode(t, err, "POSITIONS_REQUIRED")

	_, err = svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{
			{CameraID: "cam_a", X: 0.1, Y: 0.1},
			{CameraID: "cam_a", X: 0.2, Y: 0.2},
		},
	})
	requireApperrCode(t, err, "DUPLICATE_CAMERA_POSITION")

	_, err = svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{{CameraID: "cam_ghost", X: 0.1, Y: 0.1}},
	})
	requireApperrCode(t, err, "CAMERA_NOT_FOUND")

	count, err := svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{
			{CameraID: "cam_a", X: 0, Y: 1, Angle: 359.5},
		},
	})
	if err != nil {
		t.Fatalf("SavePositions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 stored position, got %d", count)
	}

	// An explicit empty array is the documented way to unbind every camera.
	count, err = svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{Positions: []spatial.CameraPositionItem{}})
	if err != nil {
		t.Fatalf("SavePositions(empty): %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 stored positions, got %d", count)
	}
	positions, err := svc.GetPositions(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetPositions: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected all positions to be unbound, got %d", len(positions))
	}
}

func TestService_QueryTrajectoryRejectsInvertedRange(t *testing.T) {
	ctx := context.Background()
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	m, err := svc.CreateMap(ctx, "园区图", bytes.NewReader(createTestPNG(t, 10, 10)), "a.png")
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	now := time.Now().UTC()
	_, err = svc.QueryTrajectory(ctx, spatial.TrajectoryQuery{
		MapID:      m.ID,
		TargetType: "vehicle",
		Plate:      "粤B88888",
		StartTime:  now,
		EndTime:    now.Add(-time.Hour),
	})
	requireApperrCode(t, err, "INVALID_TIME_RANGE")
}

func TestService_DwellWindowIsStrictlyBelowThreshold(t *testing.T) {
	ctx := context.Background()
	svc, db, cleanup := setupTestService(t)
	defer cleanup()

	if err := db.DB().Exec(`INSERT INTO cameras (id, name, enabled) VALUES ('cam_a', '东门', 1)`).Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	m, err := svc.CreateMap(ctx, "园区图", bytes.NewReader(createTestPNG(t, 10, 10)), "a.png")
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	if _, err := svc.SavePositions(ctx, m.ID, spatial.BatchSavePositionsRequest{
		Positions: []spatial.CameraPositionItem{{CameraID: "cam_a", X: 0.5, Y: 0.5}},
	}); err != nil {
		t.Fatalf("SavePositions: %v", err)
	}

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	seed := func(t1, t2 time.Time) {
		t.Helper()
		if err := db.DB().Exec(`DELETE FROM captures`).Error; err != nil {
			t.Fatalf("clear captures: %v", err)
		}
		if err := db.DB().Exec(`
			INSERT INTO captures (
				id, camera_id, target_type, score, crop_path,
				image_path, attributes_json, pts, captured_at
			) VALUES
			('d1', 'cam_a', 'vehicle', 0.8, 'snapshots/a1.jpg', 'snapshots/img1.jpg', '{"plate_number":"粤B88888"}', 1, ?),
			('d2', 'cam_a', 'vehicle', 0.9, 'snapshots/a2.jpg', 'snapshots/img2.jpg', '{"plate_number":"粤B88888"}', 2, ?)
		`, t1, t2).Error; err != nil {
			t.Fatalf("seed captures: %v", err)
		}
	}
	query := func() *spatial.TrajectoryResponse {
		t.Helper()
		res, err := svc.QueryTrajectory(ctx, spatial.TrajectoryQuery{
			MapID:      m.ID,
			TargetType: "vehicle",
			Plate:      "粤B88888",
			StartTime:  base.Add(-time.Minute),
			EndTime:    base.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("QueryTrajectory: %v", err)
		}
		return res
	}

	// 29s apart: still one dwell node.
	seed(base, base.Add(29*time.Second))
	if nodes := query().Nodes; len(nodes) != 1 {
		t.Fatalf("expected a single dwell node for a 29s gap, got %d", len(nodes))
	}

	// Exactly 30s is the boundary and is NOT merged (PRD §2.3: strictly below Δt).
	seed(base, base.Add(30*time.Second))
	if nodes := query().Nodes; len(nodes) != 2 {
		t.Fatalf("expected two nodes for a 30s gap, got %d", len(nodes))
	}
}
