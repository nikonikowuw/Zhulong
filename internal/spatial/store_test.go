package spatial_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/spatial"
	"go.uber.org/zap"
)

func setupTestStore(t *testing.T) (*spatial.Store, database.DBProvider, func()) {
	t.Helper()
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := spatial.NewStore(dbStore)
	cleanup := func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}
	return store, dbStore, cleanup
}

func TestStore_MapsCRUD(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	// 1. Create
	m := &spatial.CanvasMap{
		ID:        "map_1",
		Name:      "园区主图",
		ImagePath: "map_1.png",
		Width:     1920,
		Height:    1080,
	}
	if err := store.CreateMap(ctx, m); err != nil {
		t.Fatalf("CreateMap failed: %v", err)
	}

	// 2. Get
	fetched, err := store.GetMap(ctx, "map_1")
	if err != nil {
		t.Fatalf("GetMap failed: %v", err)
	}
	if fetched.Name != "园区主图" || fetched.Width != 1920 {
		t.Errorf("unexpected map data: %+v", fetched)
	}

	// 3. Update
	if err := store.UpdateMapName(ctx, "map_1", "新园区图"); err != nil {
		t.Fatalf("UpdateMapName failed: %v", err)
	}
	updated, err := store.GetMap(ctx, "map_1")
	if err != nil {
		t.Fatalf("GetMap after update failed: %v", err)
	}
	if updated.Name != "新园区图" {
		t.Errorf("expected updated name, got %s", updated.Name)
	}

	// 4. List
	list, err := store.ListMaps(ctx)
	if err != nil {
		t.Fatalf("ListMaps failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 map, got %d", len(list))
	}

	// 5. Delete
	if err := store.DeleteMap(ctx, "map_1"); err != nil {
		t.Fatalf("DeleteMap failed: %v", err)
	}
	_, err = store.GetMap(ctx, "map_1")
	if err == nil {
		t.Error("expected error for deleted map, got nil")
	}
}

func TestStore_Positions(t *testing.T) {
	ctx := context.Background()
	store, dbProvider, cleanup := setupTestStore(t)
	defer cleanup()

	// Seed cameras for FK constraint
	seedCameras := `INSERT INTO cameras (id, name, enabled) VALUES
		('cam_1', '相机1', 1),
		('cam_2', '相机2', 1),
		('cam_3', '相机3', 1)`
	if err := dbProvider.DB().Exec(seedCameras).Error; err != nil {
		t.Fatalf("seed cameras failed: %v", err)
	}

	m := &spatial.CanvasMap{
		ID:        "map_pos",
		Name:      "位置测试图",
		ImagePath: "map_pos.png",
		Width:     800,
		Height:    600,
	}
	if err := store.CreateMap(ctx, m); err != nil {
		t.Fatalf("CreateMap failed: %v", err)
	}

	// Insert positions
	positions := []spatial.CameraCanvasPosition{
		{CameraID: "cam_1", MapID: "map_pos", X: 0.1, Y: 0.2, Angle: 45},
		{CameraID: "cam_2", MapID: "map_pos", X: 0.8, Y: 0.9, Angle: 180},
	}
	if err := store.BatchSavePositions(ctx, "map_pos", positions); err != nil {
		t.Fatalf("BatchSavePositions failed: %v", err)
	}

	fetched, err := store.GetPositionsByMap(ctx, "map_pos")
	if err != nil {
		t.Fatalf("GetPositionsByMap failed: %v", err)
	}
	if len(fetched) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(fetched))
	}

	// Replace with 1 position
	positions2 := []spatial.CameraCanvasPosition{
		{CameraID: "cam_3", MapID: "map_pos", X: 0.5, Y: 0.5, Angle: 0},
	}
	if err := store.BatchSavePositions(ctx, "map_pos", positions2); err != nil {
		t.Fatalf("BatchSavePositions replace failed: %v", err)
	}
	fetched2, err := store.GetPositionsByMap(ctx, "map_pos")
	if err != nil {
		t.Fatalf("GetPositionsByMap failed: %v", err)
	}
	if len(fetched2) != 1 || fetched2[0].CameraID != "cam_3" {
		t.Fatalf("expected 1 position cam_3, got %+v", fetched2)
	}
}

func TestStore_QueryDetections(t *testing.T) {
	ctx := context.Background()
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	now := time.Now().UTC()

	// Empty cameraIDs short-circuits to an empty result instead of querying.
	res, err := store.QueryVehicleCaptures(ctx, spatial.TrajectoryFilter{
		MatchKey: "粤B12345",
		Start:    now.Add(-time.Hour),
		End:      now,
	})
	if err != nil || len(res) != 0 {
		t.Errorf("expected empty result for empty cameras, got %v, %v", res, err)
	}

	resPerson, err := store.QueryPersonRecognitions(ctx, spatial.TrajectoryFilter{
		MatchKey: "person_1",
		Start:    now.Add(-time.Hour),
		End:      now,
	})
	if err != nil || len(resPerson) != 0 {
		t.Errorf("expected empty result for empty cameras, got %v, %v", resPerson, err)
	}
}

// TestStore_VehiclePlateSearchEscapesWildcards 守护车牌子串检索的 LIKE 转义：
// 未转义时 "%" 会命中全部车辆抓拍，"_" 会变成单字符通配符。
func TestStore_VehiclePlateSearchEscapesWildcards(t *testing.T) {
	ctx := context.Background()
	store, dbProvider, cleanup := setupTestStore(t)
	defer cleanup()

	if err := dbProvider.DB().Exec(
		`INSERT INTO cameras (id, name, enabled) VALUES ('cam_a', '东门', 1)`,
	).Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if err := dbProvider.DB().Exec(`
		INSERT INTO captures (
			id, camera_id, target_type, score, crop_path,
			image_path, attributes_json, pts, captured_at
		) VALUES
		('k1', 'cam_a', 'vehicle', 0.9, 'snapshots/a.jpg', 'snapshots/b.jpg', '{"plate_number":"粤B88888"}', 1, ?),
		('k2', 'cam_a', 'vehicle', 0.9, 'snapshots/c.jpg', 'snapshots/d.jpg', '{"plate_number":"粤B12345"}', 2, ?)
	`, now, now.Add(time.Second)).Error; err != nil {
		t.Fatalf("seed captures: %v", err)
	}

	start, end := now.Add(-time.Hour), now.Add(time.Hour)

	all, err := store.QueryVehicleCaptures(ctx, spatial.TrajectoryFilter{
		CameraIDs: []string{"cam_a"},
		MatchKey:  "%",
		Start:     start,
		End:       end,
	})
	if err != nil {
		t.Fatalf("query with %%: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("a bare %% must not match every plate, got %d rows", len(all))
	}

	prefix, err := store.QueryVehicleCaptures(ctx, spatial.TrajectoryFilter{
		CameraIDs: []string{"cam_a"},
		MatchKey:  "粤B8",
		Start:     start,
		End:       end,
	})
	if err != nil {
		t.Fatalf("query prefix: %v", err)
	}
	if len(prefix) != 1 || prefix[0].Details != "粤B88888" {
		t.Fatalf("expected only 粤B88888, got %+v", prefix)
	}
}
