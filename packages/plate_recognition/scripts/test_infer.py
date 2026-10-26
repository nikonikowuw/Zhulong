#!/usr/bin/env python3
"""
test_infer.py - 车牌检测与识别离线推理验证脚本（C++ 插件的参考实现）

本脚本与 packages/plate_recognition/src/ 下的 C++ 实现保持**逐式对齐**，用于在没有
原生构建时交叉验证预处理与解码逻辑。三处曾经踩过的坑，改动前请先读：

1. 通道顺序不一致：检测模型吃 RGB（letterbox 后需 BGR→RGB 翻转），而识别模型
   （myNet_ocr_color）训练侧吃的是 cv2 的 **BGR**，不做翻转。喂错会让颜色头系统性
   误判（蓝牌读成黑牌），车身字符也会漂。
2. 输出布局不能硬编码：YOLOv8 导出可能是 [1, C, N]，也可能是 [1, N, C]；
   anchors 数随输入尺寸变化（640 → 8400），类别数由 C - 4 决定。
3. 采样坐标统一为像素中心语义：单层车牌整块缩放；双层车牌按「上 5/12 + 下 2/3
   各取一半宽度横向拼接」。

用法:
    python3 packages/plate_recognition/scripts/test_infer.py <image> \
        [--det-model ...] [--rec-model ...] [--conf 0.35] [--nms 0.45]
"""

import argparse
import sys

import cv2
import numpy as np

try:
    import onnxruntime as ort
except ImportError:
    sys.exit("请先安装 onnxruntime: pip install onnxruntime")

PLATE_NAMES = [
    "#", "京", "沪", "津", "渝", "冀", "晋", "蒙", "辽", "吉", "黑",
    "苏", "浙", "皖", "闽", "赣", "鲁", "豫", "鄂", "湘", "粤",
    "桂", "琼", "川", "贵", "云", "藏", "陕", "甘", "青", "宁",
    "新", "学", "警", "港", "澳", "挂", "使", "领", "民", "航", "危",
    "0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
    "A", "B", "C", "D", "E", "F", "G", "H", "J", "K",
    "L", "M", "N", "P", "Q", "R", "S", "T", "U", "V",
    "W", "X", "Y", "Z", "险", "品"
]
COLOR_NAMES = ["黑色", "蓝色", "绿色", "白色", "黄色"]

DET_INPUT_SIZE = 640
REC_WIDTH, REC_HEIGHT = 168, 48
REC_MEAN, REC_STD = 0.588, 0.193
DOUBLE_LINE = 1


def resolve_output_layout(shape):
    """从输出形状推断 (channels, num_anchors, channel_first)；不合法返回 None。"""
    dims = [d for d in shape if d != 1]
    if len(dims) != 2 or dims[0] <= 0 or dims[1] <= 0:
        return None
    first, second = dims
    max_channels = 256
    if first <= max_channels < second:
        return first, second, True
    if second <= max_channels < first:
        return second, first, False
    return None


def letterbox_detect(image, conf_threshold):
    """检测前处理：等比缩放 + 居中补边 + BGR→RGB → NCHW float32。"""
    height, width = image.shape[:2]
    scale = min(DET_INPUT_SIZE / height, DET_INPUT_SIZE / width)
    resized_w, resized_h = int(width * scale), int(height * scale)
    pad_left, pad_top = (DET_INPUT_SIZE - resized_w) // 2, (DET_INPUT_SIZE - resized_h) // 2

    canvas = np.full((DET_INPUT_SIZE, DET_INPUT_SIZE, 3), 114, dtype=np.uint8)
    canvas[pad_top:pad_top + resized_h, pad_left:pad_left + resized_w] = cv2.resize(
        image, (resized_w, resized_h)
    )
    # 检测模型吃 RGB：BGR→RGB 后转 NCHW 并归一化
    blob = canvas[:, :, ::-1].transpose(2, 0, 1).astype(np.float32) / 255.0
    return np.expand_dims(blob, axis=0), scale, pad_left, pad_top


