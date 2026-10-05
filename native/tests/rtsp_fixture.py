#!/usr/bin/env python3
"""Loopback RTSP/RTP 测试桩服务

职责与设计：
  1. 为 Native C++ 引擎与静态 FFmpeg 提供轻量级本地 RTSP 回环服务器（非 Mock，走真实 socket 交互）。
  2. 支持标准 RTSP 命令时序：OPTIONS -> DESCRIBE -> SETUP -> PLAY -> TEARDOWN。
  3. 支持 RTP over TCP（交织模式 interleaved）与标准 RTP/UDP 单播推流。
  4. 支持模拟各种异常网络与边缘用例：
     - /stall-open：连接挂起不响应；
     - /stall-info：SDP 缺失关键参数或挂起；
     - /stall-read：播放后断流；
     - /large-extra：超大 SPS/PPS 参数集；
     - /unsupported：不支持的编码格式（如 VP8）；
     - 凭据安全性：校验标准输出与错误输出中是否泄露 URL 敏感账号密码。
"""
import base64
import collections
import json
from pathlib import Path
import re
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


class Server(socketserver.ThreadingTCPServer):
    """支持多线程并发连接的本地 RTSP 测试服务器"""
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), Handler)
        self.lock = threading.Lock()
        self.connections = collections.Counter()  # 记录各路径总连接数
        self.active = collections.Counter()       # 记录各路径活跃连接数
        self.maximum = collections.Counter()      # 记录各路径最大并发连接数
        self.transports = collections.Counter()   # 记录使用的传输协议统计 (tcp/udp)


class Handler(socketserver.BaseRequestHandler):
    """处理单条 RTSP TCP 客户端连接的协议状态机"""

    def setup(self):
        self.done = threading.Event()
        self.send_lock = threading.Lock()
        self.path = None
        self.sender = None
        self.udp = None
        self.target = None
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

            # 构造 SDP 媒体描述
            if self.codec == "h264":
                name = "H264"
                parameters = ",".join(FRAMES[self.codec][:2] * (24 if "large-extra" in path else 1))
                fmtp = f"packetization-mode=1;sprop-parameter-sets={parameters}"
            else:
                name = "H265"
                fmtp = ";".join(f"sprop-{kind}={value}" for kind, value in zip(("vps", "sps", "pps"), FRAMES[self.codec]))

            if "unsupported" in path:
                name, fmtp = "VP8", ""
            if "stall-info" in path:
                fmtp = "packetization-mode=1"

            sdp = ("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=fixture\r\n"
                   "c=IN IP4 127.0.0.1\r\nt=0 0\r\na=control:*\r\n"
                   f"m=video 0 RTP/AVP 96\r\na=rtpmap:96 {name}/90000\r\n"
                   f"a=fmtp:96 {fmtp}\r\na=control:trackID=0\r\n").encode()
            self.reply(sequence, f"Content-Type: application/sdp\r\nContent-Base: {url}/\r\n", sdp)
        elif method == "SETUP":
            transport = headers["transport"]
            if "TCP" in transport:
                value = "RTP/AVP/TCP;unicast;interleaved=0-1"
                key = "tcp"
            else:
                # 解析客户端 UDP 端口并绑定本地发送套接字
                ports = re.search(r"client_port=(\d+)-(\d+)", transport)
                self.udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                self.udp.bind(("127.0.0.1", 0))
                self.target = (self.client_address[0], int(ports[1]))
                port = self.udp.getsockname()[1]
                value = f"RTP/AVP;unicast;client_port={ports[1]}-{ports[2]};server_port={port}-{port + 1}"
                key = "udp"
            with self.server.lock:
                self.server.transports[key] += 1
            self.reply(sequence, f"Transport: {value}\r\n")
        elif method == "PLAY":
            self.reply(sequence, f"Range: npt=0.000-\r\nRTP-Info: url={url}/trackID=0;seq=1;rtptime=0\r\n")
            # 启动推流线程
            self.sender = threading.Thread(target=self.send_frames, daemon=True)
            self.sender.start()
        elif method == "TEARDOWN" and "stall-close" in path:
            # 故意不回复 TEARDOWN，用于验证客户端是否具备优雅超时关闭保护
            pass
        else:
            self.reply(sequence)

    def send_frames(self):
        """按 25fps（每帧 40ms）向客户端发送封装后的 RTP 视频数据包"""
        sequence = 1
        frame = 0
        try:
            while not self.done.is_set():
                if "stall-info" in self.path or ("stall-read" in self.path and frame >= 55):
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
                self.done.wait(0.04)
        except (ConnectionError, OSError):
            pass

    def finish(self):
        self.done.set()
        if self.sender:
            self.sender.join(timeout=1)
        if self.udp:
            self.udp.close()
        if self.path:
            with self.server.lock:
                self.server.active[self.path] -= 1


def main():
    server = Server()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        # 启动测试可执行程序并连接测试桩 RTSP 地址
        result = subprocess.run([sys.argv[1], f"rtsp://127.0.0.1:{server.server_address[1]}"],
                                text=True, capture_output=True, timeout=80)
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
            assert not any(server.active.values()), "socket left open after engine destruction"
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    return 0


if __name__ == "__main__":
    sys.exit(main())
