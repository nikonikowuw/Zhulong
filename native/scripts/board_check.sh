#!/usr/bin/env bash
# 板端验收：在真实 RK3568 上验证 rk3568 档位的算法包。
#
# 为什么在**板子上**编译包，而不是交叉编译完再拷过去：
#   1. 顺带证明 SDK 前缀在真设备上真的可用（头/库/CMake 变量齐全）；
#   2. 不需要宿主机工具链参与包构建，第三方用自己的 BSP 工具链是同样的路径；
#   3. 交叉编译包再多一层 toolchain 传递，出错时说不清是包的问题还是工具链的问题。
# 交叉只用于产 aarch64 SDK 前缀（`libZhulongAlgoRuntime.a` / `libzhulong_algo_sdk.a`）。
#
# 用法：
#   native/scripts/board_check.sh                     # 默认 face_recognition/rk3568 + 远端 rk3568
#   BOARD_HOST=rk3568 BOARD_DIR=/tmp/board-check native/scripts/board_check.sh
#   PKG=packages/face_recognition/rk3568 native/scripts/board_check.sh
#
# 前提：`ssh <BOARD_HOST>` 可用（~/.ssh/config 里有 Host），板端有 g++ / cmake / make / python3 / ffmpeg。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BOARD_HOST="${BOARD_HOST:-rk3568}"
BOARD_DIR="${BOARD_DIR:-/tmp/zhulong-board-check}"
PKG="${PKG:-packages/face_recognition/rk3568}"
# 出货形态由包自己的打包脚本产出（交叉构建 + 归档），板端自建那条路只是「SDK 可用性」证据。
BUILD_SCRIPT="${BUILD_SCRIPT:-packages/face_recognition/scripts/build_package.py}"
RKNN_ROOT="${RKNN_ROOT:-${REPO_ROOT}/native/vendor/rknpu2}"
# rga 档位的前缀会把 librga.so.2 传给消费方；非 rga 档位传了也无害。
RGA_ROOT="${RGA_ROOT:-${REPO_ROOT}/native/vendor/rga}"
# 默认档位是 rk3568-rknn-rga：板端两个硬件后端（NPU 推理 + RGA 前处理）都要，
# 二者是编译期互斥开关，但同一个档位里可以同时打开。
PROFILE="${PROFILE:-native/profiles/rk3568-rknn-rga.json}"
SDK_LOCAL="${SDK_LOCAL:-/tmp/zhulong-sdk-aarch64}"

cd "${REPO_ROOT}"

if [ ! -d "${PKG}" ]; then
    echo "错误: 包目录不存在: ${PKG}" >&2
    exit 2
fi

echo "[1/5] 交叉构建 aarch64 原生库（不跑测试：交叉只允许编译）"
python3 native/scripts/build.py --profile "${PROFILE}" build

echo "[2/5] 安装 aarch64 SDK 前缀 → ${SDK_LOCAL}"
rm -rf "${SDK_LOCAL}"
cmake --install "build/native/cross-$(python3 -c "import json,sys;print(json.load(open('${PROFILE}'))['name'])")" \
    --prefix "${SDK_LOCAL}"

echo "[3/5] 同步前缀与包到 ${BOARD_HOST}:${BOARD_DIR}"
ssh "${BOARD_HOST}" "rm -rf '${BOARD_DIR}' && mkdir -p '${BOARD_DIR}'"
rsync -az --delete "${SDK_LOCAL}/" "${BOARD_HOST}:${BOARD_DIR}/sdk/"
rsync -az --delete --exclude build "${PKG}/" "${BOARD_HOST}:${BOARD_DIR}/pkg/"
scp -q native/tests/check_plugin_symbols.py "${BOARD_HOST}:${BOARD_DIR}/"

echo "[4/5] 板端编译 + 开发期自测 + 验包 + 协议自查"
ssh "${BOARD_HOST}" bash -s <<REMOTE
set -euo pipefail
cd '${BOARD_DIR}/pkg'
make SDK_PREFIX='${BOARD_DIR}/sdk' >/dev/null
make test SDK_PREFIX='${BOARD_DIR}/sdk' > test.log 2>&1
grep -E 'result written|count' test.log | tail -2

# 验包/特征提取都只吃 netpbm：jpg 先转一次（骨架不链接解码器）
ffmpeg -v error -y -i sample.jpg -pix_fmt rgb24 /tmp/board_check.ppm
make verify SDK_PREFIX='${BOARD_DIR}/sdk' VERIFY_SAMPLE=/tmp/board_check.ppm | tail -1

python3 '${BOARD_DIR}/sdk/tools/algo_probe.py' \
    --entry "build/\$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")" \
    --pkg-dir . --all --sample /tmp/board_check.ppm --image /tmp/board_check.ppm

# 宿主黑盒判据：不得有 zhulong 符号、不得 NEEDED zhulong 库
entry="build/\$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")"
echo -n "undefined zhulong symbols: "; nm -D --undefined-only "\${entry}" | grep -c zhulong || true
echo -n "defined zhulong symbols  : "; nm -D "\${entry}" | grep -c zhulong || true
echo -n "ldd zhulong deps         : "; ldd "\${entry}" | grep -ci zhulong || true
python3 '${BOARD_DIR}/check_plugin_symbols.py' --entry "\${entry}" && echo "符号门禁通过"

# NPU 真在算：连续跑几次后看 load（CPU 回退不会有读数）
for _ in 1 2 3; do "\${entry}" --verify --sample /tmp/board_check.ppm >/dev/null 2>&1 || true; done
cat /sys/kernel/debug/rknpu/load 2>/dev/null || echo "rknpu load 不可读（可能未开 debugfs）"
REMOTE

echo "[5/6] 交叉构建出货形态（归档）并在板上验同一份产物"
TARGET_NAME="$(basename "${PKG}")"
TOOLCHAIN="${REPO_ROOT}/build/native/cross-$(python3 -c "import json;print(json.load(open('${PROFILE}'))['name'])")/toolchain.cmake"
python3 "${BUILD_SCRIPT}" --target "${TARGET_NAME}" --sdk-prefix "${SDK_LOCAL}" \
    --toolchain "${TOOLCHAIN}" --rknn-root "${RKNN_ROOT}" \
    --rga-root "${RGA_ROOT}" > board-cross-build.log 2>&1 \
    || { tail -20 board-cross-build.log; exit 1; }
ARCHIVE="$(ls -t build/packages/*-"${TARGET_NAME}".tar.gz | head -1)"
echo "    归档: ${ARCHIVE}"
scp -q "${ARCHIVE}" "${BOARD_HOST}:/tmp/board-pkg.tar.gz"

ssh "${BOARD_HOST}" bash -s <<'REMOTE' || exit 1
set -euo pipefail
# 归档 → 解包 → 直接跑（这就是宿主安装后干的事：解到 <root>/<id>/<ver>/，再 exec 包根入口）
rm -rf /tmp/board-pkg && mkdir -p /tmp/board-pkg
tar xzf /tmp/board-pkg.tar.gz -C /tmp/board-pkg
cd /tmp/board-pkg
id="$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")"
echo -n "    归档内容: "; ls | tr '\n' ' '; echo
"./${id}" --verify --sample /tmp/board_check.ppm | tail -1
echo -n "    归档内 targets/ 与 plugin.so 计数（必须为 0）: "
tar tzf /tmp/board-pkg.tar.gz | grep -cE 'targets/|plugin\.so' || true
REMOTE

echo "[6/6] 板端验收完成"
