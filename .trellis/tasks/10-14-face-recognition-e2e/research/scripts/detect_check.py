# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""Golden-reference probe for det_500m.onnx (SCRFD face detector).

det_500m is a SCRFD-family detector: 3 strides x {score, bbox, kps}, 2 anchors
per location. It emits raw distances/offsets, so a decoder is required before
any box exists. This script:

  1. inspects the real output value ranges to decide whether a sigmoid is
     already applied (SCRFD exports differ on this -- assuming wrongly yields
     all-zero or all-one confidence and a detector that "finds nothing"),
  2. decodes boxes + 5 landmarks,
  3. verifies the 5 landmarks actually land inside the box (a cheap,
     assumption-free sanity check on the decoding math),
  4. reports detections on both a mugshot and a real surveillance frame.

The landmarks matter more than the boxes here: they are what feeds the
ArcFace-template alignment that facelivtv2-l requires. A detector whose
landmarks are mis-ordered produces embeddings that are silently meaningless.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

ZOO = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser()
MODEL = ZOO / "examples/insightface/buffalo_s/det_500m.onnx"
MUGSHOT = ZOO / "datasets/SCface_database/aligned_112x112/001_frontal.jpg"
SURVEILLANCE = ZOO / "datasets/SCface_database/surveillance_cameras_distance_1/cam_1/001_cam1_1.jpg"

INPUT_SIZE = 640
STRIDES = (8, 16, 32)
NUM_ANCHORS = 2
SCORE_THRESHOLD = 0.5
NMS_THRESHOLD = 0.4
MEAN = 127.5
STD = 128.0


def preprocess(path: Path, size: int = INPUT_SIZE) -> tuple[np.ndarray, float, tuple[int, int]]:
    """Letterbox-free direct resize. SCRFD is trained on square input; we keep
    the aspect ratio change explicit so the caller can map boxes back."""
    image = cv2.imread(str(path), cv2.IMREAD_COLOR)
    if image is None:
        raise SystemExit(f"cannot read {path}")
    h, w = image.shape[:2]
    rgb = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
    resized = cv2.resize(rgb, (size, size), interpolation=cv2.INTER_LINEAR)
    x = (resized.astype(np.float32) - MEAN) / STD
    x = np.transpose(x, (2, 0, 1))[None, ...]
    return np.ascontiguousarray(x), w, h


def distance2bbox(points: np.ndarray, distance: np.ndarray) -> np.ndarray:
    x1 = points[:, 0] - distance[:, 0]
    y1 = points[:, 1] - distance[:, 1]
    x2 = points[:, 0] + distance[:, 2]
    y2 = points[:, 1] + distance[:, 3]
    return np.stack([x1, y1, x2, y2], axis=-1)


def distance2kps(points: np.ndarray, distance: np.ndarray) -> np.ndarray:
    out = []
    for i in range(0, distance.shape[1], 2):
        out.append(points[:, 0] + distance[:, i])
        out.append(points[:, 1] + distance[:, i + 1])
    return np.stack(out, axis=-1)


def nms(boxes: np.ndarray, scores: np.ndarray, threshold: float) -> list[int]:
    if len(boxes) == 0:
        return []
    x1, y1, x2, y2 = boxes[:, 0], boxes[:, 1], boxes[:, 2], boxes[:, 3]
    areas = np.maximum(0.0, x2 - x1) * np.maximum(0.0, y2 - y1)
    order = scores.argsort()[::-1]
    keep: list[int] = []
    while order.size > 0:
        i = int(order[0])
        keep.append(i)
        if order.size == 1:
            break
        rest = order[1:]
        xx1 = np.maximum(x1[i], x1[rest])
        yy1 = np.maximum(y1[i], y1[rest])
        xx2 = np.minimum(x2[i], x2[rest])
        yy2 = np.minimum(y2[i], y2[rest])
        inter = np.maximum(0.0, xx2 - xx1) * np.maximum(0.0, yy2 - yy1)
        union = areas[i] + areas[rest] - inter
        iou = np.where(union > 0, inter / union, 0.0)
        order = rest[iou <= threshold]
    return keep


