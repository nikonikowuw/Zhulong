#!/usr/bin/env python3
"""
build_package.py - 车牌识别算法包自动化组装与校验脚本

用法:
    python3 packages/plate_recognition/scripts/build_package.py [--target onnx|rk3568|rk3588] [--output-dir build/packages]
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

def main():
    parser = argparse.ArgumentParser(description="组装车牌识别算法包")
    parser.add_argument("--target", default="onnx", choices=["onnx", "rk3568", "rk3588"], help="目标平台 (默认: onnx)")
    parser.add_argument("--build-dir", default="build/native/host", help="C++ 构建产物目录 (默认: build/native/host)")
    parser.add_argument("--output-dir", default="build/packages", help="输出包目录 (默认: build/packages)")
    parser.add_argument("--no-verify", action="store_true", help="跳过验包验证")
    parser.add_argument("--sdk-prefix", default="build/sdk/prefix",
                        help="算法包 SDK 前缀（默认: build/sdk/prefix，需先 make sdk-install）")
    parser.add_argument("--skip-build", action="store_true",
                        help="跳过包自身可执行文件的构建（复用 packages/<id>/<target>/build/<id>）")
    parser.add_argument("--toolchain", help="交叉构建用的 CMake 工具链文件（如 "
                                            "build/native/cross-rk3568-rknn/toolchain.cmake）；"
                                            "给了就跳过验包（交叉产物本机不能执行）")
    parser.add_argument("--rknn-root", help="RKNN 前缀（交叉构建 rknn 档位时必需）")
    parser.add_argument("--onnxruntime-root", help="ONNX Runtime 前缀（交叉构建 onnx 档位时用）")
    args = parser.parse_args()
    if args.toolchain and not args.no_verify:
        # 交叉产物在宿主机上 exec 只会得到 "Exec format error"；与其让它跑到验包步骤
        # 才失败，不如在这里说清楚，并指明板端/容器里怎么验。
        print("[*] 交叉构建：跳过本机验包（产物在宿主不可执行）。请在目标设备上跑 "
              "<pkg>/<manifest.id> --verify --sample <netpbm>，或用 make board-check。")
        args.no_verify = True

    repo_root = Path(__file__).resolve().parent.parent.parent.parent
    pkg_root = repo_root / "packages" / "plate_recognition"
    # 布局：packages/<id>/<target>/ 就是该档位的包目录（manifest / models / sample /
    # config.schema.json 全在档位目录内，不再有 targets/ 中间层）。
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
    pkg_id = manifest.get("id", "plate_recognition")
    version = manifest.get("version", "1.0.0")

    build_dir = repo_root / args.build_dir

    # ---- 1. 构建包自己的可执行文件（交付形态）--------------------------------
    # 包工程只依赖 SDK 前缀；这里用包自己的 Makefile 驱动，与包作者在本机跑的命令一致。
    sdk_prefix = Path(args.sdk_prefix)
    if not sdk_prefix.is_absolute():
        sdk_prefix = repo_root / sdk_prefix
    exe_path = target_dir / "build" / pkg_id
    if not args.skip_build:
        if not sdk_prefix.exists():
            sys.exit(f"错误: SDK 前缀不存在: {sdk_prefix}（先跑 make sdk-install）")
        print(f"[*] 构建包可执行文件（SDK 前缀 {sdk_prefix}）...")
        make_args = ["make", "-C", str(target_dir), "SDK_PREFIX=" + str(sdk_prefix)]
        if args.toolchain:
            make_args.append("TOOLCHAIN=" + str(Path(args.toolchain).resolve()))
        if args.rknn_root:
            make_args.append("RKNN_ROOT=" + str(Path(args.rknn_root).resolve()))
        if args.onnxruntime_root:
            make_args.append("ONNXRUNTIME_ROOT=" + str(Path(args.onnxruntime_root).resolve()))
        res = subprocess.run(make_args)
        if res.returncode != 0:
            sys.exit("错误: 包可执行文件构建失败")
    if not exe_path.exists():
        sys.exit(f"错误: 缺少包可执行文件 {exe_path}（去掉 --skip-build 或先 make）")


    staging_dir = build_dir / "staging" / f"{pkg_id}_{target_name}"
    if staging_dir.exists():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    # 1. 复制 manifest.json
    shutil.copy2(manifest_path, staging_dir / "manifest.json")

    # 2. 包根可执行文件：文件名必须等于 manifest.id（宿主按 id 找入口）
    dest_entry = staging_dir / pkg_id
    shutil.copy2(exe_path, dest_entry)
    os.chmod(dest_entry, 0o755)
    print(f"[*] 包入口: {dest_entry.name}")

    # 交付形态的符号门禁：可执行文件不得导出任何插件 C ABI 残留
    # （`zhulong_plugin_get_api` / `zhulong_*`），否则旧 dlopen 通道还活着。
    symbol_check = repo_root / "native" / "tests" / "check_plugin_symbols.py"
    if symbol_check.exists() and shutil.which("nm") and not args.no_verify:
        res = subprocess.run([sys.executable, str(symbol_check), "--entry", str(dest_entry)])
        if res.returncode != 0:
            sys.exit("错误: 包入口导出了插件 C ABI（交付形态不得再暴露 dlopen 入口）")

    # 3. 复制 sample.jpg
    sample_src = target_dir / "sample.jpg"
    if not sample_src.exists():
        sys.exit(f"错误: 缺少 sample.jpg: {sample_src}")
    shutil.copy2(sample_src, staging_dir / "sample.jpg")

    # 4. 复制 config.schema.json
    schema_src = target_dir / "config.schema.json"
    if schema_src.exists():
        shutil.copy2(schema_src, staging_dir / "config.schema.json")

    # 5. 复制 models/
    models_src = target_dir / "models"
    if models_src.exists() and models_src.is_dir():
        shutil.copytree(models_src, staging_dir / "models", dirs_exist_ok=True)

    # 6. 本地黑盒验包
    #    ⚠️ 必须以 staging_dir 为 CWD 调用：包目录才是部署布局（可执行文件与 models/ 同级）。
    #    从仓库根跑会命中源码树里的权重，让「模型路径拼错」这类缺陷在准入阶段 100% 逃逸。
    if not args.no_verify and args.target == "onnx":
        print(f"[*] 执行本地黑盒验包（cwd={staging_dir}）...")
        sample_path = staging_dir / "sample.jpg"
        # runner 不含图像解码器：若本机有 ffmpeg 就转成 PPM，让验包真的跑一张图；
        # 否则退回 .jpg（runner 会输出 sample_decoded=false，不假装验过样本）。
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
                print("[!] 警告: sample.jpg 转 PPM 失败，验包将使用合成帧（sample_decoded=false）", file=sys.stderr)
        else:
            print("[!] 警告: 未找到 ffmpeg，验包将使用合成帧（sample_decoded=false）", file=sys.stderr)

        # 用**包根可执行文件**验包——与生产启动的是同一个东西
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
        report = proc.stdout.strip().splitlines()[-1] if proc.stdout.strip() else "{}"
        print(f"[✓] 黑盒验包通过！{report}")
        if '"sample_decoded":false' in report:
            print("[!] 警告: 本次验包未使用真实样本帧，只证明了 ABI 生命周期", file=sys.stderr)

    # 7. 打包成 .tar.gz
    out_dir = repo_root / args.output_dir
    out_dir.mkdir(parents=True, exist_ok=True)

    archive_filename = f"{pkg_id}-{arch}-{target_name}.tar.gz"
    out_tar_path = out_dir / archive_filename
    print(f"[*] 正在打包至 {out_tar_path}...")

    with tarfile.open(out_tar_path, "w:gz") as tar:
        for item in sorted(staging_dir.iterdir()):
            tar.add(item, arcname=item.name)

    print(f"[✓] 算法包构建完成: {out_tar_path}")

if __name__ == "__main__":
    main()
