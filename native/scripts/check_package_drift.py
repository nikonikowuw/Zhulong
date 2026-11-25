#!/usr/bin/env python3
"""
check_package_drift.py - 多 target 算法包的源码与元数据漂移检查

一个包按 target 分家（`packages/<id>/<target>/`）时，每个档位是一份**自包含**的包副本，
差异应当只有「后端决定的那几行」。现实是同一处逻辑修复常常只落到一个档位，另一份悄悄
变成孤儿——而两份都能编译通过，问题只在板端表现为「这台机器结果不对」。

用法:
    python3 native/scripts/check_package_drift.py packages/face_recognition
    python3 native/scripts/check_package_drift.py packages/face_recognition --list-allowed

判据:
    - 任一子目录存在畸形 `manifest.json`（无法读取、非 JSON 对象或缺少 `platform.target`）
      一律 fail-closed（退出码 1），绝不跳过该 target 伪装通过；
    - 比较范围包含 `src/**`、`tests/**`、`config.schema.json`（逐字节比对）以及
      `manifest.json` 共享元数据字段（`id` / `name` / `version` / `task_type` / `category`）；
    - 出现在 ALLOWED_DIFFERENCES 里的相对路径允许内容不同，但**必须两边都存在且确有差异**
      （不存在或已完全一致的陈旧白名单条目也会报错）；
    - 未登记的差异 = 漂移，退出码 1，并打印两侧统一的 diff。
"""

import argparse
import difflib
import json
import sys
from pathlib import Path
from typing import Any

# 允许按 target 不同的文件：相对包目录的路径 → 允许不同的理由。
# ⚠️ 新增条目等于宣布「这段逻辑允许两份实现」，请在 PR 里写清为什么不能用同一份。
ALLOWED_DIFFERENCES = {
    "face_recognition": {
        "src/detection/detector.cpp": "前处理不同：ONNX 需 HWC→NCHW float32，RKNN 模型自带归一化",
        "src/quality/quality_assessor.cpp": "前处理不同：同上（质量评估输入）",
        "src/feature/extractor.cpp": "前处理不同：同上（特征提取输入）",
        "src/attribute/person_attribute.cpp": "前处理不同：ONNX 需 HWC→NCHW float32 + mean/std，RKNN 模型自带归一化",
        "src/pipeline/face_recognition.cpp": "权重扩展名与开发期源码树前缀按 target 不同",
    },
    "crowd_gathering": {
        "config.schema.json": "配置项不同：ONNX 暴露 detect_input_width/height，RKNN 权重为静态 640×384 不暴露分辨率调参",
        "src/detection/person_detector.cpp": "前处理不同：ONNX 需 HWC→NCHW float32 + mean/std 查表，RKNN 模型自带归一化、直接喂 HWC uint8",
        "src/detection/person_detector.hpp": "工作区与尺寸校验不同：ONNX 是动态输入，RKNN 权重为静态形状、只接受转换时固定的 640×384",
        "src/pipeline/crowd_gathering.cpp": "权重扩展名与开发期源码树前缀按 target 不同",
    },
}

# 允许只存在于部分 target 的文件（缺侧不算漂移）。
ALLOWED_ONE_SIDED = {
    "face_recognition": {
        "src/common/face_preprocess.hpp": "仅「模型不自带归一化」的档位需要该前处理助手",
    },
}

COMPARE_DIRECTORIES = ("src", "tests")
COMPARE_FILES = ("config.schema.json",)
SHARED_MANIFEST_FIELDS = ("id", "name", "version", "task_type", "category")


class ManifestError(ValueError):
    """子目录 manifest.json 损坏或缺少必需的 platform.target 字段。"""


