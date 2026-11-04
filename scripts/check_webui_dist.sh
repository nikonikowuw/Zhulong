#!/usr/bin/env bash
# 内嵌 SPA 新鲜度门禁：`web/dist` 与 `internal/webui/dist` 必须逐文件同内容。
#
# 为什么需要这道闸：`cross` / `cross-board` 只编原生库与 Go 宿主，**不重建前端**
# （见 Makefile 里紧挨 `cross` 的注释）。于是「改了前端 → 交叉编译 → 发到板子」
# 这条最自然的路径会把一个旧 SPA 一起发出去，而产物、测试、日志全部正常 ——
# 只有人肉点开控制台才会发现少了个按钮。这里把「忘了 make frontend-build」
# 变成一次硬失败。
#
# 判据是「文件列表 + 每个文件的 SHA-256」，不是 mtime：
#   - `cp -r` 的 mtime 会被保留或重写，取决于拷贝方式，不能当证据；
#   - Vite 产物文件名带内容哈希，但 `index.html`、`*.css`、字体与 public 资源不带，
#     只比文件名会漏掉「index.html 引用了不存在的 chunk」这种最常见的半更新状态。
#
# 用法：
#   scripts/check_webui_dist.sh          # 通过则静默退出 0
#   make webui-dist-check                # 同一条闸，挂在 cross / cross-board 前
#
# 缺失行为：`web/dist` 不存在时**只告警**并成功返回。
# 理由：新克隆的仓库、只跑 Go 侧测试的 CI、以及 `make check` 之外的交叉编译
# 都可能没有前端产物；此时既没有旧副本会被误发（`internal/webui/dist` 是
# 版本库里的现状），也没有可比较的对象，硬失败只会逼人先跑一遍 Node 构建。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="${REPO_ROOT}/web/dist"
EMBEDDED="${REPO_ROOT}/internal/webui/dist"

if [ ! -d "${SOURCE}" ]; then
    echo "[!] web/dist 不存在：跳过内嵌 SPA 新鲜度检查（需要发布版前端时先跑 make frontend-build）" >&2
    exit 0
fi

if [ ! -d "${EMBEDDED}" ]; then
    echo "错误: web/dist 存在但 internal/webui/dist 不存在。" >&2
    echo "      先跑 make frontend-build（它会重建 web/dist 并覆盖内嵌副本）。" >&2
    exit 1
fi

# 清单格式：`<sha256>  <相对路径>`，按路径排序。
# 用 `-print0 | sort -z` 而不是 `sort`：产物里出现过带空格的文件名时，
# 按行排序会把同一文件的两半排到不同位置，比较结果毫无意义。
#
# 排除 `.keep`：它是版本库为了留住 embed 目录而放的占位文件，只存在于
# `internal/webui/dist/`，而 `web/dist/` 里永远不会有它 —— 不排除的话这道闸
# 会 100% 误报，然后被当成噪音忽略掉。
manifest() {
    local dir="$1"
    ( cd "${dir}" && find . -type f ! -name '.keep' -print0 \
        | LC_ALL=C sort -z | xargs -0 -r sha256sum )
}

if diff -u <(manifest "${SOURCE}") <(manifest "${EMBEDDED}") > /tmp/zhulong-webui-dist.diff; then
    files="$(manifest "${SOURCE}" | wc -l | tr -d ' ')"
    echo "[✓] 内嵌 SPA 与 web/dist 一致（${files} 个文件）"
    exit 0
fi

echo "错误: internal/webui/dist 与 web/dist 不一致 —— 内嵌 SPA 是旧的。" >&2
echo "      产物会带着旧控制台一起发布。执行 make frontend-build 后重试。" >&2
echo "      差异（左侧 web/dist，右侧 internal/webui/dist）：" >&2
# 只打印差异摘要：整份清单可能有几千行，全量打出来只会把真正的线索冲掉。
# 落文件再截断，不用管道——`grep | head` 会因 SIGPIPE 让 set -o pipefail 提前失败。
grep -E '^[+-][^+-]|^@@' /tmp/zhulong-webui-dist.diff > /tmp/zhulong-webui-dist.summary || true
sed -n '1,40p' /tmp/zhulong-webui-dist.summary >&2
echo "      完整差异见 /tmp/zhulong-webui-dist.diff" >&2
exit 1
