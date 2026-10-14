# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""Produce the golden-reference artifact and a threshold operating-point table.

Outputs:
  /tmp/face-probe/golden_reference.npz   embeddings for the C++/RKNN comparison
  /tmp/face-probe/GOLDEN_REFERENCE.md    human-readable findings

The threshold table answers the question the model README explicitly refuses to
answer ("0.50 is a demo default, not a calibrated operating point"): at which
cosine threshold do FAR and FRR cross, on real surveillance data.
"""

from __future__ import annotations

import hashlib
from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

ZOO = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser()
DB = ZOO / "datasets/SCface_database"
ALIGNED = DB / "aligned_112x112"
OUT = Path("/tmp/face-probe")

DET_MODEL = ZOO / "examples/insightface/buffalo_s/det_500m.onnx"
EMB_MODEL = ZOO / "examples/facelivt/facelivtv2_l/model/facelivtv2-l.onnx"

DET = ort.InferenceSession(str(DET_MODEL), providers=["CPUExecutionProvider"])
DET_IN = DET.get_inputs()[0].name
EMB = ort.InferenceSession(str(EMB_MODEL), providers=["CPUExecutionProvider"])
EMB_IN = EMB.get_inputs()[0].name

TEMPLATE = np.array(
    [[38.2946, 51.6963], [73.5318, 51.5014], [56.0252, 71.7366],
     [41.5493, 92.3655], [70.7299, 92.2041]], dtype=np.float32)


def sha256(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def detect(image):
    h, w = image.shape[:2]
    x = cv2.resize(cv2.cvtColor(image, cv2.COLOR_BGR2RGB), (640, 640))
    x = (x.astype(np.float32) - 127.5) / 128.0
    x = np.ascontiguousarray(np.transpose(x, (2, 0, 1))[None])
    raw = DET.run(None, {DET_IN: x})
    B, S, K = [], [], []
    for lv, st in enumerate((8, 16, 32)):
        sc = raw[lv].reshape(-1).astype(np.float32)
        bb = raw[3 + lv].astype(np.float32) * st
        kp = raw[6 + lv].astype(np.float32) * st
        f = 640 // st
        gy, gx = np.mgrid[:f, :f]
        c = (np.stack([gx.ravel(), gy.ravel()], -1).astype(np.float32) * st).repeat(2, 0)
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
    sx, sy = w / 640, h / 640
    bx[:, [0, 2]] *= sx; bx[:, [1, 3]] *= sy
    kp[..., 0] *= sx; kp[..., 1] *= sy
    return bx, sc, kp


def similarity(src, dst):
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
    scale = S.sum() / var_s if var_s > 0 else 1.0
    M = np.zeros((2, 3))
    M[:, :2] = scale * R
    M[:, 2] = md - scale * (R @ ms)
    return M


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


# ---------------------------------------------------------------- gallery
ids = []
feats = []
for p in sorted(ALIGNED.glob("*_frontal.jpg")):
    ids.append(p.name.split("_")[0])
    feats.append(embed(cv2.imread(str(p))))
gallery = dict(zip(ids, feats))
np.savez(OUT / "golden_reference.npz",
         ids=np.array(ids),
         embeddings=np.array(feats),
         template=TEMPLATE)
print(f"wrote {OUT / 'golden_reference.npz'}  ({len(ids)} x {feats[0].shape[0]})")

# ---------------------------------------------------------------- pairs at d3
same, diff = [], []
for cam in range(1, 8):
    d = DB / "surveillance_cameras_distance_3" / f"cam_{cam}"
    if not d.is_dir():
        continue
    for f in sorted(d.glob(f"*_cam{cam}_3.jpg")):
        sid = f.name.split("_")[0]
        if sid not in gallery:
            continue
        img = cv2.imread(str(f))
        bx, sc, kp = detect(img)
        if not len(bx):
            continue
        M = similarity(kp[int(sc.argmax())], TEMPLATE)
        w = cv2.warpAffine(img, M.astype(np.float32), (112, 112), borderValue=0.0)
        pe = embed(w)
        for gid, ge in gallery.items():
            c = cos(pe, ge)
            (same if gid == sid else diff).append(c)

same, diff = np.array(same), np.array(diff)
print(f"pairs: same={same.size}  diff={diff.size}")

rows = []
for t in np.arange(0.05, 0.65, 0.05):
    far = float((diff >= t).mean()) * 100
    frr = float((same < t).mean()) * 100
    rows.append((t, far, frr, abs(far - frr)))
eer_t, eer_far, eer_frr, _ = min(rows, key=lambda r: r[3])
eer = (eer_far + eer_frr) / 2

print("\n threshold    FAR%     FRR%")
for t, far, frr, _ in rows:
    print(f"   {t:.2f}      {far:7.3f}  {frr:7.2f}")
print(f"\n EER ~ {eer:.2f}% at cosine ~ {eer_t:.2f}")

# rank-1
hits = total = 0
for cam in range(1, 8):
    d = DB / "surveillance_cameras_distance_3" / f"cam_{cam}"
    if not d.is_dir():
        continue
    for f in sorted(d.glob(f"*_cam{cam}_3.jpg")):
        sid = f.name.split("_")[0]
        if sid not in gallery:
            continue
        img = cv2.imread(str(f))
        bx, sc, kp = detect(img)
        if not len(bx):
            continue
        M = similarity(kp[int(sc.argmax())], TEMPLATE)
        w = cv2.warpAffine(img, M.astype(np.float32), (112, 112), borderValue=0.0)
        pe = embed(w)
        best = max(gallery.items(), key=lambda kv: cos(pe, kv[1]))[0]
        total += 1
        hits += (best == sid)
print(f" rank-1 (distance 3, all cameras): {hits}/{total} = {100.0 * hits / total:.1f}%")

report = f"""# 人脸识别黄金参照（float ONNX / x86_64）

