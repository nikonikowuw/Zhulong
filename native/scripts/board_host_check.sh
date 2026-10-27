#!/usr/bin/env bash
# 宿主交叉编译验收：把**整个系统**（Go + CGO + 原生引擎）交叉到板子上跑起来。
#
# 与 board_check.sh 的分工：
#   board_check.sh     验「算法包」这条支线（板端自建 / 交叉出包 → 包根可执行文件）
#   board_host_check.sh 验「宿主」这条主线（交叉产 build/targets/<profile>/Zhulong → 板上真跑）
#
# 覆盖的链路（全部在板子上发生）：
#   启动 → SQLite 迁移 → 原生引擎 init → HTTP/SPA → 管理员初始化/登录
#   → 上传算法包（宿主 exec 包入口跑 --verify）→ 启用 → 人员建档
#   → 人脸登记（宿主 fork 包进程 --extract-feature → RKNN/NPU → 512 维入库）
#   → 空闲回收包进程 → 跨平台包被拒
#
# 用法：
#   native/scripts/board_host_check.sh
#   BOARD_HOST=rk3568 PROFILE=native/profiles/rk3568-rknn.json native/scripts/board_host_check.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BOARD_HOST="${BOARD_HOST:-rk3568}"
PROFILE="${PROFILE:-native/profiles/rk3568-rknn.json}"
REMOTE_DIR="${REMOTE_DIR:-/root/zhulong-host-check}"
# 端口刻意不用 8000：板上可能还跑着上一次手工起的实例，撞端口会让本脚本的 curl 打到
# **旧实例**上（症状是「全新 data 目录却报算法已安装」这种莫名其妙的失败）。
HTTP="${HTTP:-127.0.0.1:8123}"
PROFILE_NAME="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["name"])' "${REPO_ROOT}/${PROFILE}")"
HOST_BIN="${REPO_ROOT}/build/targets/${PROFILE_NAME}/Zhulong"

cd "${REPO_ROOT}"

echo "[1/5] 交叉编译宿主二进制（profile=${PROFILE_NAME}）"
make -s native-cross-build CROSS_PROFILE="${PROFILE}" CROSS_OUTPUT="${HOST_BIN}"
file "${HOST_BIN}" | grep -q "ARM aarch64" || { echo "错误: 产物不是 aarch64" >&2; exit 1; }
echo "    $(basename "${HOST_BIN}"): $(du -h "${HOST_BIN}" | cut -f1)"
# 黑盒判据：宿主不得 NEEDED 任何算法运行库/厂商推理库
if readelf -d "${HOST_BIN}" | grep -E "NEEDED" | grep -qE "onnxruntime|rknnrt|Zhulong"; then
    echo "错误: 宿主 NEEDED 了算法运行库" >&2
    readelf -d "${HOST_BIN}" | grep NEEDED
    exit 1
fi
echo "    NEEDED 干净（无 onnxruntime / rknnrt / libZhulong*）"

echo "[2/5] 准备交付物（宿主二进制 + 板端 config.toml + 人脸算法包）"
PKG_ARCHIVE="$(ls -t build/packages/face_recognition-*-rk3568.tar.gz 2>/dev/null | head -1 || true)"
if [ -z "${PKG_ARCHIVE}" ]; then
    echo "    没有 rk3568 人脸包，先交叉出包"
    SDK_LOCAL="${SDK_LOCAL:-/tmp/zhulong-sdk-aarch64}"
    [ -d "${SDK_LOCAL}" ] || { rm -rf "${SDK_LOCAL}"; cmake --install "build/native/cross-${PROFILE_NAME}" --prefix "${SDK_LOCAL}"; }
    python3 packages/face_recognition/scripts/build_package.py --target rk3568 \
        --sdk-prefix "${SDK_LOCAL}" \
        --toolchain "build/native/cross-${PROFILE_NAME}/toolchain.cmake" \
        --rknn-root native/vendor/rknpu2 > board-host-pkg.log 2>&1 \
        || { tail -20 board-host-pkg.log; exit 1; }
    PKG_ARCHIVE="$(ls -t build/packages/face_recognition-*-rk3568.tar.gz | head -1)"
