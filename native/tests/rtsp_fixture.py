#!/usr/bin/env python3
"""Loopback RTSP/RTP 测试桩服务

职责与设计：
  1. 为 Native C++ 引擎与静态 FFmpeg 提供轻量级本地 RTSP 回环服务器（非 Mock，走真实 socket 交互）。
  2. 支持标准 RTSP 命令时序：OPTIONS -> DESCRIBE -> SETUP -> PLAY -> TEARDOWN。
  3. 支持 RTP over TCP（交织模式 interleaved）与标准 RTP/UDP 单播推流。
  4. 支持模拟各种异常网络与边缘用例：
     - /stall-open：连接挂起不响应；
     - /stall-info：SDP 缺失关键参数或挂起；
     - /late-probe：SDP 缺参数集且首帧延迟下发（用于验证快速探测预算的兼容档重试）；
     - /stall-read：播放后断流；
     - /large-extra：超大 SPS/PPS 参数集；
     - /unsupported：不支持的编码格式（如 VP8）；
     - 双轨音频档位（v5）：
         * /audio-pcma、/audio-pcmu：G.711 每 20ms 160 采样 / 8kHz；
         * /audio-aac：mpeg4-generic + AAC-hbr AU header（视频仍 m=video）；
         * /audio-latm：MP4A-LATM（固定 FFmpeg 会将其解包为裸 AAC）；
         * /audio-broken：音频 SETUP 返回失败，用于验证“退纯视频只重试一次”；
         * /stop-audio、/stop-video：只停音频 / 只停视频（验证空闲超时仍只由视频刷新）；
     - 音频轨 SETUP 按 trackID 分配交织通道（视频 0-1、音频 2-3），UDP 模式按轨各开一对端口；
     - 凭据安全性：校验标准输出与错误输出中是否泄露 URL 敏感账号密码。
"""
import base64
import collections
import json
import os
from pathlib import Path
import re
import signal
import socket
import socketserver
import struct
import subprocess
import sys
import threading
import time
from urllib.parse import urlsplit

# 加载测试用 H.264 与 H.265 NAL 数据集
FRAMES = json.loads((Path(__file__).parent / "fixtures/rtp_frames.json").read_text())


class SyncClip:
    """同步测量素材：白闪 H.264（Annex-B）+ 1 kHz beep 的裸 G.711 μ-law。

    为什么不把这套素材写进 ``fixtures/rtp_frames.json``：那段 JSON 是**协议**夹具，
    几 KB、可读、只关心 NAL 结构；同步素材是几分钟的码流（数百 KB），每次生成的
    比特流都随本机 x264 版本变化，塞进版本库只会变成一个没人敢动的二进制包袱。
    生成命令见 ``native/tests/make_sync_clip.sh``，两边（宿主机 / 板端）都能现场生成。
    """

    def __init__(self, video_path, audio_path):
        nals = parse_annex_b(Path(video_path).read_bytes())
        if not nals:
            raise ValueError(f"no Annex-B NAL units in {video_path}")
        self.sps = next((nal for nal in nals if nal[0] & 0x1F == 7), None)
        self.pps = next((nal for nal in nals if nal[0] & 0x1F == 8), None)
        if self.sps is None or self.pps is None:
            raise ValueError("sync clip must carry in-band SPS/PPS (x264 repeat-headers)")
        self.aus = split_access_units(nals)
        self.audio = Path(audio_path).read_bytes()
        if not self.audio:
            raise ValueError(f"empty audio payload in {audio_path}")

    @property
    def profile_level_id(self):
        """SPS 里的 profile / constraint / level 三元组（fmtp 的 profile-level-id）。"""
        return self.sps[1:4].hex()

    @property
    def sprop(self):
        return ",".join(base64.b64encode(nal).decode() for nal in (self.sps, self.pps))


def parse_annex_b(data):
    """把 Annex-B 字节流切成 NAL 单元（去掉起始码）。"""
    units = []
    offset = 0
    length = len(data)
    while offset < length:
        start = data.find(b"\x00\x00\x01", offset)
        if start < 0:
            break
        begin = start + 3
        end = data.find(b"\x00\x00\x01", begin)
        if end < 0:
            end = length
        # 四字节起始码 00 00 00 01 会在上一段尾部留下一个 0，属于分隔符而不是负载
        while end > begin and data[end - 1] == 0:
            end -= 1
        if end > begin:
            units.append(data[begin:end])
        offset = begin
    return units


