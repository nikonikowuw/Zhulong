# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""Diagnose the negative separation gap found by pipeline_check.py.

Suspicions, in order of cheapness to test:
  S1. Landmark precision. On an already-aligned mugshot the detected
      landmarks should land on the ArcFace template. Measuring the error
      directly is assumption-free and localises the problem to detection
      rather than embedding.
  S2. The fit method. estimateAffinePartial2D with LMEDS discards points as
      outliers; with only 5 points that can throw away a good landmark.
      Compare LMEDS vs plain least squares.
  S3. Probe resolution. SCface distance-1 cam1 frames are 75x100 -- a face
      there is ~25x45 px, upscaled ~4x to 112. Check whether accuracy tracks
      probe resolution by testing every camera/distance.
"""

from __future__ import annotations

from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

ZOO = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser()
DB = ZOO / "datasets/SCface_database"
ALIGNED = DB / "aligned_112x112"

DET_MODEL = ZOO / "examples/insightface/buffalo_s/det_500m.onnx"
EMB_MODEL = ZOO / "examples/facelivt/facelivtv2_l/model/facelivtv2-l.onnx"

DET_SIZE, STRIDES, NUM_ANCHORS = 640, (8, 16, 32), 2
DET_MEAN, DET_STD = 127.5, 128.0
DET_SCORE_THRESHOLD, DET_NMS_THRESHOLD = 0.5, 0.4

TEMPLATE = np.array(
    [[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
     [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float32
)

DET = ort.InferenceSession(str(DET_MODEL), providers=["CPUExecutionProvider"])
DET_IN = DET.get_inputs()[0].name
EMB = ort.InferenceSession(str(EMB_MODEL), providers=["CPUExecutionProvider"])
EMB_IN = EMB.get_inputs()[0].name


def detect(image):
    h, w = image.shape[:2]
    rgb = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
    x = cv2.resize(rgb, (DET_SIZE, DET_SIZE)).astype(np.float32)
    x = (x - DET_MEAN) / DET_STD
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None])
    raw = DET.run(None, {DET_IN: x})
    boxes_all, scores_all, kps_all = [], [], []
    for lv, stride in enumerate(STRIDES):
        sc = raw[lv].reshape(-1).astype(np.float32)
        bb = raw[3 + lv].astype(np.float32) * stride
        kp = raw[6 + lv].astype(np.float32) * stride
        feat = DET_SIZE // stride
        gy, gx = np.mgrid[:feat, :feat]
        c = np.stack([gx.ravel(), gy.ravel()], -1).astype(np.float32)
        c = (c * stride).repeat(NUM_ANCHORS, 0)
        pos = np.where(sc >= DET_SCORE_THRESHOLD)[0]
        if not pos.size:
            continue
        p = c[pos]
        d = bb[pos]
        boxes_all.append(np.stack([p[:, 0] - d[:, 0], p[:, 1] - d[:, 1],
                                   p[:, 0] + d[:, 2], p[:, 1] + d[:, 3]], -1))
        scores_all.append(sc[pos])
        kd = kp[pos]
        kps_all.append(np.stack([np.stack([p[:, 0] + kd[:, i], p[:, 1] + kd[:, i + 1]], -1)
                                 for i in range(0, kd.shape[1], 2)], 1))
    if not boxes_all:
        return np.empty((0, 4)), np.empty(0), np.empty((0, 5, 2))
    bx, sc, kp = np.concatenate(boxes_all), np.concatenate(scores_all), np.concatenate(kps_all)
    order = sc.argsort()[::-1]
    keep = []
    while order.size:
        i = int(order[0]); keep.append(i)
        if order.size == 1: break
        rest = order[1:]
        xx1 = np.maximum(bx[i, 0], bx[rest, 0]); yy1 = np.maximum(bx[i, 1], bx[rest, 1])
        xx2 = np.minimum(bx[i, 2], bx[rest, 2]); yy2 = np.minimum(bx[i, 3], bx[rest, 3])
        inter = np.maximum(0, xx2 - xx1) * np.maximum(0, yy2 - yy1)
        a_i = (bx[i, 2] - bx[i, 0]) * (bx[i, 3] - bx[i, 1])
        a_r = (bx[rest, 2] - bx[rest, 0]) * (bx[rest, 3] - bx[rest, 1])
        iou = inter / np.maximum(a_i + a_r - inter, 1e-9)
        order = rest[iou <= DET_NMS_THRESHOLD]
    bx, sc, kp = bx[keep], sc[keep], kp[keep]
    sx, sy = w / DET_SIZE, h / DET_SIZE
    bx[:, [0, 2]] *= sx; bx[:, [1, 3]] *= sy
    kp[..., 0] *= sx; kp[..., 1] *= sy
    return bx, sc, kp


def embed(img):
    rgb = cv2.cvtColor(img, cv2.COLOR_BGR2RGB)
    if rgb.shape[:2] != (112, 112):
        rgb = cv2.resize(rgb, (112, 112))
    x = (rgb.astype(np.float32) - 127.5) / 127.5
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None])
    return np.asarray(EMB.run(None, {EMB_IN: x})[0], np.float32).reshape(-1)


def cos(a, b):
    return float(np.dot(a, b) / (np.linalg.norm(a) * np.linalg.norm(b)))


def pad(img, factor=1.0):
    h, w = img.shape[:2]
    c = np.zeros((int(h * (1 + 2 * factor)), int(w * (1 + 2 * factor)), 3), np.uint8)
    y0, x0 = int(h * factor), int(w * factor)
    c[y0:y0 + h, x0:x0 + w] = img
    return c, (x0, y0)


print("=" * 78)
print("S1  LANDMARK PRECISION on already-aligned mugshots")
print("=" * 78)
print("aligned_112x112 IS the ArcFace template, so detected landmarks should")
print("land on it. Error is reported in pixels of the 112x112 frame.\n")

errs = []
for p in sorted(ALIGNED.glob("*_frontal.jpg"))[:25]:
    img = cv2.imread(str(p))
    canvas, (x0, y0) = pad(img, 1.0)
    bx, sc, kp = detect(canvas)
    if not len(bx):
        continue
    i = int(sc.argmax())
    lm = kp[i] - np.array([x0, y0], np.float32)
    err = np.linalg.norm(lm - TEMPLATE, axis=1)
    errs.append(err)
    if len(errs) <= 5:
        print(f"  {p.name}: per-point px error = {np.round(err, 2).tolist()}  "
              f"mean={err.mean():.2f}")

errs = np.array(errs)
print(f"\n  n={len(errs)}  overall mean={errs.mean():.2f}px  "
      f"max={errs.max():.2f}px  p90={np.percentile(errs, 90):.2f}px")
print(f"  per-landmark mean: {np.round(errs.mean(0), 2).tolist()}")
print("  (inter-ocular distance on the template is ~35px, so ~2px is ~6%)")

print("\n" + "=" * 78)
print("S2  FIT METHOD: LMEDS vs least squares")
print("=" * 78)


def warp(img, lm, method):
    m, _ = cv2.estimateAffinePartial2D(lm.astype(np.float32), TEMPLATE, method=method)
    if m is None:
        return None
    return cv2.warpAffine(img, m, (112, 112), borderValue=0.0)


for method, name in ((cv2.LMEDS, "LMEDS"), (0, "least-squares")):
    cos_vals = []
    for p in sorted(ALIGNED.glob("*_frontal.jpg"))[:25]:
        img = cv2.imread(str(p))
        direct = embed(img)
        canvas, (x0, y0) = pad(img, 1.0)
        bx, sc, kp = detect(canvas)
        if not len(bx):
            continue
        i = int(sc.argmax())
        lm = kp[i] - np.array([x0, y0], np.float32)
        w = warp(img, lm, method)
        if w is None:
            continue
        cos_vals.append(cos(direct, embed(w)))
    cos_vals = np.array(cos_vals)
    print(f"  {name:14}: round-trip cosine mean={cos_vals.mean():+.4f} "
          f"min={cos_vals.min():+.4f}")

print("\n" + "=" * 78)
print("S3  PROBE RESOLUTION vs ACCURACY")
print("=" * 78)
print("If accuracy tracks probe resolution, the poor separation is a data")
print("limitation, not a pipeline bug.\n")

gallery = {}
for p in sorted(ALIGNED.glob("*_frontal.jpg")):
    gallery[p.name.split("_")[0]] = embed(cv2.imread(str(p)))

for dist in (1, 2, 3):
    base = DB / f"surveillance_cameras_distance_{dist}"
    if not base.is_dir():
        continue
    for cam in sorted(base.iterdir()):
        files = sorted(cam.glob(f"*_cam{cam.name.split('_')[1]}_{dist}.jpg"))
        if not files:
            continue
        probe = cv2.imread(str(files[0]))
        if probe is None:
            continue
        bx, sc, kp = detect(probe)
        face_px = 0.0
        if len(bx):
            i = int(sc.argmax())
            face_px = float(bx[i, 2] - bx[i, 0])
        print(f"  distance {dist} {cam.name}: frame {probe.shape[1]}x{probe.shape[0]}  "
              f"detected_face_width={face_px:.0f}px  detections={len(bx)}")
