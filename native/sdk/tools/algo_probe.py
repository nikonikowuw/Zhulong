#!/usr/bin/env python3
"""算法包自查工具（随 SDK 分发，只用 Python 标准库）。

用法：
    algo_probe.py --entry <pkg>/<manifest.id> --all --sample sample.ppm
    algo_probe.py --entry <pkg>/build/<id> --pkg-dir <pkg> --all --sample sample.ppm   # 开发布局
    algo_probe.py --entry <pkg>/<manifest.id> --verify --sample face.ppm
    algo_probe.py --entry <pkg>/<manifest.id> --extract-feature --image face.ppm

它验证的是**包与宿主之间那两个公开协议**：

  --verify          安装期黑盒验包：骨架跑一次完整生命周期，最后一行 JSON 报告。
  --extract-feature 底库登记通道：28 字节帧头 + 裸像元 → 一行 JSON 特征。

不依赖任何第三方库、不依赖仓库：把它和 SDK 前缀一起拷到任意机器即可自查。

⚠️ 图像只支持 netpbm（P6 彩色 / P5 灰度）：包骨架不链接任何编解码器，JPEG/PNG
   的解码责任在调用侧（宿主用 Go 标准库解码后以 PPM 交过来）。要拿 jpg 自查，
   先用 `ffmpeg -i sample.jpg -pix_fmt rgb24 sample.ppm` 转一次。
"""

import argparse
import json
import os
import struct
import subprocess
import sys

FRAME_MAGIC = 0x45464C5A  # ASCII "ZLFE"（小端 u32）
HEADER_BYTES = 28
FORMAT_BGR24 = 1
FORMAT_RGB24 = 2
MAX_DIMENSION = 16384
EXPECTED_FEATURE_DIM = 512


class ProbeError(Exception):
    pass


def read_netpbm(path):
    """把 P6/P5 读成 (pixels, width, height, channels)。只认二进制 netpbm。"""
    with open(path, "rb") as handle:
        data = handle.read()

    fields = []
    index = 0
    while len(fields) < 4:
        while index < len(data) and data[index:index + 1].isspace():
            index += 1
        if index < len(data) and data[index:index + 1] == b"#":
            while index < len(data) and data[index:index + 1] not in (b"\n", b"\r"):
                index += 1
            continue
        start = index
        while index < len(data) and not data[index:index + 1].isspace():
            index += 1
        if start == index:
            raise ProbeError(f"{path}: truncated netpbm header")
        fields.append(data[start:index])
    index += 1  # 头与像素之间恰好一个空白

    magic, width, height, maxval = fields[0], int(fields[1]), int(fields[2]), int(fields[3])
    if magic == b"P6":
        channels = 3
    elif magic == b"P5":
        channels = 1
    else:
        raise ProbeError(f"{path}: unsupported netpbm magic {magic!r} (need P6 or P5)")
    if maxval != 255:
        raise ProbeError(f"{path}: only 8-bit netpbm is supported (maxval={maxval})")

    want = width * height * channels
    pixels = data[index:index + want]
    if len(pixels) != want:
        raise ProbeError(f"{path}: expected {want} pixel bytes, got {len(pixels)}")
    return pixels, width, height, channels


def to_rgb24(pixels, width, height, channels):
    """灰度图升成 RGB24（三通道复制）。协议只接受 RGB24/BGR24。"""
    if channels == 3:
        return pixels
    out = bytearray(width * height * 3)
    for i, value in enumerate(pixels):
        out[i * 3 + 0] = value
        out[i * 3 + 1] = value
        out[i * 3 + 2] = value
    return bytes(out)


def run_entry(entry, args, pkg_dir, stdin_bytes=None, timeout=120):
    if not os.path.isfile(entry):
        raise ProbeError(f"package entry not found: {entry}")
    if not os.access(entry, os.X_OK):
        raise ProbeError(f"package entry is not executable: {entry}")
    return subprocess.run(
        [entry] + args,
        input=stdin_bytes,
        capture_output=True,
        timeout=timeout,
        # 包按 CWD（以及自身 exe 目录）定位 models/：CWD 必须是包根。
        # 安装布局里包根 == entry 目录；开发布局是 <pkg>/build/<id>，所以 --pkg-dir 可覆盖。
        cwd=os.path.abspath(pkg_dir),
    )


def last_json_line(raw):
    text = raw.decode(errors="replace") if isinstance(raw, (bytes, bytearray)) else raw
    for line in reversed(text.strip().splitlines()):
        line = line.strip()
        if line.startswith("{"):
            try:
                return json.loads(line)
            except json.JSONDecodeError:
                continue
    return None


