# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""Resolution-aware accuracy sweep + manual similarity fit.

pipeline_check.py found a NEGATIVE separation gap on distance-1/cam-1 probes.
Before blaming the pipeline, check the data: SCface stores the same 130
identities across 7 cameras x 3 distances, and distance 1 frames are only
75x100 px while distance 3 frames are 224x168. If accuracy tracks probe
resolution, the gap is a data limitation, not a bug.

The affine fit is computed by hand (Umeyama similarity, least squares, no
RANSAC/LMEDS) because with exactly 5 correspondences the robust estimators
have nothing to be robust against and OpenCV rejects method=0.
"""

from __future__ import annotations

from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

ZOO = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser()
DB = ZOO / "datasets/SCface_database"
ALIGNED = DB / "aligned_112x112"

DET = ort.InferenceSession(
    str(ZOO / "examples/insightface/buffalo_s/det_500m.onnx"),
    providers=["CPUExecutionProvider"])
DET_IN = DET.get_inputs()[0].name
EMB = ort.InferenceSession(
    str(ZOO / "examples/facelivt/facelivtv2_l/model/facelivtv2-l.onnx"),
    providers=["CPUExecutionProvider"])
EMB_IN = EMB.get_inputs()[0].name

DET_SIZE, STRIDES, NANCHOR = 640, (8, 16, 32), 2
TEMPLATE = np.array(
    [[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
     [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float32)


def detect(image):
    h, w = image.shape[:2]
    x = cv2.resize(cv2.cvtColor(image, cv2.COLOR_BGR2RGB), (DET_SIZE, DET_SIZE))
    x = (x.astype(np.float32) - 127.5) / 128.0
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None])
    raw = DET.run(None, {DET_IN: x})
    B, S, K = [], [], []
    for lv, st in enumerate(STRIDES):
        sc = raw[lv].reshape(-1).astype(np.float32)
        bb = raw[3 + lv].astype(np.float32) * st
        kp = raw[6 + lv].astype(np.float32) * st
        f = DET_SIZE // st
        gy, gx = np.mgrid[:f, :f]
        c = (np.stack([gx.ravel(), gy.ravel()], -1).astype(np.float32) * st).repeat(NANCHOR, 0)
        pos = np.where(sc >= 0.5)[0]
        if not pos.size:
            continue
        p, d = c[pos], bb[pos]
        B.append(np.stack([p[:, 0] - d[:, 0], p[:, 1] - d[:, 1],
                           p[:, 0] + d[:, 2], p[:, 1] + d[:, 3]], -1))
        S.append(sc[pos])
        kd = kp[pos]
        K.append(np.stack([np.stack([p[:, 0] + kd[:, i], p[:, 1] + kd[:, i + 1]], -1)
                           for i in range(0, kd.shape[1], 2)], 1))
    if not B:
        return np.empty((0, 4)), np.empty(0), np.empty((0, 5, 2))
    bx, sc, kp = np.concatenate(B), np.concatenate(S), np.concatenate(K)
    o = sc.argsort()[::-1]
    keep = []
    while o.size:
        i = int(o[0]); keep.append(i)
        if o.size == 1: break
        r = o[1:]
        xx1 = np.maximum(bx[i, 0], bx[r, 0]); yy1 = np.maximum(bx[i, 1], bx[r, 1])
        xx2 = np.minimum(bx[i, 2], bx[r, 2]); yy2 = np.minimum(bx[i, 3], bx[r, 3])
        it = np.maximum(0, xx2 - xx1) * np.maximum(0, yy2 - yy1)
        ai = (bx[i, 2] - bx[i, 0]) * (bx[i, 3] - bx[i, 1])
        ar = (bx[r, 2] - bx[r, 0]) * (bx[r, 3] - bx[r, 1])
        o = r[it / np.maximum(ai + ar - it, 1e-9) <= 0.4]
    bx, sc, kp = bx[keep], sc[keep], kp[keep]
    sx, sy = w / DET_SIZE, h / DET_SIZE
    bx[:, [0, 2]] *= sx; bx[:, [1, 3]] *= sy
    kp[..., 0] *= sx; kp[..., 1] *= sy
    return bx, sc, kp


def similarity(src, dst):
    """Umeyama similarity transform (rotation + uniform scale + translation)."""
    src = np.asarray(src, np.float64); dst = np.asarray(dst, np.float64)
    ms, md = src.mean(0), dst.mean(0)
    s, d = src - ms, dst - md
    var_s = (s ** 2).sum() / len(src)
    cov = (d.T @ s) / len(src)
    U, S, Vt = np.linalg.svd(cov)
    R = U @ Vt
    if np.linalg.det(R) < 0:
        U[:, -1] *= -1
        R = U @ Vt
    scale = (S.sum()) / var_s if var_s > 0 else 1.0
    t = md - scale * (R @ ms)
    M = np.zeros((2, 3), np.float64)
    M[:, :2] = scale * R
    M[:, 2] = t
    return M


def align(img, lm):
    try:
        M = similarity(lm, TEMPLATE)
    except np.linalg.LinAlgError:
        return None
    return cv2.warpAffine(img, M.astype(np.float32), (112, 112), borderValue=0.0)


def embed(img):
    rgb = cv2.cvtColor(img, cv2.COLOR_BGR2RGB)
    if rgb.shape[:2] != (112, 112):
        rgb = cv2.resize(rgb, (112, 112))
    x = (rgb.astype(np.float32) - 127.5) / 127.5
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None])
    return np.asarray(EMB.run(None, {EMB_IN: x})[0], np.float32).reshape(-1)


def cos(a, b):
    na, nb = np.linalg.norm(a), np.linalg.norm(b)
    return float(np.dot(a, b) / (na * nb)) if na and nb else 0.0


def pad(img, f=1.0):
    h, w = img.shape[:2]
    c = np.zeros((int(h * (1 + 2 * f)), int(w * (1 + 2 * f)), 3), np.uint8)
    y0, x0 = int(h * f), int(w * f)
    c[y0:y0 + h, x0:x0 + w] = img
    return c, (x0, y0)


# --------------------------------------------------------------------------- #
print("=" * 78)
print("STEP 0  gallery (130 aligned mugshots)")
print("=" * 78)
gallery = {p.name.split("_")[0]: embed(cv2.imread(str(p)))
           for p in sorted(ALIGNED.glob("*_frontal.jpg"))}
print(f"  {len(gallery)} identities enrolled")

print("\n" + "=" * 78)
print("STEP 1  round-trip with the hand-rolled similarity fit")
print("=" * 78)
cvs = []
for p in sorted(ALIGNED.glob("*_frontal.jpg"))[:30]:
    img = cv2.imread(str(p))
    direct = embed(img)
    canvas, (x0, y0) = pad(img, 1.0)
    bx, sc, kp = detect(canvas)
    if not len(bx):
        continue
    lm = kp[int(sc.argmax())] - np.array([x0, y0], np.float32)
    w = align(img, lm)
    if w is None:
        continue
    cvs.append(cos(direct, embed(w)))
cvs = np.array(cvs)
print(f"  n={len(cvs)}  mean={cvs.mean():+.4f}  min={cvs.min():+.4f}  "
      f"p10={np.percentile(cvs, 10):+.4f}")
print("  (this upper-bounds what alignment error alone costs us)")

print("\n" + "=" * 78)
print("STEP 2  accuracy vs probe resolution (all cameras x distances)")
print("=" * 78)
print(f"{'distance':9} {'camera':8} {'frame':11} {'face_px':8} {'det':5} "
      f"{'same_mean':10} {'diff_mean':10} {'gap':9} {'rank1':7}")
print("-" * 78)

results = {}
for dist in (1, 2, 3):
    for cam in range(1, 8):
        d = DB / f"surveillance_cameras_distance_{dist}" / f"cam_{cam}"
        if not d.is_dir():
            continue
        files = sorted(d.glob(f"*_cam{cam}_{dist}.jpg"))
        if not files:
            continue

        same, diff, rank1_hits, n = [], [], 0, 0
        frame_shape, face_px = None, 0.0
        for f in files:
            sid = f.name.split("_")[0]
            if sid not in gallery:
                continue
            img = cv2.imread(str(f))
            if img is None:
                continue
            frame_shape = img.shape
            bx, sc, kp = detect(img)
            if not len(bx):
                continue
            i = int(sc.argmax())
            face_px += float(bx[i, 2] - bx[i, 0])
            w = align(img, kp[i])
            if w is None:
                continue
            pe = embed(w)
            n += 1
            for gid, ge in gallery.items():
                c = cos(pe, ge)
                (same if gid == sid else diff).append(c)
            best = max(gallery.items(), key=lambda kv: cos(pe, kv[1]))[0]
            if best == sid:
                rank1_hits += 1

        if not n:
            print(f"{dist:^9} {cam:^8} {str(frame_shape):11} {'-':8} {0:^5} "
                  f"{'no detection':<40}")
            continue
        sa, da = np.array(same), np.array(diff)
        gap = sa.min() - da.max()
        r1 = 100.0 * rank1_hits / n
        results[(dist, cam)] = (sa.mean(), da.mean(), gap, r1, n, face_px / n)
        print(f"{dist:^9} {cam:^8} {frame_shape[1]}x{frame_shape[0]:<6} "
              f"{face_px / n:7.0f} {n:^5} {sa.mean():+10.4f} {da.mean():+10.4f} "
              f"{gap:+9.4f} {r1:6.1f}%")

print("\n" + "=" * 78)
print("STEP 3  per-distance summary")
print("=" * 78)
for dist in (1, 2, 3):
    rows = [v for (d, c), v in results.items() if d == dist]
    if not rows:
        continue
    rows = np.array(rows)
    print(f"  distance {dist}: face_px~{rows[:, 5].mean():5.0f}  "
          f"same_mean={rows[:, 0].mean():+.4f}  diff_mean={rows[:, 1].mean():+.4f}  "
          f"rank1={rows[:, 3].mean():5.1f}%")

print("\n" + "=" * 78)
print("STEP 4  best-quality subset (distance 3) detailed separation")
print("=" * 78)
best_cam = max(((d, c) for (d, c) in results if d == 3),
               key=lambda k: results[k][3], default=None)
if best_cam:
    d, c = best_cam
    dirp = DB / f"surveillance_cameras_distance_{d}" / f"cam_{c}"
    same, diff = [], []
    for f in sorted(dirp.glob(f"*_cam{c}_{d}.jpg")):
        sid = f.name.split("_")[0]
        if sid not in gallery:
            continue
        img = cv2.imread(str(f))
        bx, sc, kp = detect(img)
        if not len(bx):
            continue
        w = align(img, kp[int(sc.argmax())])
        if w is None:
            continue
        pe = embed(w)
        for gid, ge in gallery.items():
            cc = cos(pe, ge)
            (same if gid == sid else diff).append(cc)
    sa, da = np.array(same), np.array(diff)
    print(f"  camera {c}, distance {d}:")
    print(f"    SAME  n={sa.size:5d}  mean={sa.mean():+.4f}  min={sa.min():+.4f}")
    print(f"    DIFF  n={da.size:5d}  mean={da.mean():+.4f}  max={da.max():+.4f}")
    print(f"    gap   = {sa.min() - da.max():+.4f}")
    if sa.min() > da.max():
        print("    => POSITIVE separation: a single threshold separates the sets")
    else:
        print("    => overlap remains; report the achievable operating point")
    print(f"    rank-1 = {results[best_cam][3]:.1f}%")