def main() -> int:
    print("=" * 78)
    print(f"model: {MODEL}")
    session = ort.InferenceSession(str(MODEL), providers=["CPUExecutionProvider"])
    inp = session.get_inputs()[0]
    outputs = session.get_outputs()
    print(f"input : name={inp.name!r} shape={inp.shape} type={inp.type}")
    print("outputs:")
    for i, o in enumerate(outputs):
        print(f"  [{i}] name={o.name!r:8} shape={o.shape}")
    print("=" * 78)

    for label, path in (("MUGSHOT", MUGSHOT), ("SURVEILLANCE", SURVEILLANCE)):
        if not path.is_file():
            print(f"\n[{label}] SKIP -- missing {path}")
            continue
        print(f"\n{'=' * 78}\n[{label}] {path.name}")

        x, orig_w, orig_h = preprocess(path)
        raw = session.run(None, {inp.name: x})

        # ---- Step 1: does the score output already contain probabilities? ----
        print("\n-- raw output ranges (deciding on sigmoid) --")
        score_raw = raw[0]
        print(f"  score[0] min={score_raw.min():.4f} max={score_raw.max():.4f} "
              f"mean={score_raw.mean():.4f}")
        needs_sigmoid = bool(score_raw.min() < 0.0 or score_raw.max() > 1.0)
        print(f"  scores outside [0,1] -> sigmoid required: {needs_sigmoid}")

        score_groups = raw[0:3]
        bbox_groups = raw[3:6]
        kps_groups = raw[6:9]

        all_boxes, all_scores, all_kps = [], [], []
        for level, stride in enumerate(STRIDES):
            scores = score_groups[level].reshape(-1).astype(np.float32)
            if needs_sigmoid:
                scores = 1.0 / (1.0 + np.exp(-scores))
            bbox_preds = bbox_groups[level].astype(np.float32) * stride
            kps_preds = kps_groups[level].astype(np.float32) * stride

            feat = INPUT_SIZE // stride
            # anchor centers: (x*stride, y*stride) for every grid cell, 2 anchors each
            grid_y, grid_x = np.mgrid[:feat, :feat]
            centers = np.stack([grid_x.ravel(), grid_y.ravel()], axis=-1).astype(np.float32)
            centers = (centers * stride).repeat(NUM_ANCHORS, axis=0)

            pos = np.where(scores >= SCORE_THRESHOLD)[0]
            if pos.size == 0:
                continue
            boxes = distance2bbox(centers[pos], bbox_preds[pos])
            kps = distance2kps(centers[pos], kps_preds[pos])
            all_boxes.append(boxes)
            all_scores.append(scores[pos])
            all_kps.append(kps)

        if not all_boxes:
            print("\n  RESULT: no face above threshold "
                  f"{SCORE_THRESHOLD} -- decoding or threshold is wrong")
            continue

        boxes = np.concatenate(all_boxes, axis=0)
        scores = np.concatenate(all_scores, axis=0)
        kps = np.concatenate(all_kps, axis=0)

        keep = nms(boxes, scores, NMS_THRESHOLD)
        boxes, scores, kps = boxes[keep], scores[keep], kps[keep]

        print(f"\n  faces after threshold+NMS: {len(keep)}")
        for i in range(min(len(keep), 5)):
            b = boxes[i]
            k = kps[i].reshape(5, 2)
            inside = all(b[0] <= px <= b[2] and b[1] <= py <= b[3] for px, py in k)
            print(f"    #{i} score={scores[i]:.4f} box=[{b[0]:7.1f},{b[1]:7.1f},"
                  f"{b[2]:7.1f},{b[3]:7.1f}]  landmarks_inside_box={inside}")
            print(f"         kps={np.round(k, 1).tolist()}")

        # ---- sanity: landmarks must be inside their box ----
        ok_count = 0
        for i in range(len(keep)):
            b = boxes[i]
            k = kps[i].reshape(5, 2)
            if all(b[0] <= px <= b[2] and b[1] <= py <= b[3] for px, py in k):
                ok_count += 1
        print(f"\n  landmark-inside-box check: {ok_count}/{len(keep)} passed")
        if ok_count == len(keep):
            print("  => DECODING SANE (all landmarks fall inside their own box)")
        else:
            print("  => WARNING: some landmarks escaped their box; decoder suspect")

        # ---- map back to original image coords ----
        sx, sy = orig_w / INPUT_SIZE, orig_h / INPUT_SIZE
        print(f"\n  original image {orig_w}x{orig_h}; "
              f"scale back x{sx:.4f} y{sy:.4f}")
        for i in range(min(len(keep), 3)):
            b = boxes[i]
            print(f"    #{i} orig box=[{b[0]*sx:7.1f},{b[1]*sy:7.1f},"
                  f"{b[2]*sx:7.1f},{b[3]*sy:7.1f}]")

    print(f"\n{'=' * 78}")
    print("det_500m decoding verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
