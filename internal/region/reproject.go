package region

import (
	"math"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// Reproject 把窗口局部归一化结果线性映射回全幅归一化坐标。
//
//	g = E.origin + c * E.span，再 clamp 到 [0,1]
//
// view 必须是引擎回报的**生效 E**（窗口像素与它一一对应）。identity 视图逐字段原样
// 返回，不做乘 1，避免浮点噪声污染“未配置裁剪”的既有路径。
//
// ⚠️ 非有限值策略：box 任一分量 NaN/Inf → 丢弃该 detection/person/vehicle/plate/face；
// landmark 任一分量非有限 → 丢弃该 landmark。绝不把 NaN clamp 成 0——那会静默造出
// 一个左上角目标。
//
// ⚠️ 顶层 face 的 PersonIndex：`-1`（孤立人脸）与 `>= 0` 且父人体保留 → 保留并重映射；
// 父人体被丢弃、或索引越界（含 Persons 为空时的 `>= 0`）→ **整条丢弃**。这不是
// 本次新定的规则：region.Filter 对同一不变量的处置完全相同（“子目标跟随宿主目标”），
// 两处判据必须一致，否则同一份结果在「配了 crop」与「配了 ROI」下会留下不同的人脸。
// person 被丢弃时其余顶层 face 的重映射同样随 remap 表收缩。
func Reproject(res aitypes.InferenceResult, view Rect) aitypes.InferenceResult {
	if view.IsIdentity() {
		return res
	}

	ox, sx := view[0], view[2]-view[0]
	oy, sy := view[1], view[3]-view[1]

	if len(res.Detections) > 0 {
		detections := make([]aitypes.DetectionItem, 0, len(res.Detections))
		for _, det := range res.Detections {
			box, ok := reprojectBox(det.Box, ox, sx, oy, sy)
			if !ok {
				continue
			}
			det.Box = box
			detections = append(detections, det)
		}
		res.Detections = detections
	}

	// Persons 与顶层 Faces 共用一张 oldIdx -> newIdx 重映射表（淘汰者为 -1）。
	if len(res.Persons) > 0 || len(res.Faces) > 0 {
		remap := make([]int32, len(res.Persons))
		persons := make([]aitypes.PersonItem, 0, len(res.Persons))
		for oldIdx, person := range res.Persons {
			box, ok := reprojectBox(person.BodyBox, ox, sx, oy, sy)
			if !ok {
				remap[oldIdx] = -1
				continue
			}
			person.BodyBox = box
			// 兼容路径（default/unknown task_type）会保留嵌套 faces，同样要映射。
			person.Faces = reprojectFaceSubs(person.Faces, ox, sx, oy, sy)
			remap[oldIdx] = int32(len(persons))
			persons = append(persons, person)
		}
		res.Persons = persons

		if len(res.Faces) > 0 {
			faces := make([]aitypes.FaceStructureItem, 0, len(res.Faces))
			for _, face := range res.Faces {
				box, ok := reprojectBox(face.Box, ox, sx, oy, sy)
				if !ok {
					continue
				}
				if face.PersonIndex >= 0 {
					// 越界非法索引与“宿主 person 被丢弃”都按 fail-closed 处理（与 Filter 同判据）。
					if int(face.PersonIndex) >= len(remap) {
						continue
					}
					newIdx := remap[face.PersonIndex]
					if newIdx < 0 {
						continue
					}
					face.PersonIndex = newIdx
				}
				face.Box = box
				face.Landmarks = reprojectLandmarks(face.Landmarks, ox, sx, oy, sy)
				faces = append(faces, face)
			}
			res.Faces = faces
		}
	}

	if len(res.Vehicles) > 0 {
		vehicles := make([]aitypes.VehicleItem, 0, len(res.Vehicles))
		for _, vehicle := range res.Vehicles {
			box, ok := reprojectBox(vehicle.VehicleBox, ox, sx, oy, sy)
			if !ok {
				continue
			}
			vehicle.VehicleBox = box
			if len(vehicle.Plates) > 0 {
				plates := make([]aitypes.PlateSubItem, 0, len(vehicle.Plates))
				for _, plate := range vehicle.Plates {
					plateBox, ok := reprojectBox(plate.Box, ox, sx, oy, sy)
					if !ok {
						continue
					}
					plate.Box = plateBox
					plates = append(plates, plate)
				}
				vehicle.Plates = plates
			}
			vehicles = append(vehicles, vehicle)
		}
		res.Vehicles = vehicles
	}

	return res
}

// reprojectFaceSubs 映射挂在 person 上的兼容路径人脸（旧 person_structure 形状）。
func reprojectFaceSubs(
	faces []aitypes.FaceSubItem,
	ox, sx, oy, sy float64,
) []aitypes.FaceSubItem {
	if len(faces) == 0 {
		return faces
	}
	out := make([]aitypes.FaceSubItem, 0, len(faces))
	for _, face := range faces {
		box, ok := reprojectBox(face.Box, ox, sx, oy, sy)
		if !ok {
			continue
		}
		face.Box = box
		face.Landmarks = reprojectLandmarks(face.Landmarks, ox, sx, oy, sy)
		out = append(out, face)
	}
	return out
}

func reprojectLandmarks(points [][2]float64, ox, sx, oy, sy float64) [][2]float64 {
	if len(points) == 0 {
		return points
	}
	out := make([][2]float64, 0, len(points))
	for _, point := range points {
		x, okX := reprojectValue(point[0], ox, sx)
		y, okY := reprojectValue(point[1], oy, sy)
		if !okX || !okY {
			continue
		}
		out = append(out, [2]float64{x, y})
	}
	return out
}

func reprojectBox(box [4]float64, ox, sx, oy, sy float64) ([4]float64, bool) {
	x1, okX1 := reprojectValue(box[0], ox, sx)
	y1, okY1 := reprojectValue(box[1], oy, sy)
	x2, okX2 := reprojectValue(box[2], ox, sx)
	y2, okY2 := reprojectValue(box[3], oy, sy)
	if !okX1 || !okY1 || !okX2 || !okY2 {
		return box, false
	}
	return [4]float64{x1, y1, x2, y2}, true
}

func reprojectValue(component, origin, span float64) (float64, bool) {
	if math.IsNaN(component) || math.IsInf(component, 0) {
		return 0, false
	}
	return clamp01(origin + component*span), true
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
