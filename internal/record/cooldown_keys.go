package record

import (
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// 去重键格式见 .trellis/spec/backend/record-guidelines.md §1「目标跟踪与身份去重契约」。
//
// 所有键只在这里构造：spec 里那张表是契约，契约的落点必须是单一文件。散落在业务
// 路径里的 fmt.Sprintf 不会随函数一起演化，格式漂移后只会表现为「同一目标重复上报」，
// 而不会编译失败。
//
// 每个键函数返回「身份键 + 可选的人员/车牌键」时，用 Keys 复数版本；后者负责去重，
// 保证 AllowAll 拿到的是互不相同的键。

func isMatchedRecognition(face aitypes.FaceSubItem) bool {
	return face.Matched || face.PersonID != ""
}

func unmatchedTrackRecognitionCooldownKey(cameraID string, trackID int64) string {
	return fmt.Sprintf("rec:%s:trk:%d:unmatched", cameraID, trackID)
}

// recognitionCooldownKey 返回识别的身份键：宿主跟踪身份优先，其次已登记人员。
//
// 有宿主跟踪身份时，未匹配陌生人使用 `rec:{camera}:trk:{id}:unmatched`，已匹配人员使用
// `rec:{camera}:trk:{id}`：使远距走近/跨帧特征融合场景下首帧先产生陌生人记录的目标，
// 在后续帧确认身份时可突破陌生人冷却完成一次 `unmatched → matched` 升级落库。
func recognitionCooldownKey(cameraID string, trackID int64, face aitypes.FaceSubItem) string {
	if trackID > 0 {
		if isMatchedRecognition(face) {
			return fmt.Sprintf("rec:%s:trk:%d", cameraID, trackID)
		}
		return unmatchedTrackRecognitionCooldownKey(cameraID, trackID)
	}
	if face.PersonID != "" {
		return personRecognitionCooldownKey(cameraID, face.PersonID)
	}
	return fmt.Sprintf("rec:%s:unmatched:%.3f:%.3f", cameraID, face.Box[0], face.Box[1])
}

// recognitionCooldownKeys 返回识别的两级抑制检查键。
//
// 有宿主身份时是 `trk` + `person` 两个键；无宿主身份时 recognitionCooldownKey 已经
// 回落到人员键，此时只返回一个，避免同一个键在 AllowAll 里出现两次。
func recognitionCooldownKeys(cameraID string, trackID int64, face aitypes.FaceSubItem) []string {
	identityKey := recognitionCooldownKey(cameraID, trackID, face)
	if face.PersonID == "" {
		return []string{identityKey}
	}
	personKey := personRecognitionCooldownKey(cameraID, face.PersonID)
	if personKey == identityKey {
		return []string{personKey}
	}
	return []string{identityKey, personKey}
}

// recognitionCooldownPlan 返回识别的检查键与通过后顺带封锁键。
//
// 当有跟踪身份的目标命中已登记人员并通过检查时，顺带封锁同轨迹的 `:unmatched` 键，
// 防止已确认身份后因侧脸退化在同一冷却期内倒退补发陌生人记录。
func recognitionCooldownPlan(cameraID string, trackID int64, face aitypes.FaceSubItem) ([]string, []string) {
	checkKeys := recognitionCooldownKeys(cameraID, trackID, face)
	if trackID > 0 && isMatchedRecognition(face) {
		return checkKeys, []string{unmatchedTrackRecognitionCooldownKey(cameraID, trackID)}
	}
	return checkKeys, nil
}

// personRecognitionCooldownKey 是已登记人员的识别抑制键。
func personRecognitionCooldownKey(cameraID, personID string) string {
	return fmt.Sprintf("rec:%s:person:%s", cameraID, personID)
}

func faceCaptureCooldownKey(cameraID string, trackID int64, box [4]float64) string {
	if trackID > 0 {
		return fmt.Sprintf("cap:%s:face:trk:%d", cameraID, trackID)
	}
	return fmt.Sprintf("cap:%s:face:box:%.3f:%.3f", cameraID, box[0], box[1])
}

func personCaptureCooldownKey(cameraID string, trackID int64, box [4]float64) string {
	if trackID > 0 {
		return fmt.Sprintf("cap:%s:person:trk:%d", cameraID, trackID)
	}
	return fmt.Sprintf("cap:%s:person:box:%.3f:%.3f", cameraID, box[0], box[1])
}

func vehicleCaptureCooldownKey(cameraID string, trackID int64, firstPlateNo string, box [4]float64) string {
	if trackID > 0 {
		return fmt.Sprintf("cap:%s:vehicle:trk:%d", cameraID, trackID)
	}
	if firstPlateNo != "" {
		return vehiclePlateCooldownKey(cameraID, firstPlateNo)
	}
	return fmt.Sprintf("cap:%s:vehicle:box:%.3f:%.3f", cameraID, box[0], box[1])
}

// vehicleCaptureCooldownKeys 返回车辆抓拍的两级抑制键（身份 + 车牌）。
func vehicleCaptureCooldownKeys(cameraID string, trackID int64, firstPlateNo string, box [4]float64) []string {
	identityKey := vehicleCaptureCooldownKey(cameraID, trackID, firstPlateNo, box)
	if firstPlateNo == "" {
		return []string{identityKey}
	}
	plateKey := vehiclePlateCooldownKey(cameraID, firstPlateNo)
	if plateKey == identityKey {
		return []string{plateKey}
	}
	return []string{identityKey, plateKey}
}

func vehiclePlateCooldownKey(cameraID, plateNo string) string {
	return fmt.Sprintf("cap:%s:vehicle:plate:%s", cameraID, plateNo)
}

// detectionCaptureCooldownKey 是通用检测抓拍的键：有宿主身份按轨迹，无身份回落到
// 「相机 + 标签」粗键（spec §1：无身份回落保持 cap:{camera}:{label}）。
func detectionCaptureCooldownKey(cameraID, label string, trackID int64) string {
	if trackID > 0 {
		return fmt.Sprintf("cap:%s:%s:trk:%d", cameraID, label, trackID)
	}
	return fmt.Sprintf("cap:%s:%s", cameraID, label)
}

// alarmCooldownKey 是告警的粗粒度动作键：不做目标级去重。
func alarmCooldownKey(cameraID, label string) string {
	return fmt.Sprintf("alarm:%s:%s", cameraID, label)
}