def split_access_units(nals):
    """按「已有一帧片（slice）后遇到新的 slice」切分访问单元。

    只看 slice 而不看 SPS/PPS/SEI：带内参数集一般紧跟在一个 IDR 前面，跟上一个
    访问单元共用 RTP 时间戳也不会破坏解码（它仍然在 IDR 之前到达）。反过来，
    按 SPS 切反而会把同一帧的参数集和 slice 分成两帧。
    """
    units = []
    current = []
    has_slice = False
    for nal in nals:
        nal_type = nal[0] & 0x1F
        is_slice = nal_type in (1, 5)
        if is_slice and has_slice:
            units.append(current)
            current = []
            has_slice = False
        current.append(nal)
        if is_slice:
            has_slice = True
    if current:
        units.append(current)
    return units


class Server(socketserver.ThreadingTCPServer):
    """支持多线程并发连接的本地 RTSP 测试服务器"""
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, host="127.0.0.1", port=0, sync_clip=None):
        super().__init__((host, port), Handler)
        self.lock = threading.Lock()
        self.connections = collections.Counter()  # 记录各路径总连接数
        self.active = collections.Counter()       # 记录各路径活跃连接数
        self.maximum = collections.Counter()      # 记录各路径最大并发连接数
        self.transports = collections.Counter()   # 记录使用的传输协议统计 (tcp/udp)
        self.sync_clip = sync_clip                # 可选：白闪 + beep 素材（--sync-clip）


