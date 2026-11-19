#!/usr/bin/env python3
"""把 rknn_model_zoo 的 scrfd_person_2.5g ONNX 转成 RK3568 静态形状 `.rknn`。

为什么必须在这里固定分辨率：zoo 的 `scrfd_person_2.5g.onnx` 输入是动态的 `[1,3,?,?]`，
而 Zhulong 的 RKNN 后端按 `element_count` 校验输入（`native/src/backends/rknn/rknn_inference_backend.cpp`），
静态 `.rknn` 与运行期输入尺寸不一致时 `forward` 直接失败、表现为静默零检出。所以转换时
就把形状钉成 640×384（16:9 CCTV 档位），运行期 `PersonDetector::set_input_size` 只放行这一组。

归一化（`(x-127.5)/128`，RGB 顺序）在 `rknn.config()` 里配置，由工具链写进模型、NPU 内执行；
应用侧只负责 letterbox（等比缩放 + 114 灰边，居中）并喂 HWC uint8。

用法：

    # 1) 生成 INT8 校准集（把任意图片按运行期前处理做成 640×384，再交给工具链）
    python3 scripts/convert_scrfd_person.py dataset --images ../../../datasets/COCO/subset \\
        --out-dir build/calib-640x384

    # 2) 转换（int8 需要 --dataset；fp 不带量化，走 FP16）
    python3 scripts/convert_scrfd_person.py build --quant int8 --dataset build/calib-640x384/dataset.txt
    python3 scripts/convert_scrfd_person.py build --quant fp

输出默认写到 `models/scrfd_person_2.5g.rknn`，并打印 ONNX / RKNN 的 SHA-256 与工具链版本
（`QUANTIZATION.md` 里的证据就抄这一屏）。
"""

from __future__ import annotations

import argparse
import hashlib
import sys
from pathlib import Path

DEFAULT_TARGET_PLATFORM = "rk3568"
# 与 src/detection/person_detector.hpp 的 kStaticModelWidth / kStaticModelHeight 必须一致
DEFAULT_WIDTH = 640
DEFAULT_HEIGHT = 384
ONNX_INPUT_NAME = "input.1"
# 与 native/src/algorithm/runtime/letterbox_target.hpp 的 letterbox_pad_value 一致
LETTERBOX_PAD_VALUE = 114
# 与 person_detector.cpp 的 kDetectMean / kDetectInvStd 一致（(x - 127.5) / 128）
MEAN = 127.5
STD = 128.0

SCRIPT_DIR = Path(__file__).resolve().parent
TARGET_DIR = SCRIPT_DIR.parent
PACKAGE_DIR = TARGET_DIR.parent
DEFAULT_ONNX = PACKAGE_DIR / "onnx" / "models" / "scrfd_person_2.5g.onnx"
DEFAULT_RKNN = TARGET_DIR / "models" / "scrfd_person_2.5g.rknn"
IMAGE_SUFFIXES = {".jpg", ".jpeg", ".png", ".bmp"}


