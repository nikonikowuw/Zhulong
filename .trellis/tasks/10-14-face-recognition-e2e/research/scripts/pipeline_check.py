# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""End-to-end golden-reference pipeline: det_500m -> align -> facelivtv2-l.

Establishes the numbers the C++ implementation must reproduce. Uses SCface,
whose filenames carry identity ground truth: `NNN_frontal.jpg` (mugshot) and
`NNN_camC_D.jpg` (surveillance, camera C, distance D) share identity NNN.

Three things are measured, in increasing order of strictness:

  1. ALIGNMENT ROUND-TRIP -- feed an already-aligned 112x112 mugshot through
     detect -> align -> embed and compare against embedding the image directly.
     The aligned set is the ArcFace template already, so a correct pipeline
     should reproduce the direct embedding almost exactly. This isolates
     alignment bugs from model bugs without needing labels.

  2. VERIFICATION PAIRS -- same identity (mugshot vs surveillance) vs
     different identity, reporting the cosine distributions and the
     separation gap. This is the real accuracy signal.

  3. RANK-1 IDENTIFICATION -- gallery of all mugshots, probe each surveillance
     frame, report rank-1 hit rate. This is the number AC1/AC3 will be
     measured against.

Notes on what is deliberately NOT done here:
  - No NPU, no RKNN, no quantization. These are float-ONNX numbers: the
    reference the board must be compared against, not the board result.
  - The detector's landmark order is assumed to be the conventional
    [left-eye, right-eye, nose, left-mouth, right-mouth]; step 1 exists
    precisely to catch it if that assumption is wrong.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

ZOO = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser()
DET_MODEL = ZOO / "examples/insightface/buffalo_s/det_500m.onnx"
EMB_MODEL = ZOO / "examples/facelivt/facelivtv2_l/model/facelivtv2-l.onnx"
DB = ZOO / "datasets/SCface_database"
ALIGNED = DB / "aligned_112x112"
SURVEILLANCE = DB / "surveillance_cameras_distance_1"

DET_SIZE = 640
STRIDES = (8, 16, 32)
NUM_ANCHORS = 2
DET_MEAN, DET_STD = 127.5, 128.0
DET_SCORE_THRESHOLD = 0.5
DET_NMS_THRESHOLD = 0.4

EMB_SIZE = 112
EMB_MEAN, EMB_STD = 127.5, 127.5

# The ArcFace 5-point template on 112x112, identical in both model READMEs.
ARCFACE_TEMPLATE = np.array(
    [
        [38.2946, 51.6963],
        [73.5318, 51.5014],
        [56.0252, 71.7366],
        [41.5493, 92.3655],
        [70.7299, 92.2041],
    ],
    dtype=np.float32,
)


