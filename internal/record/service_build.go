package record

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
)

// 本文件是「记录行 + 抓拍任务」的纯构造函数集合：无 receiver、不碰 Service 状态、
// 不发事件。编排（冷却、入库、分发）留在 service.go，两者分开改。

// optionalAlgorithmID 把推理结果里的算法标识转换成可空列值。
//
// ⚠️ 空串必须转成 nil：recognitions.algorithm_id / alarms.algorithm_id 上是
// REFERENCES algorithms(id)，SQLite 对空串同样要求父表存在匹配行，只有 NULL 才
// 跳过外键校验。直接写空串会让整条记录 insert 失败，整批抓拍被跳过 —— 表现就是
// 「记录莫名其妙消失」。
//
// 空串的来源是历史设计：AI 流按相机共享，Hub 无法归属算法，若 runner 未在载荷里
// 打戳就只剩空串。
func optionalAlgorithmID(id string) *string {
	// 与 optionalString 同一实现，保留独立名字是为了让「算法外键」的约束在调用点
	// 一眼可见；合并成一个泛型函数会把这段注释挤成可有可无的说明。
	return optionalString(id)
}

// optionalString 把空串转换成可空列值 nil。
func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// buildRecognitionRecordAndJob 组装一条识别记录与它的证据图任务。
func buildRecognitionRecordAndJob(
	f ingestFrame,
	face aitypes.FaceStructureItem,
	bodyBox [4]float64,
	attrs map[string]any,
	trackID int64,
	ref GalleryFaceRef,
) (*Recognition, snapshotJob) {
	recID := idutil.New()

	// 身份归属以本地底库为准：matched_face_id 落空（并发删除）时降级为
	// 「未归属」记录，而不是把 runner 载荷里的业务键写进外键列。
	personName := ref.PersonName
	if personName == "" {
		personName = face.PersonName
	}

	// 两条证据图：全幅（imagePath）+ 人脸裁剪（faceImagePath）。
	// 两者在一次抓拍里产出，避免为同一帧编码两次。
	fullPath := f.relPath(recID, ".jpg")
	facePath := f.relPath(recID, "_face.jpg")

	rec := &Recognition{
		ID:               recID,
		CameraID:         f.res.CameraID,
		AlgorithmID:      optionalAlgorithmID(f.res.AlgorithmID),
		AlgorithmName:    f.algo.Name,
		AlgorithmVersion: f.algo.Version,
		PTS:              f.res.PTS,
		TrackID:          trackID,
		PersonID:         optionalString(ref.PersonRowID),
		PersonExternalID: ref.PersonExternalID,
		PersonName:       personName,
		MatchedFaceID:    face.MatchedFaceID,
		Matched:          face.Matched,
		Similarity:       face.Similarity,
		CandidatesJSON:   marshalSliceOrDefault(face.Candidates),
		FaceImagePath:    facePath,
		GalleryImagePath: ref.ImagePath,
		ImagePath:        fullPath,
		BoxJSON:          marshalBox(face.Box),
		BodyBoxJSON:      marshalBox(bodyBox),
		LandmarksJSON:    marshalSliceOrDefault(face.Landmarks),
		AttributesJSON:   marshalMapOrDefault(attrs),
		FeatureVector:    face.FeatureVector,
		CapturedAt:       f.capturedAt,
		CreatedAt:        time.Now().UTC(),
	}

	job := snapshotJob{
		cameraID:      f.res.CameraID,
		pts:           f.res.PTS,
		captureTimeMs: f.res.CaptureTimeMs,
		targetTable:   tableRecognitions,
		targetID:      recID,
		boxes: []engine.SnapshotBox{
			engine.SnapshotBox(face.Box),
		},
		outputs: []snapshotOutput{
			{boxIndex: -1, relPath: fullPath},
			{boxIndex: 0, relPath: facePath},
		},
	}

	return rec, job
}

// captureSpec 是一次目标抓拍的输入。
//
// 人体 / 孤立人脸 / 通用检测 / 车辆四类抓拍只有 targetType、score、box 与
// attributes 不同，行结构与证据图任务完全一致，因此共用同一个 builder。
type captureSpec struct {
	targetType string
	trackID    int64
	score      float64
	box        [4]float64
	landmarks  [][2]float64
	attrsJSON  string
}

