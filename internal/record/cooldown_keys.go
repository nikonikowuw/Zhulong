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

// recognitionCooldownKey 返回识别的身份键：宿主跟踪身份优先，其次已登记人员。
func recognitionCooldownKey(cameraID string, trackID int64, face aitypes.FaceSubItem) string {
	if trackID > 0 {
		return fmt.Sprintf("rec:%s:trk:%d", cameraID, trackID)
	}
	if face.PersonID != "" {
		return personRecognitionCooldownKey(cameraID, face.PersonID)
	}
	return fmt.Sprintf("rec:%s:unmatched:%.3f:%.3f", cameraID, face.Box[0], face.Box[1])
}

// recognitionCooldownKeys 返回识别的两级抑制键。
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