def decode_detections(det_output, scale, pad_left, pad_top, image_shape, conf_threshold):
    """解析 YOLOv8 检测头输出，返回归一化框列表（与 C++ PlateDetector 一致）。"""
    tensor = det_output[0]
    layout = resolve_output_layout(tensor.shape)
    if layout is None:
        raise SystemExit(f"无法识别的检测输出形状: {tensor.shape}")
    channels, num_anchors, channel_first = layout
    if channels < 5:
        raise SystemExit(f"检测输出通道数不足: {channels}")

    if channel_first:
        values = tensor.reshape(channels, num_anchors)
    else:
        values = tensor.reshape(num_anchors, channels).T

    boxes, scores, classes = [], [], []
    original_h, original_w = image_shape[:2]
    for i in range(num_anchors):
        class_scores = values[4:, i]
        class_id = int(np.argmax(class_scores))
        score = float(class_scores[class_id])
        if not score >= conf_threshold:  # NaN-safe
            continue

        cx, cy, w, h = values[0, i], values[1, i], values[2, i], values[3, i]
        x1 = float(np.clip((cx - w / 2 - pad_left) / scale / original_w, 0.0, 1.0))
        y1 = float(np.clip((cy - h / 2 - pad_top) / scale / original_h, 0.0, 1.0))
        x2 = float(np.clip((cx + w / 2 - pad_left) / scale / original_w, 0.0, 1.0))
        y2 = float(np.clip((cy + h / 2 - pad_top) / scale / original_h, 0.0, 1.0))
        if x2 <= x1 or y2 <= y1:
            continue
        boxes.append([x1, y1, x2, y2])
        scores.append(score)
        classes.append(class_id)
    return boxes, scores, classes


def plate_sample_maps(target_w, target_h, x1, y1, crop_w, crop_h, plate_type):
    """构造 168×48 采样坐标映射（与 C++ plate_sample_point 逐式一致）。"""
    map_x = np.zeros((target_h, target_w), dtype=np.float32)
    map_y = np.zeros((target_h, target_w), dtype=np.float32)
    half_width = target_w // 2
    upper_height = crop_h * 5.0 / 12.0
    lower_start = crop_h / 3.0
    lower_height = crop_h - lower_start

    for y in range(target_h):
        for x in range(target_w):
            if plate_type == DOUBLE_LINE and x < half_width:
                map_x[y, x] = x1 + (x + 0.5) * (crop_w / half_width) - 0.5
                map_y[y, x] = y1 + (y + 0.5) * (upper_height / target_h) - 0.5
            elif plate_type == DOUBLE_LINE:
                local_x = x - half_width
                sub_w = target_w - half_width
                map_x[y, x] = x1 + (local_x + 0.5) * (crop_w / sub_w) - 0.5
                map_y[y, x] = y1 + lower_start + (y + 0.5) * (lower_height / target_h) - 0.5
            else:
                map_x[y, x] = x1 + (x + 0.5) * (crop_w / target_w) - 0.5
                map_y[y, x] = y1 + (y + 0.5) * (crop_h / target_h) - 0.5
    return map_x, map_y