def probe_verify(entry, pkg_dir, sample_path):
    # 子进程的 CWD 是包根，相对样本路径会解析到那儿去 —— 一律绝对化。
    result = run_entry(entry, ["--verify", "--sample", os.path.abspath(sample_path)], pkg_dir)
    report = last_json_line(result.stdout)
    if report is None:
        raise ProbeError(f"--verify produced no JSON report (exit {result.returncode})\n"
                         f"stderr tail: {result.stderr.decode(errors='replace')[-400:]}")
    if result.returncode != 0 or report.get("status") != "passed":
        raise ProbeError(f"--verify failed (exit {result.returncode}): {report}")
    print(f"[verify] status={report.get('status')} result_type={report.get('result_type')} "
          f"count={report.get('count')} sample_decoded={report.get('sample_decoded')}")
    if not report.get("sample_decoded"):
        # 不是失败，但必须说清楚这次「通过」到底证明了什么。
        print("[verify] ⚠️  sample_decoded=false：只证明了 ABI 生命周期，样本没有被解码进模型。"
              " 用 netpbm（.ppm/.pgm）样本才能测到真实推理。")
    return report


def probe_extract_feature(entry, pkg_dir, image_path):
    pixels, width, height, channels = read_netpbm(image_path)
    if width > MAX_DIMENSION or height > MAX_DIMENSION:
        raise ProbeError(f"{image_path}: {width}x{height} exceeds the {MAX_DIMENSION} limit")
    rgb = to_rgb24(pixels, width, height, channels)
    stride = width * 3
    header = struct.pack("<7I", FRAME_MAGIC, 1, width, height, stride, FORMAT_RGB24, len(rgb))

    result = run_entry(entry, ["--extract-feature"], pkg_dir, stdin_bytes=header + rgb)
    reply = last_json_line(result.stdout)
    if reply is None:
        raise ProbeError(f"--extract-feature produced no JSON line (exit {result.returncode})\n"
                         f"stderr tail: {result.stderr.decode(errors='replace')[-400:]}")
    if reply.get("status") != "passed":
        # 可选能力：非人脸类算法包本来就不实现它，如实返回 UNSUPPORTED 是**正确**行为。
        # 但要注意：一旦把该包配成宿主的 [person].extractor_algorithm，底库登记就会失败。
        if reply.get("code") == "UNSUPPORTED" and result.returncode == 5:
            print("[extract-feature] SKIPPED: 本包未提供底库登记能力（abi.json capability=optional）。"
                  " 若要用它做底库登记，需要在包内定义 zhulong_face_extract_feature。")
            return None
        raise ProbeError(f"--extract-feature failed: {reply}")

    dim = reply.get("feature_dim")
    features = reply.get("features") or []
    if dim != len(features):
        raise ProbeError(f"feature_dim={dim} does not match {len(features)} values")
    if dim != EXPECTED_FEATURE_DIM:
        raise ProbeError(f"expected {EXPECTED_FEATURE_DIM}-dim features, got {dim}")
    norm = sum(v * v for v in features) ** 0.5
    if abs(norm - 1.0) > 1e-2:
        # 宿主按余弦相似度比对：未归一化的向量会污染整个底库。
        raise ProbeError(f"feature vector is not L2-normalised (norm={norm:.4f})")
    print(f"[extract-feature] request_id={reply.get('request_id')} dim={dim} "
          f"norm={norm:.6f} head={[round(v, 4) for v in features[:4]]}")
    return reply


def main(argv):
    parser = argparse.ArgumentParser(description="算法包自查（--verify / --extract-feature）")
    parser.add_argument("--entry", required=True, help="包根可执行文件（<pkg>/<manifest.id>）")
    parser.add_argument("--pkg-dir",
                        help="包根目录（含 manifest.json 与 models/）；默认取 --entry 所在目录。"
                             "开发布局（<pkg>/build/<id>）需要显式指定 <pkg>。")
    parser.add_argument("--sample", help="--verify 用的样本（netpbm .ppm/.pgm）")
    parser.add_argument("--image", help="--extract-feature 用的整图（netpbm .ppm/.pgm）")
    parser.add_argument("--verify", action="store_true", help="跑安装期黑盒验包")
    parser.add_argument("--extract-feature", action="store_true", help="跑底库登记特征提取")
    parser.add_argument("--all", action="store_true", help="两种都跑")
    args = parser.parse_args(argv)

    if not args.verify and not args.extract_feature and not args.all:
        parser.error("选择 --verify / --extract-feature / --all 之一")

    pkg_dir = args.pkg_dir or os.path.dirname(os.path.abspath(args.entry))
    failures = 0
    if args.verify or args.all:
        if not args.sample:
            parser.error("--verify 需要 --sample <file>")
        try:
            probe_verify(args.entry, pkg_dir, args.sample)
        except ProbeError as error:
            print(f"[verify] FAILED: {error}", file=sys.stderr)
            failures += 1

    if args.extract_feature or args.all:
        if not args.image:
            parser.error("--extract-feature 需要 --image <file>")
        try:
            probe_extract_feature(args.entry, pkg_dir, args.image)
        except ProbeError as error:
            print(f"[extract-feature] FAILED: {error}", file=sys.stderr)
            failures += 1

    if failures:
        return 1
    print("[✓] 协议自查通过")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