class Handler(socketserver.BaseRequestHandler):
    """处理单条 RTSP TCP 客户端连接的协议状态机"""

    def setup(self):
        self.done = threading.Event()
        self.send_lock = threading.Lock()
        self.path = None
        self.sender = None
        self.audio_sender = None
        self.udp = None
        self.target = None
        self.audio_udp = None
        self.audio_target = None
        self.audio_type = None
        self.request.settimeout(0.2)

    def reply(self, sequence, headers="", body=b"", code="200 OK"):
        """构造并回复标准 RTSP 响应报文"""
        message = (f"RTSP/1.0 {code}\r\nCSeq: {sequence}\r\nSession: fixture\r\n"
                   f"{headers}Content-Length: {len(body)}\r\n\r\n").encode() + body
        with self.send_lock:
            self.request.sendall(message)

    def handle(self):
        """解析客户端 RTSP 请求报文主循环"""
        buffer = b""
        try:
            while not self.done.is_set():
                try:
                    data = self.request.recv(65536)
                except socket.timeout:
                    continue
                if not data:
                    return
                buffer += data
                while buffer:
                    # 忽略客户端发送的交织 RTCP 帧 ($ 开始)
                    if buffer[0] == 36:
                        if len(buffer) < 4:
                            break
                        size = struct.unpack("!H", buffer[2:4])[0]
                        if len(buffer) < size + 4:
                            break
                        buffer = buffer[size + 4:]
                        continue
                    if b"\r\n\r\n" not in buffer:
                        break
                    request, buffer = buffer.split(b"\r\n\r\n", 1)
                    lines = request.decode().split("\r\n")
                    method, url, _ = lines[0].split(" ")
                    headers = dict(line.split(":", 1) for line in lines[1:] if ":" in line)
                    headers = {key.lower(): value.strip() for key, value in headers.items()}
                    self.command(method, url, headers)
        except (ConnectionError, OSError):
            pass

    def command(self, method, url, headers):
        """根据 RTSP 命令类型响应相应的协议流程"""
        sequence = headers["cseq"]
        path = urlsplit(url).path

        # 模拟建连阻塞用例
        if "stall-open" in path:
            return

        if method == "OPTIONS":
            self.reply(sequence, "Public: OPTIONS, DESCRIBE, SETUP, PLAY, TEARDOWN, GET_PARAMETER\r\n")
        elif method == "DESCRIBE":
            self.path = path
            with self.server.lock:
                self.server.connections[path] += 1
                self.server.active[path] += 1
                self.server.maximum[path] = max(self.server.maximum[path], self.server.active[path])

            self.codec = "h265" if "h265" in path else "h264"
            self.nals = [base64.b64decode(nal) for nal in FRAMES[self.codec]]
            # sync 档位：真正的白闪画面 + 1 kHz beep（素材由 make_sync_clip.sh 生成）
            self.sync_clip = self.server.sync_clip if "sync" in path else None

            # 构造 SDP 媒体描述
            if self.sync_clip is not None:
                name = "H264"
                fmtp = (f"packetization-mode=1;profile-level-id={self.sync_clip.profile_level_id};"
                        f"sprop-parameter-sets={self.sync_clip.sprop}")
            elif self.codec == "h264":
                name = "H264"
                parameters = ",".join(FRAMES[self.codec][:2] * (24 if "large-extra" in path else 1))
                fmtp = f"packetization-mode=1;sprop-parameter-sets={parameters}"
            else:
                name = "H265"
                fmtp = ";".join(f"sprop-{kind}={value}" for kind, value in zip(("vps", "sps", "pps"), FRAMES[self.codec]))

            if "unsupported" in path:
                name, fmtp = "VP8", ""
            if "stall-info" in path or "late-probe" in path:
                fmtp = "packetization-mode=1"

            self.audio_type = None
            audio_sdp = ""
            if self.sync_clip is not None:
                # 同步档位的音频固定为 PCMU：素材是同一次 ffmpeg 生成的裸 μ-law，
                # beep 起点与白闪起点在媒体时间上精确对齐。
                self.audio_type = "pcmu"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 0\r\n"
                    "a=rtpmap:0 PCMU/8000/1\r\n"
                    "a=control:trackID=1\r\n"
                )
            elif "audio-pcma" in path:
                self.audio_type = "pcma"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 8\r\n"
                    "a=rtpmap:8 PCMA/8000/1\r\n"
                    "a=control:trackID=1\r\n"
                )
            elif "audio-pcmu" in path:
                self.audio_type = "pcmu"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 0\r\n"
                    "a=rtpmap:0 PCMU/8000/1\r\n"
                    "a=control:trackID=1\r\n"
                )
            elif "audio-aac" in path:
                self.audio_type = "aac"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 97\r\n"
                    "a=rtpmap:97 mpeg4-generic/44100/2\r\n"
                    "a=fmtp:97 streamtype=5;profile-level-id=1;mode=AAC-hbr;sizelength=13;indexlength=3;indexdeltalength=3;config=1210\r\n"
                    "a=control:trackID=1\r\n"
                )
            elif "audio-latm" in path:
                self.audio_type = "latm"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 97\r\n"
                    "a=rtpmap:97 MP4A-LATM/90000\r\n"
                    "a=fmtp:97 cpresent=0;config=40002410adca00\r\n"
                    "a=control:trackID=1\r\n"
                )
            elif "audio-broken" in path:
                self.audio_type = "broken"
                audio_sdp = (
                    "m=audio 0 RTP/AVP 8\r\n"
                    "a=rtpmap:8 PCMA/8000/1\r\n"
                    "a=control:trackID=1\r\n"
                )

            # 监听地址就是客户端将回连的地址：绑 127.0.0.1 时行为与以前逐字一致，
            # 绑 0.0.0.0（板端压测）时相机看到的是它的对端地址，而不是“去连自己”。
            origin = self.client_address[0]
            sdp = (f"v=0\r\no=- 1 1 IN IP4 {origin}\r\ns=fixture\r\n"
                   f"c=IN IP4 {origin}\r\nt=0 0\r\na=control:*\r\n"
                   f"m=video 0 RTP/AVP 96\r\na=rtpmap:96 {name}/90000\r\n"
                   f"a=fmtp:96 {fmtp}\r\na=control:trackID=0\r\n"
                   f"{audio_sdp}").encode()
            self.reply(sequence, f"Content-Type: application/sdp\r\nContent-Base: {url}/\r\n", sdp)
        elif method == "SETUP":
            is_audio_track = "trackID=1" in url or (self.path and "trackID=1" in path)
            if self.audio_type == "broken" and is_audio_track:
                self.reply(sequence, code="454 Session Not Found")
                return

            transport = headers["transport"]
            if "TCP" in transport:
                if is_audio_track:
                    value = "RTP/AVP/TCP;unicast;interleaved=2-3"
                else:
                    value = "RTP/AVP/TCP;unicast;interleaved=0-1"
                key = "tcp"
            else:
                # 解析客户端 UDP 端口并绑定本地发送套接字
                ports = re.search(r"client_port=(\d+)-(\d+)", transport)
                udp_sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                udp_sock.bind((self.server.server_address[0], 0))
                target = (self.client_address[0], int(ports[1]))
                port = udp_sock.getsockname()[1]
                value = f"RTP/AVP;unicast;client_port={ports[1]}-{ports[2]};server_port={port}-{port + 1}"
                if is_audio_track:
                    self.audio_udp = udp_sock
                    self.audio_target = target
                else:
                    self.udp = udp_sock
                    self.target = target
                key = "udp"
            with self.server.lock:
                self.server.transports[key] += 1
            self.reply(sequence, f"Transport: {value}\r\n")
        elif method == "PLAY":
            rtp_infos = [f"url={url}/trackID=0;seq=1;rtptime=0"]
            if self.audio_type and self.audio_type != "broken":
                rtp_infos.append(f"url={url}/trackID=1;seq=1;rtptime=0")
            self.reply(sequence, f"Range: npt=0.000-\r\nRTP-Info: {','.join(rtp_infos)}\r\n")
            # 启动推流线程
            if "stop-video" not in self.path:
                self.sender = threading.Thread(target=self.send_frames, daemon=True)
                self.sender.start()
            if self.audio_type in ("pcma", "pcmu", "aac", "latm") and "stop-audio" not in self.path:
                self.audio_sender = threading.Thread(target=self.send_audio_frames, daemon=True)
                self.audio_sender.start()
        elif method == "TEARDOWN" and "stall-close" in path:
            # 故意不回复 TEARDOWN，用于验证客户端是否具备优雅超时关闭保护
            pass
        else:
            self.reply(sequence)

    def send_frames(self):
        """向客户端发送封装后的 RTP 视频数据包（前 25 帧极短间隔加速 FFmpeg 探测，随后 25fps 稳态推流）"""
        if self.sync_clip is not None:
            self.send_sync_video()
            return
        sequence = 1
        frame = 0
        try:
            # 首帧延迟 500ms：快速档（300ms 探测窗口）必然读不到参数集，
            # 只有兼容档（1s）才能拿到 SPS/PPS —— 这正是 fallback 路径要用例钉住的形态。
            if "late-probe" in self.path:
                self.done.wait(0.5)
            while not self.done.is_set():
                if "stall-info" in self.path or (self.path == "/stall-read" and frame >= 8):
                    self.done.wait(0.04)
                    continue
                for index, nal in enumerate(self.nals):
                    marker = 128 if index == len(self.nals) - 1 else 0
                    # 组装 RTP 头：V=2, PayloadType=96, Sequence, Timestamp (步长 3600), SSRC=12345
                    packet = struct.pack("!BBHII", 128, marker | 96, sequence % 65536,
                                         (frame * 3600) % 2**32, 12345) + nal
                    sequence += 1
                    if self.udp:
                        self.udp.sendto(packet, self.target)
                    else:
                        with self.send_lock:
                            # TCP 交织模式：$ + channel (0) + 2字节长度 + RTP负载
                            self.request.sendall(b"$\x00" + struct.pack("!H", len(packet)) + packet)
                frame += 1
                interval = 0.001 if frame < 25 else 0.04
                self.done.wait(interval)
        except (ConnectionError, OSError):
            pass

    def send_audio_frames(self):
        """向客户端发送封装后的 RTP 音频数据包"""
        if self.sync_clip is not None:
            self.send_sync_audio()
            return
        sequence = 1
        frame = 0
        raw_aac = bytes.fromhex("de02004c61766336312e31392e31303100422008c11838")
        # AAC-hbr：2 字节 AU-headers-length + 每 AU 13-bit size/index header + 裸 AU
        aac_payload = struct.pack("!H", 16) + struct.pack("!H", len(raw_aac) << 3) + raw_aac
        # MP4A-LATM（RFC 3016）：length-prefixed 子帧序列，无 AU header
        latm_payload = bytes([len(raw_aac)]) + raw_aac

        try:
            while not self.done.is_set():
                if "stop-audio" in self.path:
                    self.done.wait(0.04)
                    continue

                if self.audio_type == "pcma":
                    pt = 8
                    payload = b"\xd5" * 160
                    ts_step = 160
                    sleep_sec = 0.02
                elif self.audio_type == "pcmu":
                    pt = 0
                    payload = b"\xff" * 160
                    ts_step = 160
                    sleep_sec = 0.02
                elif self.audio_type == "aac":
                    pt = 97
                    payload = aac_payload
                    ts_step = 1024
                    sleep_sec = 0.023
                elif self.audio_type == "latm":
                    pt = 97
                    payload = latm_payload
                    ts_step = 1024
                    sleep_sec = 0.023
                else:
                    break

                marker = 128 if self.audio_type in ("aac", "latm") else 0
                packet = struct.pack("!BBHII", 128, marker | pt, sequence % 65536,
                                     (frame * ts_step) % 2**32, 54321) + payload
                sequence += 1

                if self.audio_udp:
                    self.audio_udp.sendto(packet, self.audio_target)
                else:
                    with self.send_lock:
                        # TCP 交织模式：$ + channel (2) + 2字节长度 + RTP负载
                        self.request.sendall(b"$\x02" + struct.pack("!H", len(packet)) + packet)

                frame += 1
                interval = 0.001 if frame < 25 else sleep_sec
                self.done.wait(interval)
        except (ConnectionError, OSError):
            pass

    def send_rtp(self, channel, packet, udp_sock, target):
        """按当前传输模式发出一枚 RTP 包：TCP 用交织通道，UDP 用对应套接字。"""
        if udp_sock:
            udp_sock.sendto(packet, target)
            return
        with self.send_lock:
            # TCP 交织模式：$ + channel + 2 字节长度 + RTP 负载
            self.request.sendall(bytes([36, channel]) + struct.pack("!H", len(packet)) + packet)

    def send_sync_video(self):
        """按 25 fps 发送白闪码流，时间戳直接由帧号推出。

        用「绝对截止时刻」而不是每帧 ``sleep(0.04)``：后者把调度抖动量累加成
        真正的时钟漂移，几分钟后视频相对音频整体偏掉 —— 而这次测的就是音画偏差。
        落后时立即追赶（不补睡），媒体时间由帧号保证，不靠墙钟。
        """
        sequence = 1
        frame = 0
        started = time.monotonic()
        try:
            while not self.done.is_set():
                access_unit = self.sync_clip.aus[frame % len(self.sync_clip.aus)]
                for index, nal in enumerate(access_unit):
                    marker = 128 if index == len(access_unit) - 1 else 0
                    packet = struct.pack("!BBHII", 128, marker | 96, sequence % 65536,
                                         (frame * 3600) % 2**32, 12345) + nal
                    sequence += 1
                    self.send_rtp(0, packet, self.udp, self.target)
                frame += 1
                delay = started + frame * 0.04 - time.monotonic()
                if delay > 0:
                    self.done.wait(delay)
        except (ConnectionError, OSError):
            pass

    def send_sync_audio(self):
        """按 50 pps 发送 beep 音频（每包 160 样本 = 20 ms），与视频同一条媒体时间轴。"""
        sequence = 1
        frame = 0
        chunk = 160
        packets = [self.sync_clip.audio[offset:offset + chunk]
                   for offset in range(0, len(self.sync_clip.audio), chunk)]
        packets = [payload for payload in packets if payload]
        started = time.monotonic()
        try:
            while not self.done.is_set():
                payload = packets[frame % len(packets)]
                packet = struct.pack("!BBHII", 128, 0, sequence % 65536,
                                     (frame * chunk) % 2**32, 54321) + payload
                sequence += 1
                self.send_rtp(2, packet, self.audio_udp, self.audio_target)
                frame += 1
                delay = started + frame * 0.02 - time.monotonic()
                if delay > 0:
                    self.done.wait(delay)
        except (ConnectionError, OSError):
            pass

    def finish(self):
        self.done.set()
        if self.sender:
            self.sender.join(timeout=1)
        if self.audio_sender:
            self.audio_sender.join(timeout=1)
        if self.udp:
            self.udp.close()
        if self.audio_udp:
            self.audio_udp.close()
        if self.path:
            with self.server.lock:
                self.server.active[self.path] -= 1