def recognize(image, rec_session, box, plate_type):
    """按与 C++ 相同的采样几何裁出 168×48 BGR 输入并解码字符/颜色。"""
    height, width = image.shape[:2]
    x1 = int(np.clip(box[0] * width, 0, width - 1))
    y1 = int(np.clip(box[1] * height, 0, height - 1))
    x2 = int(np.clip(box[2] * width, x1 + 1, width))
    y2 = int(np.clip(box[3] * height, y1 + 1, height))
    crop_w, crop_h = x2 - x1, y2 - y1
    if crop_w <= 0 or crop_h <= 0:
        return None

    map_x, map_y = plate_sample_maps(REC_WIDTH, REC_HEIGHT, x1, y1, crop_w, crop_h, plate_type)
    # BORDER_REPLICATE 对应 C++ 侧的边缘 clamp 语义
    sampled = cv2.remap(image, map_x, map_y, cv2.INTER_LINEAR, borderMode=cv2.BORDER_REPLICATE)

    # ⚠️ 识别模型吃 BGR：这里**不做** BGR→RGB 翻转（与检测前处理相反）
    blob = sampled.astype(np.float32)
    blob = (blob / 255.0 - REC_MEAN) / REC_STD
    blob = blob.transpose(2, 0, 1)
    blob = np.expand_dims(blob, axis=0)

    outputs = rec_session.run(None, {rec_session.get_inputs()[0].name: blob})
    char_logits = outputs[0][0].reshape(-1, outputs[0].shape[-1])

    plate_number = ""
    confidence_sum = 0.0
    recognized = 0
    previous = 0
    for step in char_logits:
        best = int(np.argmax(step))
        if best != 0 and best != previous and best < len(PLATE_NAMES):
            plate_number += PLATE_NAMES[best]
            shifted = np.exp(step - step[best])
            probability = 1.0 / float(np.sum(shifted))
            confidence_sum += probability
            recognized += 1
        previous = best
    if not plate_number or recognized == 0:
        return None

    color = ""
    if len(outputs) > 1 and outputs[1].size >= len(COLOR_NAMES):
        color = COLOR_NAMES[int(np.argmax(outputs[1].reshape(-1)))]
    return plate_number, color, confidence_sum / recognized


def run_inference(image_path, det_model_path, rec_model_path, conf_threshold, nms_threshold):
    image = cv2.imread(image_path)
    if image is None:
        sys.exit(f"错误: 无法读取图片: {image_path}")

    det_session = ort.InferenceSession(det_model_path)
    rec_session = ort.InferenceSession(rec_model_path)

    blob, scale, pad_left, pad_top = letterbox_detect(image, conf_threshold)
    det_output = det_session.run(None, {det_session.get_inputs()[0].name: blob})
    boxes, scores, classes = decode_detections(
        det_output[0], scale, pad_left, pad_top, image.shape, conf_threshold
    )
    if not boxes:
        print(f"[{image_path}] 未检出任何车牌")
        return

    # NMS 在归一化坐标上做，与 C++ 侧一致
    pixel_boxes = [
        [b[0] * image.shape[1], b[1] * image.shape[0], b[2] * image.shape[1], b[3] * image.shape[0]]
        for b in boxes
    ]
    keep = cv2.dnn.NMSBoxes(pixel_boxes, scores, conf_threshold, nms_threshold)
    keep = np.array(keep).reshape(-1).tolist() if len(keep) else []

    print("=======================================================")
    print(f"测试图片: {image_path} ({image.shape[1]}x{image.shape[0]})")
    print(f"检出车牌数: {len(keep)}")
    print("=======================================================")

    for index in keep:
        recognized = recognize(image, rec_session, boxes[index], classes[index])
        if recognized is None:
            print(f"  * 检出车牌但识别失败 置信度: {scores[index]:.3f}")
            continue
        plate_number, color, score = recognized
        layer = "双层" if classes[index] == DOUBLE_LINE else "单层"
        print(
            f"  * 车牌号: {plate_number:<10} 颜色: {color}  类型: {layer}"
            f"  识别置信度: {score:.3f}  检测置信度: {scores[index]:.3f}"
            f"  归一化框: [{boxes[index][0]:.4f}, {boxes[index][1]:.4f}, "
            f"{boxes[index][2]:.4f}, {boxes[index][3]:.4f}]"
        )


def main():
    parser = argparse.ArgumentParser(description="离线验证车牌 ONNX 检测与识别")
    parser.add_argument("image", help="待测试图片路径")
    parser.add_argument("--det-model", default="packages/plate_recognition/targets/onnx/models/plate_detect.onnx",
                        help="检测 ONNX 模型路径")
    parser.add_argument("--rec-model", default="packages/plate_recognition/targets/onnx/models/plate_rec_color.onnx",
                        help="识别 ONNX 模型路径")
    parser.add_argument("--conf", type=float, default=0.35, help="检测置信度阈值（同 config.schema.json）")
    parser.add_argument("--nms", type=float, default=0.45, help="NMS IoU 阈值（同 config.schema.json）")
    args = parser.parse_args()

    run_inference(args.image, args.det_model, args.rec_model, args.conf, args.nms)


if __name__ == "__main__":
    main()
