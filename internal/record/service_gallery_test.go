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

// TestEnrichRecognitionCandidatesResolvesPersonRowID 守住 Top-N 候选人回查语义：
// 推理载荷里的 candidate.person_id 是外部业务键（可为空），而底库取图接口
// /persons/{id}/faces/{face_id}/image 要求内部行 ID（persons.id）。查询路径必须按
// face_id 批量反查并填充 personRowId，同时对已删库的人脸保持空串不报错。
func TestEnrichRecognitionCandidatesResolvesPersonRowID(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := dbStore.DB().Exec(`INSERT INTO persons (id, person_id, name, created_at, updated_at)
		VALUES ('person_bob', 'EMP-BOB', 'Bob', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert bob: %v", err)
	}
	if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		VALUES
			('face_alice_01', 'person_alice', 'alice.jpg', X'00', CURRENT_TIMESTAMP),
			('face_bob_01', 'person_bob', 'bob.jpg', X'00', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert person_faces: %v", err)
	}

	rec := &record.Recognition{
		ID:             "rec_cand_enrich_1",
		CameraID:       "cam_01",
		AlgorithmID:    strPtr("algo_01"),
		PTS:            100,
		TrackID:        1,
		Matched:        false,
		CandidatesJSON: `[{"person_id":"","person_name":"","face_id":"face_alice_01","similarity":0.74},{"person_id":"EMP-BOB","person_name":"Bob","face_id":"face_bob_01","similarity":0.68},{"person_id":"EMP-GONE","person_name":"Ghost","face_id":"face_deleted","similarity":0.61}]`,
		CapturedAt:     time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC),
	}
	if err := store.CreateRecognition(ctx, rec); err != nil {
		t.Fatalf("CreateRecognition: %v", err)
	}

	svc := record.NewService(store, zap.NewNop())

	assertCandidates := func(t *testing.T, got []record.RecognitionCandidateInfo) {
		t.Helper()
		if len(got) != 3 {
			t.Fatalf("len(candidates) = %d, want 3", len(got))
		}
		if got[0].PersonRowID != "person_alice" || got[0].PersonID != "" || got[0].PersonName != "Alice" {
			t.Errorf("candidate[0] = %+v, want personRowId=person_alice, person_id='', personName=Alice", got[0])
		}
		if got[1].PersonRowID != "person_bob" || got[1].PersonID != "EMP-BOB" || got[1].PersonName != "Bob" {
			t.Errorf("candidate[1] = %+v, want personRowId=person_bob, person_id=EMP-BOB, personName=Bob", got[1])
		}
		if got[2].PersonRowID != "" || got[2].PersonID != "EMP-GONE" || got[2].PersonName != "Ghost" {
			t.Errorf("candidate[2] = %+v, want empty personRowId for deleted face", got[2])
		}
	}

	list, total, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListRecognitions failed: err=%v, total=%d, len=%d", err, total, len(list))
	}
	assertCandidates(t, list[0].Candidates)

	single, err := svc.GetRecognition(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetRecognition failed: %v", err)
	}
	assertCandidates(t, single.Candidates)
}
