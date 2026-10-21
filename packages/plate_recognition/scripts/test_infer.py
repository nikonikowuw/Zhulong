#!/usr/bin/env python3
"""
test_infer.py - 车牌检测与识别真实图片离线推理验证脚本
"""

import argparse
import os
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

def run_inference(img_path, det_model_path, rec_model_path, conf_thresh=0.35, nms_thresh=0.45):
    img = cv2.imread(img_path)
    if img is None:
        print(f"错误: 无法读取图片: {img_path}")
        return

    det_session = ort.InferenceSession(det_model_path)
    rec_session = ort.InferenceSession(rec_model_path)

    h0, w0 = img.shape[:2]
    r = min(640 / h0, 640 / w0)
    rw, rh = int(w0 * r), int(h0 * r)
    dw, dh = (640 - rw) // 2, (640 - rh) // 2

    resized = cv2.resize(img, (rw, rh))
    letterbox_img = np.full((640, 640, 3), 114, dtype=np.uint8)
    letterbox_img[dh:dh+rh, dw:dw+rw] = resized

    blob = letterbox_img[:, :, ::-1].transpose(2, 0, 1).astype(np.float32) / 255.0
    blob = np.expand_dims(blob, axis=0)

    # 1. 检测
    out0 = det_session.run(None, {det_session.get_inputs()[0].name: blob})[0][0]

    boxes, scores, classes = [], [], []
    for i in range(out0.shape[1]):
        cx, cy, w, h, s0, s1 = out0[:, i]
        score = max(s0, s1)
        if score > conf_thresh:
            cls_id = 1 if s1 > s0 else 0
            x1 = (cx - w / 2 - dw) / r
            y1 = (cy - h / 2 - dh) / r
            x2 = (cx + w / 2 - dw) / r
            y2 = (cy + h / 2 - dh) / r
            boxes.append([x1, y1, x2, y2])
            scores.append(float(score))
            classes.append(cls_id)

    if not boxes:
        print(f"[{img_path}] 未检出任何车牌")
        return

    indices = cv2.dnn.NMSBoxes(boxes, scores, conf_thresh, nms_thresh)
    print(f"\n=======================================================")
    print(f"测试图片: {img_path} ({w0}x{h0})")
    print(f"检出车牌数: {len(indices)}")
    print(f"=======================================================")

    for idx in indices:
        x1, y1, x2, y2 = [int(v) for v in boxes[idx]]
        x1, y1 = max(0, x1), max(0, y1)
        x2, y2 = min(w0, x2), min(h0, y2)
        cls_id = classes[idx]
        score = scores[idx]

        crop = img[y1:y2, x1:x2]
        if crop.size == 0:
            continue

        if cls_id == 1: # 双层车牌切块重拼
            ch, cw = crop.shape[:2]
            upper = crop[0:int(5/12 * ch), :]
            lower = crop[int(1/3 * ch):, :]
            upper_resized = cv2.resize(upper, (lower.shape[1], lower.shape[0]))
            crop = np.hstack([upper_resized, lower])

        crop_resized = cv2.resize(crop, (168, 48))
        rec_blob = crop_resized.astype(np.float32)
        rec_blob = (rec_blob / 255.0 - 0.588) / 0.193
        rec_blob = rec_blob.transpose(2, 0, 1)
        rec_blob = np.expand_dims(rec_blob, axis=0)

        # 2. 识别
        char_out, color_out = rec_session.run(None, {rec_session.get_inputs()[0].name: rec_blob})

        # CTC 解码
        char_indices = np.argmax(char_out[0], axis=-1)
        plate_str = ""
        pre = 0
        for c in char_indices:
            if c != 0 and c != pre:
                plate_str += PLATE_NAMES[c]
            pre = c

        color_idx = np.argmax(color_out[0])
        color_str = COLOR_NAMES[color_idx]

        print(f"  * 车牌号: {plate_str:<10} 颜色: {color_str}  类型: {'双层' if cls_id == 1 else '单层'}  置信度: {score:.3f}  坐标: [{x1}, {y1}, {x2}, {y2}]")

def main():
    parser = argparse.ArgumentParser(description="离线验证车牌 ONNX 检测与识别")
    parser.add_argument("image", help="待测试图片路径")
    parser.add_argument("--det-model", default="packages/plate_recognition/targets/cpu/models/plate_detect.onnx", help="检测 ONNX 模型路径")
    parser.add_argument("--rec-model", default="packages/plate_recognition/targets/cpu/models/plate_rec_color.onnx", help="识别 ONNX 模型路径")
    args = parser.parse_args()

    run_inference(args.image, args.det_model, args.rec_model)

if __name__ == "__main__":
    main()