def load_targets(package_dir: Path) -> list[tuple[Path, dict[str, Any]]]:
    """返回该包下所有档位目录及其解析后的 manifest.json；遇到畸形 manifest 直接抛出异常（fail-closed）。"""
    targets: list[tuple[Path, dict[str, Any]]] = []
    for candidate in sorted(package_dir.iterdir()):
        if not candidate.is_dir():
            continue
        manifest = candidate / "manifest.json"
        if not manifest.exists() and not manifest.is_symlink():
            continue
        try:
            data = json.loads(manifest.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
            raise ManifestError(f"无法解析 manifest: {manifest}: {error}") from error
        if not isinstance(data, dict):
            raise ManifestError(f"manifest 顶层必须是 JSON 对象: {manifest}")
        platform = data.get("platform")
        target = platform.get("target") if isinstance(platform, dict) else None
        if not isinstance(target, str) or not target.strip():
            raise ManifestError(f"manifest 缺少非空的 platform.target: {manifest}")
        targets.append((candidate, data))
    return targets


def target_directories(package_dir: Path) -> list[Path]:
    """返回该包下所有档位目录（含合法 manifest.json 且带 platform.target 的子目录）。"""
    return [target_dir for target_dir, _ in load_targets(package_dir)]


def collect_files(target_dir: Path) -> dict[str, Path]:
    files: dict[str, Path] = {}
    for filename in COMPARE_FILES:
        file_path = target_dir / filename
        if file_path.is_file():
            files[filename] = file_path
    for directory in COMPARE_DIRECTORIES:
        root = target_dir / directory
        if not root.is_dir():
            continue
        for path in sorted(root.rglob("*")):
            if path.is_file():
                files[path.relative_to(target_dir).as_posix()] = path
    return files


def check_package(package_dir: Path, *, list_allowed: bool = False) -> int:
    if not package_dir.is_dir():
        print(f"错误: 包目录不存在: {package_dir}", file=sys.stderr)
        return 1

    package_id = package_dir.name
    allowed_diff = ALLOWED_DIFFERENCES.get(package_id, {})
    allowed_one_sided = ALLOWED_ONE_SIDED.get(package_id, {})

    if list_allowed:
        print(f"{package_id} 允许按 target 不同的文件：")
        for path, reason in allowed_diff.items():
            print(f"  {path}: {reason}")
        for path, reason in allowed_one_sided.items():
            print(f"  {path}（单侧）: {reason}")
        return 0

    try:
        targets = load_targets(package_dir)
    except ManifestError as error:
        print(f"错误: {error}", file=sys.stderr)
        return 1

    if len(targets) < 2:
        only = targets[0][0].name if targets else "无"
        print(f"[✓] {package_id}: 只有一个 target（{only}），无需漂移检查")
        return 0

    reference, reference_manifest = targets[0]
    reference_files = collect_files(reference)
    other_targets = [(other, manifest, collect_files(other)) for other, manifest in targets[1:]]
    drift_found = False
    allowed_diff_saw_difference: set[str] = set()

    print(f"[*] {package_id}: 以 {reference.name} 为基准比较 {', '.join(t.name for t, _, _ in other_targets)}")

    for other, other_manifest, other_files in other_targets:
        for field in SHARED_MANIFEST_FIELDS:
            ref_val = reference_manifest.get(field)
            other_val = other_manifest.get(field)
            if ref_val != other_val:
                drift_found = True
                print(
                    f"\n[漂移] manifest.json 字段 '{field}' 不一致: "
                    f"{reference.name}={ref_val!r} vs {other.name}={other_val!r}"
                )

        for relative_path in sorted(set(reference_files) | set(other_files)):
            in_reference = relative_path in reference_files
            in_other = relative_path in other_files

            if in_reference != in_other:
                if relative_path in allowed_one_sided:
                    continue
                drift_found = True
                side = reference.name if in_reference else other.name
                print(f"\n[漂移] 文件只存在于 {side}: {relative_path}")
                continue

            reference_bytes = reference_files[relative_path].read_bytes()
            other_bytes = other_files[relative_path].read_bytes()

            if relative_path in allowed_diff:
                if reference_bytes != other_bytes:
                    allowed_diff_saw_difference.add(relative_path)
                continue

            if reference_bytes == other_bytes:
                continue

            drift_found = True
            print(f"\n[漂移] {relative_path}（未登记为允许差异）")
            reference_text = reference_bytes.decode("utf-8", errors="replace")
            other_text = other_bytes.decode("utf-8", errors="replace")
            diff_lines = list(
                difflib.unified_diff(
                    reference_text.splitlines(),
                    other_text.splitlines(),
                    fromfile=f"{reference.name}/{relative_path}",
                    tofile=f"{other.name}/{relative_path}",
                    lineterm="",
                )
            )
            if diff_lines:
                for index, line in enumerate(diff_lines):
                    if index >= 40:
                        print("  ...（diff 已截断）")
                        break
                    print(f"  {line}")
            else:
                print(f"  （字节内容不同，长度 {len(reference_bytes)} vs {len(other_bytes)}）")

    all_file_maps = [reference_files] + [other_files for _, _, other_files in other_targets]
    for relative_path in sorted(allowed_diff):
        present_count = sum(1 for files in all_file_maps if relative_path in files)
        if present_count == 0:
            drift_found = True
            print(f"\n[陈旧] ALLOWED_DIFFERENCES 条目在所有 target 中均不存在: {relative_path}")
        elif present_count == len(all_file_maps) and relative_path not in allowed_diff_saw_difference:
            drift_found = True
            print(f"\n[陈旧] ALLOWED_DIFFERENCES 条目在所有 target 中已完全一致，请从白名单移除: {relative_path}")

    for relative_path in sorted(allowed_one_sided):
        present_count = sum(1 for files in all_file_maps if relative_path in files)
        if present_count == 0:
            drift_found = True
            print(f"\n[陈旧] ALLOWED_ONE_SIDED 条目在所有 target 中均不存在: {relative_path}")
        elif present_count == len(all_file_maps):
            drift_found = True
            print(f"\n[陈旧] ALLOWED_ONE_SIDED 条目在所有 target 中均已存在，请从单侧白名单移除: {relative_path}")

    if drift_found:
        print(
            "\n错误: 检测到未登记的 target 间漂移或陈旧白名单条目。\n"
            "  要么把两边改成一致并清理陈旧白名单，要么在 check_package_drift.py 的 ALLOWED_DIFFERENCES 里\n"
            "  登记路径与理由（等于宣布这段逻辑必须有两份实现）。",
            file=sys.stderr,
        )
        return 1

    print(f"[✓] {package_id}: 各 target 源码与元数据一致（允许差异 {len(allowed_diff)} 项）")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="算法包多 target 源码漂移检查")
    parser.add_argument("package_dir", type=Path, help="包目录，如 packages/face_recognition")
    parser.add_argument("--list-allowed", action="store_true", help="只打印允许差异清单")
    args = parser.parse_args()
    return check_package(args.package_dir, list_allowed=args.list_allowed)


if __name__ == "__main__":
    sys.exit(main())
