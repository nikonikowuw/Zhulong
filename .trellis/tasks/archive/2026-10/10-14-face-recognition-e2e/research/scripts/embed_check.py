# NOTE: requires RKNN_MODEL_ZOO (default ~/dev/rknn_model_zoo) to point at a
# checkout of github.com/airockchip/rknn_model_zoo that still carries the
# SCface dataset and the two .onnx models. See face-model-baseline.md.
#!/usr/bin/env python3
"""Golden-reference check for facelivtv2-l.onnx (face embedding / recognition).

Why this exists
---------------
Before wiring ONNX Runtime into the C++ engine we must be able to reproduce the
numbers published in the model README. If we cannot, our preprocessing (channel
order, normalization, layout) is wrong, and every downstream similarity number
would be meaningless -- silently wrong, not loudly broken.

Documented float-ONNX values (README section 8):
  face_a1  norm = 10.0972   embedding[0:5] = [-0.2670,  0.3539,  0.8917,  0.6197, -0.1279]
  face_a2  norm = 10.6698   embedding[0:5] = [-0.5242, -0.0067,  0.9970,  0.6192, -0.0307]
  face_b1  norm = 12.7857   embedding[0:5] = [ 0.7236,  0.2250, -0.3486, -0.0361, -0.2126]
  cosine(face_a1, face_a2) =  0.9235   (same person)
  cosine(face_a1, face_b1) = -0.0763   (different person)

This script tries both RGB and BGR channel orders and reports which one
reproduces the published values, so the channel order is established by
evidence rather than assumption.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

import cv2
import numpy as np
import onnxruntime as ort

BASE = Path(os.environ.get("RKNN_MODEL_ZOO", "~/dev/rknn_model_zoo")).expanduser() / "examples/facelivt/facelivtv2_l"
MODEL = BASE / "model" / "facelivtv2-l.onnx"
SAMPLES = {
    "face_a1": BASE / "model" / "face_a1.jpg",
    "face_a2": BASE / "model" / "face_a2.jpg",
    "face_b1": BASE / "model" / "face_b1.jpg",
}

EXPECTED_NORM = {"face_a1": 10.0972, "face_a2": 10.6698, "face_b1": 12.7857}
EXPECTED_HEAD = {
    "face_a1": [-0.2670, 0.3539, 0.8917, 0.6197, -0.1279],
    "face_a2": [-0.5242, -0.0067, 0.9970, 0.6192, -0.0307],
    "face_b1": [0.7236, 0.2250, -0.3486, -0.0361, -0.2126],
}
EXPECTED_COS_SAME = 0.9235
EXPECTED_COS_DIFF = -0.0763

MEAN = 127.5
STD = 127.5
SIZE = 112


def preprocess(path: Path, channel_order: str) -> np.ndarray:
    image = cv2.imread(str(path), cv2.IMREAD_COLOR)
    if image is None:
        raise SystemExit(f"cannot read {path}")
    if channel_order == "RGB":
        image = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)
    image = cv2.resize(image, (SIZE, SIZE), interpolation=cv2.INTER_LINEAR)
    x = image.astype(np.float32)
    x = (x - MEAN) / STD
    x = np.transpose(x, (2, 0, 1))[None, ...]
    return np.ascontiguousarray(x)


def l2_normalize(vec: np.ndarray) -> np.ndarray:
    flat = np.asarray(vec, dtype=np.float32).reshape(-1)
    norm = float(np.linalg.norm(flat))
    return flat / norm if norm else flat


def main() -> int:
    print("=" * 72)
    print(f"model : {MODEL}")
    print(f"exists: {MODEL.is_file()}")
    print("=" * 72)

    session = ort.InferenceSession(str(MODEL), providers=["CPUExecutionProvider"])
    inp = session.get_inputs()[0]
    print(f"input : name={inp.name!r} shape={inp.shape} type={inp.type}")
    for out in session.get_outputs():
        print(f"output: name={out.name!r} shape={out.shape} type={out.type}")
    print()

    verdicts: dict[str, bool] = {}
    for order in ("RGB", "BGR"):
        print("-" * 72)
        print(f"channel order = {order}")
        print("-" * 72)
        embeddings: dict[str, np.ndarray] = {}
        for name, path in SAMPLES.items():
            x = preprocess(path, order)
            y = session.run(None, {inp.name: x})[0]
            embeddings[name] = y
            flat = np.asarray(y, dtype=np.float32).reshape(-1)
            norm = float(np.linalg.norm(flat))
            head = [round(float(v), 4) for v in flat[:5]]
            exp_norm = EXPECTED_NORM[name]
            exp_head = EXPECTED_HEAD[name]
            head_ok = all(abs(a - b) < 2e-3 for a, b in zip(head, exp_head))
            print(f"  {name}: norm={norm:.4f} (expect {exp_norm:.4f})  head={head}")
            print(f"          head matches documented: {head_ok}")

        cos_same = float(
            np.dot(l2_normalize(embeddings["face_a1"]), l2_normalize(embeddings["face_a2"]))
        )
        cos_diff = float(
            np.dot(l2_normalize(embeddings["face_a1"]), l2_normalize(embeddings["face_b1"]))
        )
        print(f"  cosine(a1,a2) = {cos_same:+.4f}  (documented {EXPECTED_COS_SAME:+.4f})")
        print(f"  cosine(a1,b1) = {cos_diff:+.4f}  (documented {EXPECTED_COS_DIFF:+.4f})")
        ok = (
            abs(cos_same - EXPECTED_COS_SAME) < 2e-3
            and abs(cos_diff - EXPECTED_COS_DIFF) < 2e-3
        )
        print(f"  => reproduces documented values: {ok}")
        verdicts[order] = ok
        print()

    print("=" * 72)
    for order, ok in verdicts.items():
        print(f"{order}: {'PASS' if ok else 'FAIL'}")
    winners = [o for o, ok in verdicts.items() if ok]
    if len(winners) == 1:
        print(f"\nGOLDEN REFERENCE ESTABLISHED: channel order = {winners[0]}")
        return 0
    if not winners:
        print("\nNEITHER channel order reproduced the documented values -- preprocessing is wrong")
        return 1
    print("\nAMBIGUOUS: both channel orders matched (unexpected)")
    return 2


if __name__ == "__main__":
    sys.exit(main())
