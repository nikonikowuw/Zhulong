#!/usr/bin/env python3
"""
build_package.py - 组装 PPE 安全帽检测算法包并做交付前检查

用法:
    python3 packages/ppe_helmet/scripts/build_package.py --target onnx
    python3 packages/ppe_helmet/scripts/build_package.py --target rk3568 \
        --toolchain build/native/cross-rk3568-rknn-rga/toolchain.cmake

默认从仓库的 build/sdk/prefix（onnx）或 build/sdk/rk3568（rk3568）取 SDK 前缀；
交叉构建默认从 native/vendor/rknpu2、native/vendor/rga 取厂商运行库。
"""

import argparse
import json
import os
import platform
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path


def resolve_path(value: Path, base: Path) -> Path:
    return value if value.is_absolute() else (base / value).resolve()


def main() -> None:
    parser = argparse.ArgumentParser(description="组装 PPE 安全帽检测算法包")
    parser.add_argument("--target", default="onnx", choices=("onnx", "rk3568"))
    parser.add_argument("--output-dir", default="build/packages")
    parser.add_argument("--sdk-prefix")
    parser.add_argument("--toolchain")
    parser.add_argument("--rknn-root")
    parser.add_argument("--rga-root")
    parser.add_argument("--skip-build", action="store_true",
                        help="复用 packages/ppe_helmet/<target>/build/ppe_helmet")
    parser.add_argument("--no-verify", action="store_true",
                        help="跳过本机黑盒验包（交叉构建会自动跳过）")
    args = parser.parse_args()

    pkg_root = Path(__file__).resolve().parent.parent
    repo_root = None
    for parent in [pkg_root, *pkg_root.parents]:
        if (parent / "cmd" / "Zhulong").exists() or (parent / "native" / "CMakeLists.txt").exists():
            repo_root = parent
            break
    if repo_root is None:
        sys.exit("错误: 未找到 Zhulong 仓库根目录（需要 cmd/Zhulong 或 native/CMakeLists.txt）")

    target_dir = pkg_root / args.target
    manifest_path = target_dir / "manifest.json"
    if not manifest_path.is_file():
        sys.exit(f"错误: 缺少 manifest.json: {manifest_path}")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    pkg_id = manifest["id"]
    arch = manifest["platform"]["arch"]
    target_name = manifest["platform"]["target"]
    version = manifest["version"]

    # ---- SDK 前缀与后端校验 -------------------------------------------------
    if args.sdk_prefix:
        sdk_prefix = resolve_path(Path(args.sdk_prefix), Path.cwd())
    else:
        sdk_prefix = repo_root / "build" / "sdk" / ("rk3568" if args.target == "rk3568" else "prefix")
    sdk_prefix = sdk_prefix.resolve()
    if not (sdk_prefix / "lib" / "cmake" / "ZhulongAlgoSDK" / "ZhulongAlgoSDKConfig.cmake").exists():
        hint = "make sdk-cross-install" if args.target == "rk3568" else "make sdk-install"
        sys.exit(f"错误: 未找到 SDK 前缀 {sdk_prefix}\n请先执行 {hint}，或用 --sdk-prefix 指定")

    expected_backend = "rknn" if args.target == "rk3568" else "onnx"
    version_file = sdk_prefix / "VERSION"
    if version_file.exists():
        for line in version_file.read_text(encoding="utf-8", errors="ignore").splitlines():
            if line.startswith("backend="):
                backend = line.split("=", 1)[1].strip()
                if backend and backend != expected_backend:
                    sys.exit(f"错误: {args.target} 需要 {expected_backend} 后端，"
                             f"但 {sdk_prefix} 的后端是 {backend}")
                break

    # ---- 交叉工具链与厂商库 -------------------------------------------------
    is_cross = arch == "aarch64" and platform.machine() not in ("aarch64", "arm64")
    toolchain = None
    if args.toolchain:
        toolchain = resolve_path(Path(args.toolchain), Path.cwd())
    elif is_cross:
        candidate = repo_root / "build" / "native" / "cross-rk3568-rknn-rga" / "toolchain.cmake"
        if not candidate.exists():
            sys.exit(f"错误: 交叉构建需要工具链，请用 --toolchain 指定（未找到 {candidate}）")
        toolchain = candidate

    rknn_root = None
    rga_root = None
    if args.target == "rk3568":
        rknn_root = resolve_path(Path(args.rknn_root), Path.cwd()) if args.rknn_root \
            else repo_root / "native" / "vendor" / "rknpu2"
        rga_root = resolve_path(Path(args.rga_root), Path.cwd()) if args.rga_root \
            else repo_root / "native" / "vendor" / "rga"
        if not (rknn_root / "lib" / "librknnrt.so").exists():
            sys.exit(f"错误: 未找到 {rknn_root}/lib/librknnrt.so，请用 --rknn-root 指定")
        if not (rga_root / "lib" / "librga.so.2").exists():
            sys.exit(f"错误: 未找到 {rga_root}/lib/librga.so.2，请用 --rga-root 指定")

    if is_cross and not args.no_verify:
        print("[*] 交叉构建：跳过本机验包（aarch64 产物在宿主不可执行）")
        args.no_verify = True

    # ---- 构建包入口 ---------------------------------------------------------
    exe_path = target_dir / "build" / pkg_id
    if not args.skip_build:
        make_args = ["make", "-C", str(target_dir), f"SDK_PREFIX={sdk_prefix}"]
        if toolchain:
            make_args.append(f"TOOLCHAIN={toolchain}")
        if rknn_root:
            make_args.append(f"RKNN_ROOT={rknn_root}")
        if rga_root:
            make_args.append(f"RGA_ROOT={rga_root}")
        print(f"[*] 构建 {args.target} 包入口（SDK 前缀 {sdk_prefix}）...")
        if subprocess.run(make_args).returncode != 0:
            sys.exit("错误: 包入口构建失败")
    if not exe_path.is_file():
        sys.exit(f"错误: 缺少包入口 {exe_path}（去掉 --skip-build 或先 make）")

    # ---- 组装扁平交付目录 ---------------------------------------------------
    output_dir = resolve_path(Path(args.output_dir), repo_root)
    staging = output_dir / "staging" / f"{pkg_id}_{target_name}"
    if staging.exists():
        shutil.rmtree(staging)
    staging.mkdir(parents=True)

    shutil.copy2(manifest_path, staging / "manifest.json")
    entry = staging / pkg_id
    shutil.copy2(exe_path, entry)
    os.chmod(entry, 0o755)

    symbol_check = repo_root / "native" / "tests" / "check_plugin_symbols.py"
    if symbol_check.exists() and shutil.which("nm"):
        result = subprocess.run([sys.executable, str(symbol_check), "--entry", str(entry)])
        if result.returncode != 0:
            sys.exit("错误: 包入口仍导出插件 C ABI 符号（交付形态不得再暴露 dlopen 入口）")

    for name in ("sample.jpg", "config.schema.json"):
        src = target_dir / name
        if not src.is_file():
            sys.exit(f"错误: 缺少 {src}")
        shutil.copy2(src, staging / name)
    models = target_dir / "models"
    if not models.is_dir():
        sys.exit(f"错误: 缺少模型目录 {models}")
    shutil.copytree(models, staging / "models")

    # ---- 本机黑盒验包（仅 onnx，且非交叉） ----------------------------------
    if not args.no_verify:
        sample = staging / "sample.jpg"
        proc = subprocess.run(
            [str(entry), "--verify", "--sample", str(sample)],
            capture_output=True, text=True, cwd=str(staging))
        if proc.returncode != 0:
            print(proc.stderr, file=sys.stderr)
            sys.exit(f"错误: 黑盒验包未通过（退出码 {proc.returncode}）")
        report = proc.stdout.strip().splitlines()[-1] if proc.stdout.strip() else "{}"
        print(f"[✓] 黑盒验包（ABI 生命周期）通过: {report}")
        if '"sample_decoded":false' in report:
            print("[!] --verify 只解 netpbm；真实检出由 devtest 校验", file=sys.stderr)

        # --verify 的骨架只解 PPM/PGM，用 devtest 读真实 JPEG 验证检出。
        devtest = target_dir / "build" / f"{pkg_id}_devtest"
        if not devtest.is_file():
            subprocess.run(["cmake", "--build", str(target_dir / "build"),
                            "--target", f"{pkg_id}_devtest"], check=False)
        if devtest.is_file():
            out = Path("/tmp") / f"{pkg_id}_package_verify.jpg"
            env = dict(os.environ, ZHULONG_PPE_MODEL_DIR=str(staging))
            dt = subprocess.run(
                [str(devtest), "--sample", str(sample), "--out", str(out)],
                capture_output=True, text=True, cwd=str(staging), env=env)
            detected = 0
            for line in dt.stdout.splitlines():
                parts = line.split(":")
                if len(parts) == 2 and parts[0].strip() == "count":
                    try:
                        detected = int(parts[1].strip())
                    except ValueError:
                        detected = 0
            if dt.returncode != 0 or detected < 1:
                print(dt.stdout, file=sys.stderr)
                print(dt.stderr, file=sys.stderr)
                sys.exit("错误: devtest 未在 sample.jpg 上检出违规，拒绝打包")
            print(f"[✓] devtest 检出 {detected} 个违规目标（sample.jpg）")
        else:
            print("[!] 警告: 未找到 devtest，跳过真实检出校验", file=sys.stderr)

    # ---- 打包 ---------------------------------------------------------------
    output_dir.mkdir(parents=True, exist_ok=True)
    archive = output_dir / f"{pkg_id}-{arch}-{target_name}.tar.gz"
    with tarfile.open(archive, "w:gz") as tar:
        for item in sorted(staging.iterdir()):
            tar.add(item, arcname=item.name)
    if args.target == "onnx":
        shutil.copy2(archive, output_dir / f"{pkg_id}.tar.gz")

    names = sorted(p.name for p in staging.iterdir())
    print(f"[✓] 算法包: {archive} ({archive.stat().st_size} 字节)")
    print(f"    版本: {version} / 平台: {arch}-{target_name}")
    print(f"    根目录: {', '.join(names)}")


if __name__ == "__main__":
    main()