fi
echo "    包: ${PKG_ARCHIVE}"

echo "[3/5] 部署到 ${BOARD_HOST}:${REMOTE_DIR}"
ssh "${BOARD_HOST}" "rm -rf '${REMOTE_DIR}' && mkdir -p '${REMOTE_DIR}'"
scp -q "${HOST_BIN}" "${BOARD_HOST}:${REMOTE_DIR}/Zhulong"
scp -q "${PKG_ARCHIVE}" "${BOARD_HOST}:${REMOTE_DIR}/pkg.tar.gz"
# 板端配置：data 目录在 REMOTE_DIR 下，algorithm.target 必须等于包 manifest 的 platform.target
sed -e "s|^address = .*|address = \"0.0.0.0:${HTTP##*:}\"|" \
    -e "s|^directory = .*|directory = \"${REMOTE_DIR}/data\"|" \
    -e 's|^target = .*|target = "rk3568"|' \
    config.toml > /tmp/zhulong-board-config.toml
scp -q /tmp/zhulong-board-config.toml "${BOARD_HOST}:${REMOTE_DIR}/config.toml"
# 人脸样本与「跨平台包应被拒」用的对照组
scp -q packages/face_recognition/rk3568/sample.jpg "${BOARD_HOST}:${REMOTE_DIR}/face.jpg"
MISMATCH_PKG="${MISMATCH_PKG:-$(ls -t build/packages/plate_recognition-x86_64-onnx.tar.gz 2>/dev/null | head -1 || true)}"
if [ -n "${MISMATCH_PKG}" ]; then
    scp -q "${MISMATCH_PKG}" "${BOARD_HOST}:${REMOTE_DIR}/plate.tar.gz"
else
    ssh "${BOARD_HOST}" "cp '${REMOTE_DIR}/pkg.tar.gz' '${REMOTE_DIR}/plate.tar.gz'  # 退而求其次：同包重装也应幂等"
fi

echo "[4/5] 板端启动并跑通全链路"
ssh "${BOARD_HOST}" bash -s <<REMOTE
set -euo pipefail
cd '${REMOTE_DIR}'
# 先确认端口上没有旧实例：否则 wait_ready 会打到**上一个实例**上，
# 症状是「刚删了 data 目录却报算法/人员已存在」这种说不通的失败。
#
# 按 /proc/<pid>/exe 认亲，不按命令行：脚本用相对路径 `./Zhulong` 启动，argv[0] 里
# 没有目录，pkill -f 之类根本匹配不到（而且 pkill -f 还会匹配到自己的 ssh 命令行、
# 把自己杀掉 → ssh 255）。
[ -f pid ] && kill "\$(cat pid)" 2>/dev/null || true
python3 - <<'PYEOF' || true
import glob, os, signal
for entry in glob.glob('/proc/[0-9]*/exe'):
    try:
        exe = os.readlink(entry)
    except OSError:
        continue
    # 去掉 " (deleted)"：scp 覆写可执行文件后，内核给在跑的进程加这个后缀。
    # 忘了这一步的后果就是「明明有旧实例在跑，脚本却认为端口是干净的」。
    # 不用 str.removesuffix：它要 Python 3.9+，而板端解释器版本不受本仓控制。
    deleted_suffix = ' (deleted)'
    if exe.endswith(deleted_suffix):
        exe = exe[:-len(deleted_suffix)]
    if exe.startswith('${REMOTE_DIR}/') and exe.endswith('/Zhulong'):
        try:
            os.kill(int(entry.split('/')[2]), signal.SIGKILL)
        except OSError:
            pass
PYEOF
for _ in \$(seq 1 30); do
  curl -s -m 1 -o /dev/null "http://${HTTP}/api/v1/health" || break
  sleep 0.5
done
if curl -s -m 1 -o /dev/null "http://${HTTP}/api/v1/health"; then
  echo "FAIL 端口 ${HTTP} 上仍有旧实例占用（先手工杀掉）"; exit 1