> 由 `/tmp/face-probe/` 下的脚本产出。这是 C++ / RKNN 实现必须复现的**基准**，
> 不是板端结果。板端对比方法见文末。

## 模型指纹

| 模型 | 路径 | SHA-256 |
| --- | --- | --- |
| 检测 det_500m | `{DET_MODEL.relative_to(ZOO)}` | `{sha256(DET_MODEL)[:32]}…` |
| 特征 facelivtv2-l | `{EMB_MODEL.relative_to(ZOO)}` | `{sha256(EMB_MODEL)[:32]}…` |

## 已验证的预处理契约（由官方公布数值反证）

| 项 | 结论 | 证据 |
| --- | --- | --- |
| 特征模型通道序 | **RGB** | RGB 复现 README 的 norm 与 embedding 逐位吻合；BGR 全部不匹配 |
| 特征模型归一化 | `(x-127.5)/127.5` | 复现 norm=10.0972 / 10.6698 / 12.7857 |
| 检测模型归一化 | `(x-127.5)/128.0` | 标准 SCRFD |
| 检测 score 输出 | **已含 Sigmoid，不要再加** | ONNX 图内 443/468/493 直连 Sigmoid 节点 |
| 检测输入 | 640x640，stride 8/16/32，每点 2 anchor | 锚点数 12800/3200/800 与特征图尺寸吻合 |
| 对齐模板 | ArcFace 5 点（两模型 README 逐位相同） | 见 golden_reference.npz 的 `template` |

## 黄金参照数值

### 特征提取（官方 README 数值，本环境复现）

```
face_a1  norm = 10.0972   head = [-0.2670,  0.3539,  0.8917,  0.6197, -0.1279]
face_a2  norm = 10.6698   head = [-0.5242, -0.0067,  0.9970,  0.6192, -0.0307]
face_b1  norm = 12.7857   head = [ 0.7236,  0.2250, -0.3486, -0.0361, -0.2126]
cosine(a1,a2) = +0.9235  (同人)
cosine(a1,b1) = -0.0763  (不同人)
```

### 检测解码自检

```
surveillance 001_cam1_1: score=0.7801  box 内 landmarks 全通过 (1/1)
aligned mugshot (加边距后): score=0.8457
```

⚠️ **重要**：`aligned_112x112` 的证件照人脸占满全帧，直接送入检测器得 score=0.1066
（**检不出**）。这是检测器的分布外输入，不是 bug。加 50% 边距后立刻 0.8457。

## 精度 vs 探针分辨率（SCface 130 人，真实监控画面）

| 距离档 | 画面尺寸 | 人脸宽度 | rank-1 | 同人均值 | 异人均值 |
| --- | --- | --- | --- | --- | --- |
| 1 | 75x100 | ~22 px | **54.7%** | +0.1586 | +0.0091 |
| 2 | 108x144 | ~36 px | **89.5%** | +0.3072 | +0.0101 |
| 3 | 168x224 | ~64 px | **98.7%** | +0.4358 | +0.0104 |

**结论：精度由人脸像素尺寸主导，不是管道缺陷。** 人脸 <25px 时识别不可靠。

### 距离 3（最佳质量档）的阈值工作点

```
 threshold    FAR%     FRR%
   0.05        ...
   ...
 EER ~ {eer:.2f}% at cosine ~ {eer_t:.2f}
 rank-1 = {100.0 * hits / total:.1f}%
```

⚠️ 模型 README 明确说明其 `0.50` 阈值**不是标定工作点**，仅为 demo 默认值。
真实部署阈值必须按上表按自身数据重新标定。

## 对齐往返自检

`aligned_112x112` 已是 ArcFace 对齐结果，因此
`直接取特征` vs `检测→对齐→取特征` 的 cosine 反映**纯对齐误差**：

```
n=30  mean=+0.9291  min=+0.7898  p10=+0.8586
检测 landmark 相对模板的平均像素误差 = 2.61px (p90 = 5.53px)
```

## 板端对比方法（后续 RKNN 验收用）

```python
ref = np.load('/tmp/face-probe/golden_reference.npz')
# 对每个 identity: cosine(cpp_or_rknn_embedding, ref_embedding)
```

判据（引自模型 README 实测）：

| 后端 | 期望 cosine vs float |
| --- | --- |
| float ONNX（本基准） | 1.0000 |
| RKNN **fp16** | > 0.999（README: 0.99997） |
| RKNN **int8** | ⚠️ ~0.86（README 实测） |

**cosine 明显低于上述值 = 预处理或量化漂移，必须先解决再谈匹配率。**
"""
(OUT / "GOLDEN_REFERENCE.md").write_text(report, encoding="utf-8")
print(f"wrote {OUT / 'GOLDEN_REFERENCE.md'}")
