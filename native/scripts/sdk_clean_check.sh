#!/usr/bin/env bash
# 干净容器验收：算法包能否**只依赖 SDK 前缀**构建、自查与运行。
#
# 为什么要单独一个脚本：整套「包脱离仓库独立开发」的说法，只有在没有本仓库、
# 没有宿主二进制、没有仓库 build 目录的机器上跑通才算数。开发机上「碰巧能找到
# ../native 的头」是最容易漏的假绿。
#
# 做法：
#   1. 宿主机准备好 SDK 前缀（含 onnxruntime，BUNDLE_RUNTIME=1）；
#   2. 把**包目录**与**前缀**只读挂进一个干净 debian，装好编译器（容器内 apt）；
#   3. 容器内 `make` 构建 → `make test` 跑 devtest → `tools/algo_probe.py --all` 自查协议；
#   4. 断言包工程目录里不存在任何仓库路径（../native、../../build 之类）。
#
# 用法：
#   native/scripts/sdk_clean_check.sh packages/face_recognition/onnx [packages/plate_recognition/onnx ...]
#
# 需要：docker（或 podman），以及宿主机已 `make sdk-install BUNDLE_RUNTIME=1 SDK_PREFIX=<dir>`。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SDK_PREFIX="${SDK_PREFIX:-${REPO_ROOT}/build/sdk/prefix}"
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-docker}"
# 镜像基线必须 ≥ 构建前缀那台机器：前缀里的 libZhulongAlgoRuntime.a / onnxruntime 是
# 在开发机上编译的二进制，符号版本（GLIBC_2.38 / CXXABI_1.3.15 …）没法向下兼容。
# 默认取与开发机同代或更新的发行版；更老的镜像上「链接期 undefined reference to
# __isoc23_strtol@GLIBC_2.38」不是包的问题，是镜像太旧。
IMAGE="${IMAGE:-debian:trixie-slim}"

if [ "$#" -eq 0 ]; then
    echo "用法: $0 <package-dir> [package-dir ...]" >&2
    exit 2
fi
if [ ! -f "${SDK_PREFIX}/lib/libzhulong_algo_sdk.a" ]; then
    echo "错误: SDK 前缀不完整: ${SDK_PREFIX}（先跑 make sdk-install BUNDLE_RUNTIME=1）" >&2
    exit 2
fi
if [ ! -e "${SDK_PREFIX}/third_party/onnx/lib/libonnxruntime.so" ]; then
    echo "错误: 前缀里没有 onnxruntime（BUNDLE_RUNTIME=1），容器内无法链接" >&2
    exit 2
fi

# 1) 仓库路径门禁：包目录里不得出现指向仓库的相对路径
echo "[*] 检查包目录是否残留仓库路径..."
if grep -rnE '\.\./(\.\./)?(native|build)' \
        --include='CMakeLists.txt' --include='Makefile' --include='*.cmake' \
        --include='*.cpp' --include='*.hpp' --include='*.h' \
        "$@" ; then
    echo "错误: 上面的包文件引用了仓库路径；包必须只依赖 SDK_PREFIX" >&2
    exit 1
fi

for pkg_dir in "$@"; do
    abs_pkg="$(cd "${pkg_dir}" && pwd)"
    pkg_name="$(basename "${abs_pkg}")"
    echo "[*] === ${pkg_name} ==="

    # 2) 干净容器：只挂包目录（读写，构建产物落回去）与前缀（只读）
    "${CONTAINER_RUNTIME}" run --rm \
        -v "${abs_pkg}:/work" \
        -v "${SDK_PREFIX}:/opt/zhulong-sdk:ro" \
        -w /work \
        "${IMAGE}" \
        bash -lc '
            set -euo pipefail
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq >/dev/null
            apt-get install -y -qq --no-install-recommends g++ cmake make python3 ffmpeg >/dev/null

            echo "[容器] $(g++ --version | head -1) / $(cmake --version | head -1)"

            # 从零构建：不继承宿主机任何 build 目录内容
            rm -rf build
            make SDK_PREFIX=/opt/zhulong-sdk

            # 开发期自测：sample.jpg 进 → result.jpg 出（读 jpg 用 SDK 的 stb）
            make test SDK_PREFIX=/opt/zhulong-sdk

            # 协议自查：jpg 先转 netpbm（骨架不含解码器），再跑两种协议
            sample_ppm=/tmp/sample.ppm
            ffmpeg -v error -y -i sample.jpg -pix_fmt rgb24 "${sample_ppm}"
            entry="build/$(python3 -c "import json;print(json.load(open(\"manifest.json\"))[\"id\"])")"
            python3 /opt/zhulong-sdk/tools/algo_probe.py \
                --entry "${entry}" --pkg-dir . --all \
                --sample "${sample_ppm}" --image "${sample_ppm}"
        '

    echo "[✓] ${pkg_name}: 干净容器验收通过"
done