# --------------------------------------------------------------------------- #
# detector
# --------------------------------------------------------------------------- #
def detect_faces(image: np.ndarray) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Returns (boxes, scores, landmarks) in ORIGINAL image coordinates."""
    h, w = image.shape[:2]
    rgb = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
    resized = cv2.resize(rgb, (DET_SIZE, DET_SIZE), interpolation=cv2.INTER_LINEAR)
    x = (resized.astype(np.float32) - DET_MEAN) / DET_STD
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None, ...])

    raw = DET_SESSION.run(None, {DET_INPUT: x})
    # score outputs already carry a Sigmoid inside the graph (verified: nodes
    # 443/468/493 are fed directly by Sigmoid) -- do NOT apply it again.
    score_groups, bbox_groups, kps_groups = raw[0:3], raw[3:6], raw[6:9]

    boxes_all, scores_all, kps_all = [], [], []
    for level, stride in enumerate(STRIDES):
        scores = score_groups[level].reshape(-1).astype(np.float32)
        bbox_preds = bbox_groups[level].astype(np.float32) * stride
        kps_preds = kps_groups[level].astype(np.float32) * stride

        feat = DET_SIZE // stride
        gy, gx = np.mgrid[:feat, :feat]
        centers = np.stack([gx.ravel(), gy.ravel()], axis=-1).astype(np.float32)
        centers = (centers * stride).repeat(NUM_ANCHORS, axis=0)

        pos = np.where(scores >= DET_SCORE_THRESHOLD)[0]
        if pos.size == 0:
            continue
        c = centers[pos]
        d = bbox_preds[pos]
        boxes = np.stack([c[:, 0] - d[:, 0], c[:, 1] - d[:, 1],
                          c[:, 0] + d[:, 2], c[:, 1] + d[:, 3]], axis=-1)
        kd = kps_preds[pos]
        kps = np.stack([np.stack([c[:, 0] + kd[:, i], c[:, 1] + kd[:, i + 1]], axis=-1)
                        for i in range(0, kd.shape[1], 2)], axis=1)
        boxes_all.append(boxes)
        scores_all.append(scores[pos])
        kps_all.append(kps)

    if not boxes_all:
        return np.empty((0, 4)), np.empty(0), np.empty((0, 5, 2))

    boxes = np.concatenate(boxes_all)
    scores = np.concatenate(scores_all)
    kps = np.concatenate(kps_all)

    keep = nms(boxes, scores, DET_NMS_THRESHOLD)
    boxes, scores, kps = boxes[keep], scores[keep], kps[keep]

    # scale from the 640x640 letterbox-free resize back to the original frame
    sx, sy = w / DET_SIZE, h / DET_SIZE
    boxes[:, [0, 2]] *= sx
    boxes[:, [1, 3]] *= sy
    kps[..., 0] *= sx
    kps[..., 1] *= sy
    return boxes, scores, kps


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


# --------------------------------------------------------------------------- #
# alignment + embedding
# --------------------------------------------------------------------------- #
def warp_to_template(image: np.ndarray, landmarks: np.ndarray) -> np.ndarray:
    """Similarity-transform the face onto the ArcFace template.

    estimateAffinePartial2D gives a 4-DoF (rotation+scale+translation) fit, which
    is what ArcFace alignment uses -- a full 8-DoF homography would shear the
    face and shift the embedding.
    """
    m, _ = cv2.estimateAffinePartial2D(
        landmarks.astype(np.float32), ARCFACE_TEMPLATE, method=cv2.LMEDS
    )
    if m is None:
        return None
    return cv2.warpAffine(image, m, (EMB_SIZE, EMB_SIZE), borderValue=0.0)


def embed(aligned_bgr: np.ndarray) -> np.ndarray:
    rgb = cv2.cvtColor(aligned_bgr, cv2.COLOR_BGR2RGB)
    if rgb.shape[:2] != (EMB_SIZE, EMB_SIZE):
        rgb = cv2.resize(rgb, (EMB_SIZE, EMB_SIZE), interpolation=cv2.INTER_LINEAR)
    x = (rgb.astype(np.float32) - EMB_MEAN) / EMB_STD
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None, ...])
    y = EMB_SESSION.run(None, {EMB_INPUT: x})[0]
    return np.asarray(y, dtype=np.float32).reshape(-1)


def cos(a: np.ndarray, b: np.ndarray) -> float:
    na, nb = np.linalg.norm(a), np.linalg.norm(b)
    return float(np.dot(a, b) / (na * nb)) if na and nb else 0.0


# --------------------------------------------------------------------------- #
# sessions
# --------------------------------------------------------------------------- #
print("=" * 78)
DET_SESSION = ort.InferenceSession(str(DET_MODEL), providers=["CPUExecutionProvider"])
DET_INPUT = DET_SESSION.get_inputs()[0].name
EMB_SESSION = ort.InferenceSession(str(EMB_MODEL), providers=["CPUExecutionProvider"])
EMB_INPUT = EMB_SESSION.get_inputs()[0].name
print(f"detector : {DET_MODEL.name}")
print(f"embedder : {EMB_MODEL.name}")
print("=" * 78)


# --------------------------------------------------------------------------- #
# 1. alignment round-trip
# --------------------------------------------------------------------------- #
def step1_alignment_roundtrip() -> bool:
    print("\n" + "=" * 78)
    print("STEP 1  alignment round-trip")
    print("=" * 78)
    print("The aligned_112x112 set is already ArcFace-aligned, so")
    print("  direct embedding  vs  detect->align->embed")
    print("should agree closely. A large gap means the landmark order or the")
    print("warp is wrong.\n")

    ids = ["001", "002", "003", "004", "005"]
    worst = 1.0
    for sid in ids:
        p = ALIGNED / f"{sid}_frontal.jpg"
        if not p.is_file():
            continue
        img = cv2.imread(str(p))
        direct = embed(img)

        # The mugshot fills the frame, which the detector scores low (verified:
        # 0.1066 raw). Pad it so the face sits inside a plausible scene.
        h, w = img.shape[:2]
        pad = 1.0
        canvas = np.zeros((int(h * (1 + 2 * pad)), int(w * (1 + 2 * pad)), 3), np.uint8)
        canvas[int(h * pad):int(h * pad) + h, int(w * pad):int(w * pad) + w] = img

        boxes, scores, kps = detect_faces(canvas)
        if len(boxes) == 0:
            print(f"  {sid}: no detection on padded mugshot -- SKIP")
            continue
        best = int(scores.argmax())
        # undo the padding offset to get landmarks in the original frame
        lm = kps[best] - np.array([w * pad, h * pad], dtype=np.float32)
        warped = warp_to_template(img, lm)
        if warped is None:
            print(f"  {sid}: warp failed -- SKIP")
            continue
        via = embed(warped)
        c = cos(direct, via)
        worst = min(worst, c)
        print(f"  {sid}: cosine(direct, detect->align->embed) = {c:+.4f}")

    print(f"\n  worst round-trip cosine = {worst:+.4f}")
    ok = worst > 0.95
    print(f"  => alignment pipeline {'CONSISTENT' if ok else 'SUSPECT'}")
    return ok


# --------------------------------------------------------------------------- #
# 2. verification pairs
# --------------------------------------------------------------------------- #
def step2_verification_pairs(n_ids: int = 40) -> None:
    print("\n" + "=" * 78)
    print("STEP 2  verification pairs (mugshot vs surveillance, distance 1)")
    print("=" * 78)

    gallery: dict[str, np.ndarray] = {}
    probes: dict[str, np.ndarray] = {}
    for p in sorted(ALIGNED.glob("*_frontal.jpg")):
        sid = p.name.split("_")[0]
        gallery[sid] = embed(cv2.imread(str(p)))

    cam1 = SURVEILLANCE / "cam_1"
    for p in sorted(cam1.glob("*_cam1_1.jpg"))[:n_ids]:
        sid = p.name.split("_")[0]
        img = cv2.imread(str(p))
        if img is None:
            continue
        boxes, scores, kps = detect_faces(img)
        if len(boxes) == 0:
            continue
        best = int(scores.argmax())
        warped = warp_to_template(img, kps[best])
        if warped is None:
            continue
        probes[sid] = embed(warped)

    print(f"gallery: {len(gallery)} mugshots    probes with a detected face: {len(probes)}\n")

    same, diff = [], []
    for sid, pe in probes.items():
        for gid, ge in gallery.items():
            c = cos(pe, ge)
            (same if gid == sid else diff).append(c)

    same_a, diff_a = np.array(same), np.array(diff)
    if same_a.size:
        print(f"  SAME identity  n={same_a.size:5d}  "
              f"mean={same_a.mean():+.4f}  min={same_a.min():+.4f}  max={same_a.max():+.4f}")
    if diff_a.size:
        print(f"  DIFF identity  n={diff_a.size:5d}  "
              f"mean={diff_a.mean():+.4f}  min={diff_a.min():+.4f}  max={diff_a.max():+.4f}")

    if same_a.size and diff_a.size:
        print(f"\n  separation gap (same.min - diff.max) = "
              f"{same_a.min() - diff_a.max():+.4f}")
        print(f"  EER-ish midpoint = {(same_a.mean() + diff_a.mean()) / 2:+.4f}")
        print("\n  reference: the model README's 0.50 demo threshold is explicitly")
        print("  documented as NOT a calibrated operating point.")

    # ---- rank-1 identification ----
    print("\n  rank-1 identification:")
    hits = 0
    total = 0
    for sid, pe in probes.items():
        best_id = max(gallery.items(), key=lambda kv: cos(pe, kv[1]))[0]
        total += 1
        if best_id == sid:
            hits += 1
    if total:
        print(f"    {hits}/{total} = {100.0 * hits / total:.1f}%")
    return same_a, diff_a


# --------------------------------------------------------------------------- #
# 3. board-comparison checklist
# --------------------------------------------------------------------------- #
def step3_export_reference(gallery: dict[str, np.ndarray] | None = None) -> None:
    print("\n" + "=" * 78)
    print("STEP 3  reference artifact for the C++ / RKNN implementation")
    print("=" * 78)
    out = Path("/tmp/face-probe/reference.npz")
    ids = []
    feats = []
    for p in sorted(ALIGNED.glob("*_frontal.jpg")):
        ids.append(p.name.split("_")[0])
        feats.append(embed(cv2.imread(str(p))))
    np.savez(out, ids=np.array(ids), embeddings=np.array(feats))
    print(f"  wrote {out}")
    print(f"  {len(ids)} embeddings x {feats[0].shape[0]} dims")
    print("\n  Use it to answer, on the board:")
    print("    cosine(cpp_embedding, reference_embedding) per identity")
    print("    fp16 RKNN should land > 0.999 ; int8 around 0.86 (documented)")
    print("    a large drop means the quantization or the preprocessing drifted")


if __name__ == "__main__":
    ok_align = step1_alignment_roundtrip()
    same, diff = step2_verification_pairs()
    step3_export_reference()
    print("\n" + "=" * 78)
    print(f"alignment round-trip: {'PASS' if ok_align else 'FAIL'}")
    print("=" * 78)
    sys.exit(0 if ok_align else 1)