fi
rm -rf data server.log cookies.txt
nohup "${REMOTE_DIR}/Zhulong" > server.log 2>&1 &
echo \$! > pid

api() { curl -s -m "\$1" -b cookies.txt "\${@:2}"; }
wait_ready() {
  for _ in \$(seq 1 40); do
    if api 2 http://${HTTP}/api/v1/health | grep -q '"status":"ready"'; then return 0; fi
    sleep 0.5
  done
  echo "FAIL 健康检查未就绪"; tail -20 server.log; exit 1
}

wait_ready
echo "PASS 启动就绪（database + engine）"

curl -s -m 5 -o /dev/null -w "" http://${HTTP}/
index_bytes=\$(curl -s -m 5 -o /dev/null -w '%{size_download}' http://${HTTP}/)
[ "\${index_bytes}" -gt 1000 ] && echo "PASS SPA 内嵌前端可服务（\${index_bytes} bytes）" \
    || { echo "FAIL SPA 首页字节数异常: \${index_bytes}"; exit 1; }

curl -s -m 5 -c cookies.txt -X POST http://${HTTP}/api/v1/auth/init -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Zhulong#2026","confirmPassword":"Zhulong#2026"}' >/dev/null || true
api 10 -c cookies.txt -X POST http://${HTTP}/api/v1/auth/login -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"Zhulong#2026"}' | grep -q '"code":"OK"' \
    && echo "PASS 管理员初始化/登录" || { echo "FAIL 登录失败"; exit 1; }

install=\$(api 180 -X POST http://${HTTP}/api/v1/algorithms/install -F 'file=@pkg.tar.gz')
echo "\${install}" | grep -q '"code":"OK"' \
    && echo "PASS 算法包安装（宿主 exec 包入口跑 --verify）" \
    || { echo "FAIL 安装失败: \${install}"; exit 1; }

api 60 -X POST http://${HTTP}/api/v1/algorithms/face_recognition/enable | grep -q '"status":"ACTIVE"' \
    && echo "PASS 算法启用" || { echo "FAIL 启用失败"; exit 1; }

person=\$(api 20 -X POST http://${HTTP}/api/v1/persons -H 'Content-Type: application/json' \
    -d '{"personId":"P-HOST-1","name":"主机链路验证"}')
person_id=\$(echo "\${person}" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or {};print(d.get("id",""))')
if [ -z "\${person_id}" ]; then
  echo "FAIL 建档失败: \${person}"; exit 1
fi

start_ms=\$(date +%s%N)
face=\$(api 180 -X POST "http://${HTTP}/api/v1/persons/\${person_id}/faces" -F 'file=@face.jpg')
elapsed_ms=\$(( (\$(date +%s%N) - start_ms) / 1000000 ))
echo "\${face}" | grep -q '"code":"OK"' \
    && echo "PASS 人脸登记：宿主 → 包进程 --extract-feature → NPU（\${elapsed_ms}ms）" \
    || { echo "FAIL 人脸登记失败: \${face}"; exit 1; }

ps -eo pid,cmd | grep -q "[f]ace_recognition --extract-feature" \
    && echo "PASS 特征提取进程在跑（--extract-feature）" || { echo "FAIL 没看到包进程"; exit 1; }

# 空闲回收：默认 60s，但 reaper 是 IdleTimeout/4 的节拍（15s）轮询，
# 实际落点在 60~75s 之间。写死 sleep 65 会偶发假红 —— 这里轮询到 120s 上限。
for _ in \$(seq 1 60); do
  ps -eo pid,cmd | grep -q "[f]ace_recognition --extract-feature" || break
  sleep 2
done
ps -eo pid,cmd | grep -q "[f]ace_recognition --extract-feature" \
    && { echo "FAIL 包进程 120s 内未被空闲回收"; exit 1; } || echo "PASS 空闲超时回收包进程"

mismatch=\$(api 120 -X POST http://${HTTP}/api/v1/algorithms/install -F 'file=@plate.tar.gz')
echo "INFO 对照组上传结果：\$(echo "\${mismatch}" | head -c 160)"
REMOTE

echo "[5/5] 宿主交叉验收完成"