// buildCaptureRecordAndJob 组装一条目标抓拍与其证据图任务（全幅 + 裁剪）。
func buildCaptureRecordAndJob(f ingestFrame, spec captureSpec) (*Capture, snapshotJob) {
	captureID := idutil.New()
	fullPath := f.relPath(captureID, ".jpg")
	cropPath := f.relPath(captureID, "_crop.jpg")

	capture := &Capture{
		ID:               captureID,
		CameraID:         f.res.CameraID,
		AlgorithmID:      optionalAlgorithmID(f.res.AlgorithmID),
		AlgorithmName:    f.algo.Name,
		AlgorithmVersion: f.algo.Version,
		TargetType:       spec.targetType,
		TrackID:          spec.trackID,
		Score:            spec.score,
		BoxJSON:          marshalBox(spec.box),
		LandmarksJSON:    marshalSliceOrDefault(spec.landmarks),
		CropPath:         cropPath,
		ImagePath:        fullPath,
		AttributesJSON:   spec.attrsJSON,
		PTS:              f.res.PTS,
		CapturedAt:       f.capturedAt,
		CreatedAt:        time.Now().UTC(),
	}

	job := snapshotJob{
		cameraID:      f.res.CameraID,
		pts:           f.res.PTS,
		captureTimeMs: f.res.CaptureTimeMs,
		targetTable:   tableCaptures,
		targetID:      captureID,
		boxes: []engine.SnapshotBox{
			engine.SnapshotBox(spec.box),
		},
		outputs: []snapshotOutput{
			{boxIndex: -1, relPath: fullPath},
			{boxIndex: 0, relPath: cropPath},
		},
	}

	return capture, job
}

// alarmSpec 是一次告警的输入。
//
// boxJSON / attrsJSON 由调用方决定（检测结果有框与属性，分类结果两者皆空），
// cropBox 为 nil 表示场景级告警：没有目标裁剪框，只出全幅图。
type alarmSpec struct {
	label     string
	score     float64
	boxJSON   string
	attrsJSON string
	cropBox   *[4]float64
}

// buildAlarmRecordAndJob 组装一条告警行与它的抓拍任务。
func buildAlarmRecordAndJob(f ingestFrame, spec alarmSpec) (*Alarm, snapshotJob) {
	alarmID := idutil.New()
	fullPath := f.relPath(alarmID, ".jpg")

	cropPath := ""
	if spec.cropBox != nil {
		cropPath = f.relPath(alarmID, "_crop.jpg")
	}

	alarm := &Alarm{
		ID:               alarmID,
		CameraID:         f.res.CameraID,
		AlgorithmID:      optionalAlgorithmID(f.res.AlgorithmID),
		AlgorithmName:    f.algo.Name,
		AlgorithmVersion: f.algo.Version,
		AlarmType:        spec.label,
		Score:            spec.score,
		BoxJSON:          spec.boxJSON,
		ImagePath:        fullPath,
		CropPath:         cropPath,
		ExtraJSON:        spec.attrsJSON,
		PTS:              f.res.PTS,
		CapturedAt:       f.capturedAt,
		CreatedAt:        time.Now().UTC(),
	}

	// 告警图是高价值证据：写图前先落 .lock，防止并发清理器在写完到加锁之间把它收走。
	outputs := []snapshotOutput{{boxIndex: -1, relPath: fullPath, protected: true}}
	var boxes []engine.SnapshotBox
	if spec.cropBox != nil {
		boxes = append(boxes, engine.SnapshotBox(*spec.cropBox))
		outputs = append(outputs, snapshotOutput{boxIndex: 0, relPath: cropPath, protected: true})
	}

	job := snapshotJob{
		cameraID:      f.res.CameraID,
		pts:           f.res.PTS,
		captureTimeMs: f.res.CaptureTimeMs,
		targetTable:   tableAlarms,
		targetID:      alarmID,
		boxes:         boxes,
		outputs:       outputs,
	}

	return alarm, job
}

func firstPlateNumber(v aitypes.VehicleItem) string {
	if len(v.Plates) == 0 {
		return ""
	}
	return v.Plates[0].PlateNumber
}

