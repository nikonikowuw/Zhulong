#!/usr/bin/env python3
"""
build_package.py - 人员非法聚集检测算法包自动化组装与校验脚本

用法:
    python3 packages/crowd_gathering/scripts/build_package.py [--target onnx] [--output-dir build/packages]
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

def main():
    parser = argparse.ArgumentParser(description="组装人员非法聚集检测算法包")
    parser.add_argument("--target", default="onnx", choices=["onnx", "rk3568", "rk3588"], help="目标平台 (默认: onnx)")
    parser.add_argument("--build-dir", default="build/native/host", help="C++ 构建产物目录 (默认: build/native/host)")
    parser.add_argument("--output-dir", default="build/packages", help="输出包目录 (默认: build/packages)")
    parser.add_argument("--no-verify", action="store_true", help="跳过验包验证")
    parser.add_argument("--sdk-prefix", default=None,
                        help="算法包 SDK 前缀（默认根据 target 自动解析）")
    parser.add_argument("--skip-build", action="store_true",
                        help="跳过包自身可执行文件的构建（复用 packages/<id>/<target>/build/<id>）")
    parser.add_argument("--toolchain", help="交叉构建用的 CMake 工具链文件")
    parser.add_argument("--rknn-root", help="RKNN 前缀")
    parser.add_argument("--onnxruntime-root", help="ONNX Runtime 前缀")
    args = parser.parse_args()

    pkg_root = Path(__file__).resolve().parent.parent

    repo_root = None
    for parent in [pkg_root] + list(pkg_root.parents):
        if (parent / "native" / "CMakeLists.txt").exists() or (parent / "cmd" / "Zhulong").exists():
            repo_root = parent
            break

    target_dir = pkg_root / args.target
    if not target_dir.exists():
        sys.exit(f"错误: 目标平台目录不存在: {target_dir}")

    manifest_path = target_dir / "manifest.json"
    if not manifest_path.exists():
        sys.exit(f"错误: 缺少 manifest.json: {manifest_path}")

    with open(manifest_path, "r", encoding="utf-8") as f:
        manifest = json.load(f)

    arch = manifest.get("platform", {}).get("arch", "unknown")
    target_name = manifest.get("platform", {}).get("target", args.target)
    pkg_id = manifest.get("id", "crowd_gathering")
    version = manifest.get("version", "1.0.0")

    # ---- SDK 前缀推导与校验 ----------------------------------------------------
    sdk_prefix_path = None
    if args.sdk_prefix:
        p = Path(args.sdk_prefix)
        if not p.is_absolute():
            if (Path.cwd() / p).exists():
                sdk_prefix_path = (Path.cwd() / p).resolve()
            elif repo_root and (repo_root / p).exists():
                sdk_prefix_path = (repo_root / p).resolve()
            else:
                sdk_prefix_path = (pkg_root / p).resolve()
        else:
            sdk_prefix_path = p.resolve()
    else:
        candidates = []
        if args.target in ("rk3568", "rk3588"):
            if repo_root:
                candidates.append(repo_root / "build" / "sdk" / args.target)
            candidates.extend([
                Path(f"/opt/zhulong-sdk-{args.target}"),
                Path("/opt/zhulong-sdk-aarch64"),
                Path("/opt/zhulong-sdk"),
            ])
        else:
            if repo_root:
                candidates.append(repo_root / "build" / "sdk" / "prefix")
            candidates.extend([
                Path("/opt/zhulong-sdk-onnx"),
                Path("/opt/zhulong-sdk-x86_64"),
                Path("/opt/zhulong-sdk"),
            ])
        for cand in candidates:
            if cand.exists() and (cand / "include" / "zhulong" / "algo.hpp").exists():
                sdk_prefix_path = cand.resolve()
                break

    if not sdk_prefix_path or not sdk_prefix_path.exists():
        if args.target in ("rk3568", "rk3588"):
            hint = f"在仓库根目录运行 'make sdk-cross-install' 生成 '{repo_root / 'build/sdk/rk3568'}'" if repo_root else "安装 aarch64 SDK 前缀"
            sys.exit(f"错误: 目标 '{args.target}' 需要 aarch64 SDK 前缀，但未找到有效目录。\n"
                     f"请先 {hint}，或通过 '--sdk-prefix <path>' 显式指定。")
        else:
            hint = f"在仓库根目录运行 'make sdk-install' 生成 '{repo_root / 'build/sdk/prefix'}'" if repo_root else "安装 host SDK 前缀"
            sys.exit(f"错误: 目标 '{args.target}' 需要 SDK 前缀，但未找到有效目录。\n"
                     f"请先 {hint}，或通过 '--sdk-prefix <path>' 显式指定。")

    version_file = sdk_prefix_path / "VERSION"
    if version_file.exists():
        backend = ""
        try:
            for line in version_file.read_text(encoding="utf-8").splitlines():
                if line.startswith("backend="):
                    backend = line.split("=", 1)[1].strip()
                    break
        except Exception:
            pass
        expected_backend = "rknn" if args.target in ("rk3568", "rk3588") else "onnx"
        if backend and backend != expected_backend:
            sys.exit(f"错误: 目标 '{args.target}' 要求 '{expected_backend}' 推理后端，\n"
                     f"但当前 SDK 前缀 ({sdk_prefix_path}) 的后端为 '{backend}'。\n"
                     f"请使用匹配的 SDK 前缀（通过 '--sdk-prefix' 指定）。")

    is_cross = (arch == "aarch64" and platform.machine() not in ("aarch64", "arm64"))
    toolchain_path = None
    if args.toolchain:
        t = Path(args.toolchain)
        toolchain_path = t.resolve() if t.is_absolute() else ((repo_root or pkg_root) / t).resolve()
    elif is_cross:
        if repo_root:
            toolchain_candidates = [
                repo_root / "build" / "native" / f"cross-{args.target}" / "toolchain.cmake",
                repo_root / "build" / "native" / "cross-rk3568-rknn-rga" / "toolchain.cmake",
                repo_root / "build" / "native" / "cross-rk3568-rknn" / "toolchain.cmake",
            ]
            for cand in toolchain_candidates:
                if cand.exists():
                    toolchain_path = cand.resolve()
                    break
        if not toolchain_path:
            sys.exit(f"错误: 在 {platform.machine()} 宿主机上交叉构建 aarch64 目标 ({args.target}) 需要指定 CMake 工具链。\n"
                     f"请通过 '--toolchain <path>' 指定 CMake 工具链文件。")

    rknn_root = args.rknn_root
    if not rknn_root and args.target in ("rk3568", "rk3588"):
        if repo_root and (repo_root / "native" / "vendor" / "rknpu2").exists():
            rknn_root = str((repo_root / "native" / "vendor" / "rknpu2").resolve())

    if (toolchain_path or is_cross) and not args.no_verify:
        print("[*] 交叉构建：跳过本机验包（产物在宿主不可执行）。")
        args.no_verify = True

    build_cache = target_dir / "build" / "CMakeCache.txt"
    if build_cache.exists():
        try:
            cache_text = build_cache.read_text(encoding="utf-8", errors="ignore")
            need_clean = False
            if toolchain_path and str(toolchain_path) not in cache_text:
                need_clean = True
            elif not toolchain_path and "CMAKE_TOOLCHAIN_FILE" in cache_text and not is_cross:
                need_clean = True
            if str(sdk_prefix_path) not in cache_text:
                need_clean = True
            if need_clean:
                print(f"[*] 构建参数或工具链已变，自动清理旧构建缓存: {target_dir / 'build'}")
                shutil.rmtree(target_dir / "build", ignore_errors=True)
        except Exception:
            pass

    exe_path = target_dir / "build" / pkg_id
    if not args.skip_build:
        print(f"[*] 构建包可执行文件（SDK 前缀 {sdk_prefix_path}）...")
        make_args = ["make", "-C", str(target_dir), "SDK_PREFIX=" + str(sdk_prefix_path)]
        if toolchain_path:
            make_args.append("TOOLCHAIN=" + str(toolchain_path))
        if rknn_root:
            make_args.append("RKNN_ROOT=" + str(rknn_root))
        if args.onnxruntime_root:
            make_args.append("ONNXRUNTIME_ROOT=" + str(Path(args.onnxruntime_root).resolve()))
        res = subprocess.run(make_args)
        if res.returncode != 0:
            sys.exit("错误: 包可执行文件构建失败")
    if not exe_path.exists():
        sys.exit(f"错误: 缺少包可执行文件 {exe_path}（去掉 --skip-build 或先 make）")

    build_dir = Path(args.build_dir)
    if not build_dir.is_absolute():
        build_dir = (repo_root or pkg_root) / build_dir

    staging_dir = build_dir / "staging" / f"{pkg_id}_{target_name}"
    if staging_dir.exists():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    shutil.copy2(manifest_path, staging_dir / "manifest.json")

    dest_entry = staging_dir / pkg_id
    shutil.copy2(exe_path, dest_entry)
    os.chmod(dest_entry, 0o755)
    print(f"[*] 包入口: {dest_entry.name}")

    symbol_check = None
    if repo_root and (repo_root / "native" / "tests" / "check_plugin_symbols.py").exists():
        symbol_check = repo_root / "native" / "tests" / "check_plugin_symbols.py"
    elif (sdk_prefix_path / "tools" / "check_plugin_symbols.py").exists():
        symbol_check = sdk_prefix_path / "tools" / "check_plugin_symbols.py"

    if symbol_check and shutil.which("nm") and not args.no_verify:
        res = subprocess.run([sys.executable, str(symbol_check), "--entry", str(dest_entry)])
        if res.returncode != 0:
            sys.exit("错误: 包入口导出了插件 C ABI（交付形态不得再暴露 dlopen 入口）")

    sample_src = target_dir / "sample.jpg"
    if not sample_src.exists():
        sys.exit(f"错误: 缺少 sample.jpg: {sample_src}")
    shutil.copy2(sample_src, staging_dir / "sample.jpg")

    schema_src = target_dir / "config.schema.json"
    if schema_src.exists():
        shutil.copy2(schema_src, staging_dir / "config.schema.json")

    models_src = target_dir / "models"
    if models_src.exists() and models_src.is_dir():
        shutil.copytree(models_src, staging_dir / "models", dirs_exist_ok=True)

    if not args.no_verify and args.target == "onnx":
        print(f"[*] 执行本地黑盒验包（cwd={staging_dir}）...")
        sample_path = staging_dir / "sample.jpg"
        verify_sample = sample_path
        converted = staging_dir / ".verify_sample.ppm"
        ffmpeg = shutil.which("ffmpeg")
        if ffmpeg:
            conv = subprocess.run(
                [ffmpeg, "-v", "error", "-y", "-i", str(sample_path),
                 "-pix_fmt", "rgb24", str(converted)],
                capture_output=True, text=True,
            )
            if conv.returncode == 0 and converted.exists():
                verify_sample = converted
            else:
                print("[!] 警告: sample.jpg 转 PPM 失败，验包将使用合成帧", file=sys.stderr)
        else:
            print("[!] 警告: 未找到 ffmpeg，验包将使用合成帧", file=sys.stderr)

        verify_cmd = [
            str(dest_entry),
            "--verify", "--sample", str(verify_sample),
        ]
        proc = subprocess.run(verify_cmd, capture_output=True, text=True, cwd=str(staging_dir))
        if converted.exists():
            converted.unlink()
        if proc.returncode != 0:
            print(proc.stderr, file=sys.stderr)
            sys.exit(f"错误: 黑盒验包未通过 (退出码 {proc.returncode})")

        last_line = proc.stdout.strip().splitlines()[-1] if proc.stdout.strip() else ""
        if not last_line:
            sys.exit("错误: 黑盒验包没有输出 JSON 报告")
        try:
            report = json.loads(last_line)
        except json.JSONDecodeError as error:
            sys.exit(f"错误: 验包报告不是合法 JSON ({error}): {last_line}")

        if report.get("status") != "passed":
            print(last_line, file=sys.stderr)
            sys.exit(f"错误: 验包报告 status={report.get('status')!r}（期望 passed）")

        if not report.get("sample_decoded", False):
            print("[!] 警告: 验包未解码真实样本 (sample_decoded=false)，本次只证明 ABI 生命周期；"
                  "请安装 ffmpeg 或用 PPM 样本复验", file=sys.stderr)
            print(f"[!] 黑盒验包（仅 ABI 冒烟）: {report.get('count', 0)} 条结果，cost_ms={report.get('cost_ms')}")
        else:
            print(f"[✓] 黑盒验包通过！{last_line}")

    out_dir = Path(args.output_dir)
    if not out_dir.is_absolute():
        out_dir = (repo_root or pkg_root) / out_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    archive_filename = f"{pkg_id}-{arch}-{target_name}.tar.gz"
    out_tar_path = out_dir / archive_filename
    print(f"[*] 正在打包至 {out_tar_path}...")

    with tarfile.open(out_tar_path, "w:gz") as tar:
        for item in sorted(staging_dir.iterdir()):
            tar.add(item, arcname=item.name)

    default_archive = out_dir / f"{pkg_id}.tar.gz"
    if args.target == "onnx":
        shutil.copy2(out_tar_path, default_archive)

    print(f"[✓] 算法包构建完成: {out_tar_path}")

if __name__ == "__main__":
    main()