def serve(host, port, sync_clip):
    """独立监听模式：给宿主机 / 板端压测与浏览器同步实测当相机用。

    为什么需要它：默认入口是「开一个临时端口、拉起一个测试程序、结束后断言」，
    而压测要的是反过来的——服务先活下来，几十个客户端陆续连进来，人在外面看指标。
    两者共用同一个 Handler，不再另写一套桩。
    """
    server = Server(host=host, port=port, sync_clip=sync_clip)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    bound_host, bound_port = server.server_address[:2]
    print(f"RTSP fixture listening on rtsp://{bound_host}:{bound_port}", flush=True)
    if sync_clip is not None:
        print(f"sync stream: /sync  ({len(sync_clip.aus)} video AUs, "
              f"{len(sync_clip.audio) / 8000:.1f}s audio)", flush=True)

    stopping = threading.Event()

    def stop(_signum, _frame):
        stopping.set()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        while not stopping.wait(1):
            pass
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
        with server.lock:
            print("RTSP fixture connections:", dict(server.connections), flush=True)
            print("RTSP fixture maximum:", dict(server.maximum), flush=True)
            print("RTSP fixture transports:", dict(server.transports), flush=True)
            print("RTSP fixture active:", dict(server.active), flush=True)
    return 0


def main():
    if "--serve" in sys.argv[1:]:
        import argparse

        parser = argparse.ArgumentParser(description="Loopback RTSP fixture")
        parser.add_argument("--serve", action="store_true", help="run standalone instead of driving a test binary")
        parser.add_argument("--host", default="127.0.0.1")
        parser.add_argument("--port", type=int, default=8554)
        parser.add_argument("--sync-clip", metavar="PREFIX",
                            help="enable the /sync stream from PREFIX.h264 + PREFIX.pcmu")
        args = parser.parse_args()
        clip = None
        if args.sync_clip:
            clip = SyncClip(f"{args.sync_clip}.h264", f"{args.sync_clip}.pcmu")
        return serve(args.host, args.port, clip)

    server = Server()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    env = os.environ.copy()
    env.setdefault("ZHULONG_STREAM_GRACE_PERIOD_MS", "200")
    try:
        # 启动测试可执行程序并连接测试桩 RTSP 地址
        result = subprocess.run([sys.argv[1], f"rtsp://127.0.0.1:{server.server_address[1]}"],
                                text=True, capture_output=True, timeout=80, env=env)
        sys.stdout.write(result.stdout)
        sys.stderr.write(result.stderr)
        captured = result.stdout + result.stderr

        # 核心安全断言：验证密码凭据未泄漏到日志或输出中
        if "fixture-user" in captured or "fixture-password" in captured:
            raise AssertionError("credential leak from Native/FFmpeg")
        if result.returncode:
            return result.returncode

        time.sleep(0.3)
        with server.lock:
            print("RTSP fixture connections:", dict(server.connections))
            print("RTSP fixture transports:", dict(server.transports))
            assert server.connections["/pool"] == 2, "duplicate connection or failed grace expiry"
            assert server.maximum["/pool"] == 1, "overlapping physical connections"
            assert server.transports["tcp"] and server.transports["udp"]

            # 双轨（v5）连接次数断言：
            # - /audio-broken 必须是 2 次（先 video+audio 建连失败，再纯视频重开一次），
            #   这是“音频失败不得拖垮视频”的物理证据，不是日志自证；
            # - /audio-pcmu 必须仍是 1 次：音频正常时绝不能多付一次重连。
            assert server.connections["/audio-broken"] == 2, \
                f"audio SETUP failure must reopen exactly once, got {server.connections['/audio-broken']}"
            assert server.connections["/audio-pcmu"] == 1, \
                f"working audio must not reconnect, got {server.connections['/audio-pcmu']}"

            assert not any(server.active.values()), "socket left open after engine destruction"
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    return 0


if __name__ == "__main__":
    sys.exit(main())
