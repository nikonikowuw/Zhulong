#!/usr/bin/env bash
# 生成音画同步测量素材：白闪 H.264 + 1 kHz beep 的裸 G.711 μ-law。
#
# 判据（父任务 design.md §6.3）：画面每 2 s 白闪 80 ms，同一媒体时刻叠 1 kHz / 100 ms beep。
# 两者必须来自**同一个媒体时间轴**：视频帧时刻 = 帧号 × 40 ms，音频采样时刻 = 采样号 ÷ 8000。
# 因此墙钟对齐没有意义，材料里对齐的是媒体时间。
#
# 为什么用 ffmpeg 现场生成而不是进版本库：
#   - 素材 16 s 就有几百 KB，进库后没人再动它，等于一个不可审计的二进制块；
#   - x264 的输出随版本变化，而这段素材的作用只是「闪得出来、响得出来」，不需要稳定比特流；
#   - 宿主机与板端都自带 ffmpeg（`make native-deps` 的静态库不提供 CLI）。
#
# 用法：
#   native/tests/make_sync_clip.sh                # → /tmp/zhulong-sync.{h264,pcmu}
#   native/tests/make_sync_clip.sh /tmp/clip 20   # 自定义前缀与时长（秒）
#
# 输出：
#   <prefix>.h264  Annex-B，Baseline，640x360@25fps，每秒一个 IDR，带内 SPS/PPS
#   <prefix>.pcmu  裸 μ-law，8 kHz 单声道，每 2 s 一个 100 ms 的 1 kHz 音
set -euo pipefail

PREFIX="${1:-/tmp/zhulong-sync}"
DURATION="${2:-16}"

command -v ffmpeg > /dev/null 2>&1 || { echo "错误: 需要 ffmpeg（宿主机或板端均可）" >&2; exit 2; }

# 白闪：每 2 s 的前 80 ms 画满白（25 fps 下正好两帧 —— 只闪一帧在 60 Hz 屏幕上
# 也只有 40 ms，用 canvas 采样时容易错过）。
ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "color=c=black:s=640x360:r=25:d=${DURATION}" \
    -vf "drawbox=x=0:y=0:w=iw:h=ih:color=white:t=fill:enable='lt(mod(t,2),0.08)'" \
    -c:v libx264 -profile:v baseline -level 3.0 -pix_fmt yuv420p \
    -g 25 -keyint_min 25 -sc_threshold 0 \
    -x264-params "repeat-headers=1:aud=0" \
    -f h264 "${PREFIX}.h264"

# beep：每 2 s 的前 100 ms 放 1 kHz 正弦（中间静音），转 μ-law 8 kHz 单声道。
#
# 为什么用 aevalsrc 而不是 `sine` + `volume=enable='…'`：`volume` 的 `t` 只在
# **帧**边界更新（默认 1024 样本 = 128 ms），实测把 2.000 s 的 beep 推到 2.048 s、
# 4.000 → 4.096，而且每次落后多少还不同 —— 用这种素材去测「≤ 80 ms」等于用
# 一堆指甲先把基准砸碎。aevalsrc 的 `t` 是**逐样本**时间，起点误差 1 个采样。
ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "aevalsrc='0.5*sin(2*PI*1000*t)*lt(mod(t\,2)\,0.1)':s=8000:d=${DURATION}" \
    -ar 8000 -ac 1 -c:a pcm_mulaw -f mulaw "${PREFIX}.pcmu"

printf '[✓] %s.h264 (%s)\n' "${PREFIX}" "$(du -h "${PREFIX}.h264" | cut -f1)"
printf '[✓] %s.pcmu (%s)\n' "${PREFIX}" "$(du -h "${PREFIX}.pcmu" | cut -f1)"