def sha256_of(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def letterbox_image(image, width: int, height: int):
    """按运行期语义做 letterbox：等比缩放、居中、114 灰边。

    校准图必须与推理时的前处理逐像素同构，否则量化统计的是另一套分布 —— 模型在板上
    只会「看起来跑了、分数全错」。这里不引入额外依赖，缩放用 cv2（工具链本身也依赖它）。
    """
    import cv2
    import numpy as np

    src_h, src_w = image.shape[:2]
    scale = min(width / float(src_w), height / float(src_h))
    new_w = max(1, int(round(src_w * scale)))
    new_h = max(1, int(round(src_h * scale)))
    resized = cv2.resize(image, (new_w, new_h), interpolation=cv2.INTER_LINEAR)
    canvas = np.full((height, width, image.shape[2]), LETTERBOX_PAD_VALUE, dtype=image.dtype)
    left = (width - new_w) // 2
    top = (height - new_h) // 2
    canvas[top:top + new_h, left:left + new_w] = resized
    return canvas


def cmd_dataset(args: argparse.Namespace) -> int:
    import cv2

    out_dir = Path(args.out_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    # 目录展开成图片列表（可混用「目录 + 单张」）；保持用户给出的顺序
    sources: list[Path] = []
    for raw in args.images:
        candidate = Path(raw).resolve()
        if candidate.is_dir():
            sources.extend(sorted(p for p in candidate.iterdir() if p.suffix.lower() in IMAGE_SUFFIXES))
        else:
            sources.append(candidate)
    if not sources:
        print("错误: 没有找到任何输入图片", file=sys.stderr)
        return 2

    list_path = out_dir / "dataset.txt"
    written: list[Path] = []
    for index, src in enumerate(sources):
        image = cv2.imread(str(src), cv2.IMREAD_COLOR)  # BGR
        if image is None:
            print(f"警告: 读不出图片，跳过: {src}", file=sys.stderr)
            continue
        image = cv2.cvtColor(image, cv2.COLOR_BGR2RGB)  # 模型吃 RGB
        prepared = letterbox_image(image, args.width, args.height)
        dst = out_dir / f"calib_{index:03d}.png"  # PNG：不引入 JPEG 压缩噪声
        cv2.imwrite(str(dst), cv2.cvtColor(prepared, cv2.COLOR_RGB2BGR))
        written.append(dst)

    if not written:
        print("错误: 没有任何图片转换成功", file=sys.stderr)
        return 2

    list_path.write_text("".join(f"{p}\n" for p in written), encoding="utf-8")
    print(f"校准集: {len(written)} 张 {args.width}x{args.height}（letterbox {LETTERBOX_PAD_VALUE} 灰边）")
    print(f"清单  : {list_path}")
    return 0


def cmd_build(args: argparse.Namespace) -> int:
    from rknn.api import RKNN

    onnx_path = Path(args.onnx).resolve()
    out_path = Path(args.out).resolve()
    if not onnx_path.is_file():
        print(f"错误: ONNX 不存在: {onnx_path}", file=sys.stderr)
        return 2
    dataset_path = None
    if args.quant == "int8":
        if not args.dataset:
            print("错误: int8 量化必须给 --dataset（用 `dataset` 子命令生成）", file=sys.stderr)
            return 2
        dataset_path = Path(args.dataset).resolve()
        if not dataset_path.is_file():
            print(f"错误: 校准清单不存在: {dataset_path}", file=sys.stderr)
            return 2

    try:
        import rknn
        toolkit_version = getattr(rknn.api, "__version__", None) or "unknown"
    except Exception:  # pragma: no cover - 只在装坏的 venv 里发生
        toolkit_version = "unknown"

    print("=== conversion input ===")
    print(f"onnx          : {onnx_path}")
    print(f"onnx sha256   : {sha256_of(onnx_path)}")
    print(f"toolkit       : rknn-toolkit2 {toolkit_version}")
    print(f"target        : {args.target_platform}")
    print(f"input shape   : [1, 3, {args.height}, {args.width}]")
    print(f"quantization  : {args.quant}")
    if dataset_path is not None:
        images = [line for line in dataset_path.read_text(encoding="utf-8").splitlines() if line.strip()]
        print(f"calibration   : {dataset_path} ({len(images)} images)")

    rknn_model = RKNN(verbose=args.verbose)
    try:
        rknn_model.config(
            mean_values=[[MEAN, MEAN, MEAN]],
            std_values=[[STD, STD, STD]],
            target_platform=args.target_platform,
        )
        # 动态 ONNX 必须在 load_onnx 里钉死形状，否则工具链无法确定静态权重的 shape。
        if rknn_model.load_onnx(
            model=str(onnx_path),
            inputs=[ONNX_INPUT_NAME],
            input_size_list=[[1, 3, args.height, args.width]],
        ) != 0:
            print("错误: load_onnx 失败", file=sys.stderr)
            return 1
        if rknn_model.build(do_quantization=(args.quant == "int8"), dataset=str(dataset_path) if dataset_path else None) != 0:
            print("错误: build 失败", file=sys.stderr)
            return 1
        out_path.parent.mkdir(parents=True, exist_ok=True)
        if rknn_model.export_rknn(str(out_path)) != 0:
            print("错误: export_rknn 失败", file=sys.stderr)
            return 1
    finally:
        rknn_model.release()

    print("=== conversion output ===")
    print(f"rknn          : {out_path}")
    print(f"rknn sha256   : {sha256_of(out_path)}")
    print(f"rknn size     : {out_path.stat().st_size} bytes")
    print(
        "提示: 真实精度以板上 `make test` 的检出表为准（x86 宿主上的 `init_runtime` "
        "需要 x86 librknnrt，本仓库不提供）。"
    )
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--verbose", action="store_true", help="工具链详细日志")
    subparsers = parser.add_subparsers(dest="command", required=True)

    build = subparsers.add_parser("build", help="ONNX -> 静态 RKNN")
    build.add_argument("--onnx", default=str(DEFAULT_ONNX))
    build.add_argument("--out", default=str(DEFAULT_RKNN))
    build.add_argument("--target-platform", default=DEFAULT_TARGET_PLATFORM)
    build.add_argument("--width", type=int, default=DEFAULT_WIDTH)
    build.add_argument("--height", type=int, default=DEFAULT_HEIGHT)
    build.add_argument("--quant", choices=("fp", "int8"), default="int8")
    build.add_argument("--dataset", default=None, help="INT8 校准清单（dataset.txt）")
    build.set_defaults(func=cmd_build)

    dataset = subparsers.add_parser("dataset", help="生成与运行期前处理同构的 INT8 校准集")
    dataset.add_argument("--images", nargs="+", required=True, help="图片文件或目录")
    dataset.add_argument("--out-dir", required=True)
    dataset.add_argument("--width", type=int, default=DEFAULT_WIDTH)
    dataset.add_argument("--height", type=int, default=DEFAULT_HEIGHT)
    dataset.set_defaults(func=cmd_dataset)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
