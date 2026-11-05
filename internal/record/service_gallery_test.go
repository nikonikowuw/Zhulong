package record_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TestServiceGalleryLookupFollowsCooldown 守住「被冷却抑制的帧不回查底库」。
//
// 底库解析曾经发生在冷却判定之前：默认窗口 3s、推理结果按帧到达，等于每一帧
// 都为一次 person_faces JOIN 付费，随后把绝大多数记录丢掉。这里用 GORM Row 回调
// 计数（`Scan` 走 Row 处理器），断言只有真正落库的帧才会触发回查。
func TestServiceGalleryLookupFollowsCooldown(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		VALUES ('face_alice_01', 'person_alice',
		        'faces/person_alice_face_alice_01.jpg', X'00', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert person_face: %v", err)
	}

	// Scan 走 Row 处理器；SQL 要到 After 阶段才构建完成，因此按 SQL 内容计数。
	galleryQueries := 0
	if err := dbStore.DB().Callback().Row().After("gorm:row").
		Register("zhulong:test:count_gallery_queries", func(tx *gorm.DB) {
			if strings.Contains(tx.Statement.SQL.String(), "person_faces") {
				galleryQueries++
			}
		}); err != nil {
		t.Fatalf("register gallery query hook: %v", err)
	}

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)

	captureTime := time.Date(2026, 10, 16, 15, 30, 0, 0, time.UTC)
	frame := func(at time.Time) aitypes.InferenceResult {
		return aitypes.InferenceResult{
			CameraID:    "cam_01",
			AlgorithmID: "algo_01",
			PTS:         123456,
			Timestamp:   at,
			TaskType:    aitypes.TaskTypePersonStructure,
			Persons: []aitypes.PersonItem{
				{
					BodyBox:   [4]float64{0.1, 0.2, 0.4, 0.9},
					BodyScore: 0.95,
					Faces: []aitypes.FaceSubItem{
						{
							Box:           [4]float64{0.15, 0.22, 0.35, 0.45},
							Quality:       0.91,
							Matched:       true,
							PersonID:      "person_alice",
							PersonName:    "Alice",
							MatchedFaceID: "face_alice_01",
							Similarity:    0.96,
						},
					},
					Attributes: map[string]any{"gender": "female"},
				},
			},
		}
	}

	if err := svc.ProcessInferenceResult(ctx, frame(captureTime)); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if galleryQueries != 1 {
		t.Fatalf("gallery queries after first frame = %d, want 1", galleryQueries)
	}

	// 冷却窗口内的后续帧全部被抑制，不得再触底库。
	for i := 1; i <= 5; i++ {
		at := captureTime.Add(time.Duration(i) * 100 * time.Millisecond)
		if err := svc.ProcessInferenceResult(ctx, frame(at)); err != nil {
			t.Fatalf("suppressed frame %d: %v", i, err)
		}
	}
	if galleryQueries != 1 {
		t.Fatalf("gallery queries after suppressed frames = %d, want 1", galleryQueries)
	}

	// 窗口过后重新落库，底库解析恢复。
	if err := svc.ProcessInferenceResult(ctx, frame(captureTime.Add(4*time.Second))); err != nil {
		t.Fatalf("frame after cooldown: %v", err)
	}
	if galleryQueries != 2 {
		t.Fatalf("gallery queries after cooldown = %d, want 2", galleryQueries)
	}

	recs, total, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || total != 2 || len(recs) != 2 {
		t.Fatalf("expected 2 recognitions, got %d, err=%v", total, err)
	}
	for _, rec := range recs {
		if rec.GalleryImagePath != "faces/person_alice_face_alice_01.jpg" {
			t.Errorf("recognition %s: galleryImagePath = %q", rec.ID, rec.GalleryImagePath)
		}
	}
}