// vehicleAttributesJSON 把车辆结构化字段拍平进 attributes 列。
//
// 属性列是 open-ended 的、按算法扩展；target_type 只有 person/face/vehicle 三态，
// 所以车型、车身颜色与车牌信息走 attributes 而不是各自建列。
func vehicleAttributesJSON(v aitypes.VehicleItem, plateNo string) string {
	attrs := make(map[string]any, len(v.Attributes)+5)
	for k, val := range v.Attributes {
		attrs[k] = val
	}
	if v.VehicleType != "" {
		attrs["vehicle_type"] = v.VehicleType
	}
	if v.VehicleColor != "" {
		attrs["vehicle_color"] = v.VehicleColor
	}
	if len(v.Plates) > 0 {
		attrs["plates"] = v.Plates
		if plateNo != "" {
			attrs["plate_number"] = plateNo
		}
		if v.Plates[0].PlateColor != "" {
			attrs["plate_color"] = v.Plates[0].PlateColor
		}
		attrs["plate_type"] = v.Plates[0].PlateType
	}

	return marshalMapOrDefault(attrs)
}

// marshalBox 序列化归一化检测框。
//
// json.Marshal 对定长 float64 数组不会失败；错误分支返回 "[]" 是为了与列默认值
// 保持一致（历史行同样是 "[]"），读取侧 unmarshalBox 会把两者都降级成零框。
func marshalBox(box [4]float64) string {
	b, err := json.Marshal(box)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func marshalSliceOrDefault[T any](items []T) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// faceAttributeKeySuffix 是两条属性通道撞名时给人脸键加的后缀。
//
// 标签→后缀的改名约定属于**记录载荷的对外契约**（写进 `records.attributes_json`，
// 前端与导出一律按它展示），所以必须是有名字的常量而不是散落的字面量：
// 改这里等于改对外数据格式，需要同步 design.md 与前端。
const faceAttributeKeySuffix = "@face"

// mergeAttributeChannels 把人体的属性与人脸自身的属性合成一份记录载荷。
//
// 两条通道**来源不同**：人体属性来自 PPLCNet 人体属性模型（192×256 人体框，
// AgeLess18/Age18-60/AgeOver60 三档年龄 + 服饰 + 姿态），人脸属性来自 InsightFace
// genderage（96×96 对齐人脸，性别 + 五档年龄）。两者都可能在 "Female" 这类同名标签上
// 各自给出分数，所以不能覆盖、也不能取最大值：
//
// 同名键两条通道**都保留**，人脸通道改名为 `<标签>@face`。理由：
//   - 覆盖会静默丢掉一整条通道的证据，而且丢哪边取决于 map 遍历顺序（Go 的 map 无序）；
//   - 取最大值看似合理，实际把「人体模型说 Female=0.54」和「人脸模型说 Male=0.98」
//     混成一个数，两种矛盾都能被它掩盖。
//
// 后缀而不是前缀：现有报表与前端按「标签名是否等于某个已知属性名」匹配，后缀在
// 视觉上仍归到同名标签附近，同时保证键集合是纯函数、不受遍历顺序影响。
//
// 撞名不是只可能发生一次：人脸通道自身就可能已经带 `<标签>@face`（上游合并过一轮），
// 所以后缀要**循环追加**直到不撞（`Female@face@face`），只探一次会在两条键互相踩到时
// 静默丢掉一条证据。人脸键按字典序处理，撞名链与 Go 的 map 迭代顺序无关 —— 否则
// `{Female, Female@face}` 这种输入每次运行落在哪个键上都不一定。
//
// 只有恰好一条通道有值时直接返回它（不复制、不改名），让最常见的情况保持原样。
func mergeAttributeChannels(personAttrs, faceAttrs map[string]any) map[string]any {
	if len(faceAttrs) == 0 {
		return personAttrs
	}
	if len(personAttrs) == 0 {
		return faceAttrs
	}
	merged := make(map[string]any, len(personAttrs)+len(faceAttrs))
	for k, v := range personAttrs {
		merged[k] = v
	}
	faceKeys := make([]string, 0, len(faceAttrs))
	for k := range faceAttrs {
		faceKeys = append(faceKeys, k)
	}
	slices.Sort(faceKeys)
	for _, k := range faceKeys {
		key := k
		for {
			if _, clash := merged[key]; !clash {
				break
			}
			key += faceAttributeKeySuffix
		}
		merged[key] = faceAttrs[k]
	}
	return merged
}

func marshalMapOrDefault(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
