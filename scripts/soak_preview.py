#!/usr/bin/env python3
"""预览 + 按需音频通道压测客户端。

用途：在一台机器上稳定地开 N 条预览 WebSocket（其中 K 条再开一条音频 WebSocket），
把帧率、码率、序号空洞与首帧耗时量化下来 —— 板端压测（父任务 Phase 5.4）需要
「完全不收听」与「1~4 路收听」两份可对照的数据，靠人肉点浏览器给不出可复现的数。

只依赖标准库：压测机和板端都不该为了跑一次压测去装 requests / websockets。
WebSocket 手写在这里是刻意的 —— 需要的只是「握手 + 读帧 + 回 pong」，
用现成库反而要解释它的重连策略与缓冲策略，而这两项正是被测对象。

用法：
  # 16 路预览，其中 4 路收听音频，跑 30 分钟
  scripts/soak_preview.py --base-url http://192.168.1.50:8000 \\
      --preview 16 --audio 4 --duration 1800 --label audio --json /tmp/soak-audio.json

  # 对照组：同样 16 路预览，一条音频都不建
  scripts/soak_preview.py --base-url http://192.168.1.50:8000 \\
      --preview 16 --audio 0 --duration 1800 --label baseline --json /tmp/soak-baseline.json

前置：目标宿主已初始化管理员（`--username/--password`），并且已存在至少
`--preview` 路相机；相机按列表顺序取用（`--camera-prefix` 可只取前缀匹配的）。

输出的 JSON 字段：
  - 每条连接的 frames / bytes / seq_gaps / first_frame_ms / close；
  - 汇总 steady-state fps、Mbps、音频帧率、错误计数；
  - `windows`：每 30 s 一格的时间序列，用于画漂移曲线（对照「有无收听」的斜率）。
"""

import argparse
import base64
import json
import os
import socket
import struct
import sys
import threading
import time
import urllib.error
import urllib.request

WIRE_MAGIC_ZLM2 = 0x5A4C4D32
WIRE_HEADER_SIZE = 32
AUDIO_MAGIC_ZLA1 = 0x5A4C4131
AUDIO_HEADER_SIZE = 20


# --------------------------------------------------------------------------- HTTP


class HttpClient:
    """极简 cookie 客户端：登录一次，之后所有请求与 WS 握手复用这个会话。"""

    def __init__(self, base_url, username, password):
        self.base_url = base_url.rstrip("/")
        self.cookies = {}
        self.username = username
        self.password = password

    def _request(self, method, path, payload=None):
        data = None
        headers = {"Accept": "application/json"}
        if payload is not None:
            data = json.dumps(payload).encode()
            headers["Content-Type"] = "application/json"
        if self.cookies:
            headers["Cookie"] = "; ".join(f"{k}={v}" for k, v in self.cookies.items())
        request = urllib.request.Request(self.base_url + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                body = response.read()
                for header in response.headers.get_all("Set-Cookie") or []:
                    name, _, rest = header.partition("=")
                    value = rest.split(";", 1)[0]
                    self.cookies[name.strip()] = value
        except urllib.error.HTTPError as error:
            body = error.read()
        payload = json.loads(body)
        if payload.get("code") != "OK":
            raise RuntimeError(f"{method} {path} → {payload.get('code')}: {payload.get('message')}")
        return payload.get("data")

    def login(self):
        status = self._request("GET", "/api/v1/auth/status")
        if not status.get("initialized"):
            self._request("POST", "/api/v1/auth/init", {
                "username": self.username,
                "password": self.password,
                "confirmPassword": self.password,
            })
            return
        self._request("POST", "/api/v1/auth/login", {
            "username": self.username,
            "password": self.password,
        })

    @property
    def cookie_header(self):
        return "; ".join(f"{k}={v}" for k, v in self.cookies.items())

    def cameras(self):
        page = self._request("GET", "/api/v1/cameras?pageSize=100")
        return page.get("items", [])


# --------------------------------------------------------------------- WebSocket


def ws_connect(host, port, path, cookie_header, timeout=10):
    """手工完成一次 WebSocket 升级：只需要一个「能一直读二进制帧」的连接。"""
    sock = socket.create_connection((host, port), timeout=timeout)
    key = base64.b64encode(os.urandom(16)).decode()
    request = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        f"Cookie: {cookie_header}\r\n\r\n"
    )
    sock.sendall(request.encode())
    buffer = b""
    while b"\r\n\r\n" not in buffer:
        chunk = sock.recv(4096)
        if not chunk:
            raise RuntimeError("websocket handshake: connection closed")
        buffer += chunk
    head, _, rest = buffer.partition(b"\r\n\r\n")
    status_line = head.split(b"\r\n", 1)[0].decode(errors="replace")
    if " 101 " not in status_line + " ":
        raise RuntimeError(f"websocket handshake failed: {status_line}")
    return sock, rest


def _read_exact(sock, size, pending):
    data, pending = pending, b""
    while len(data) < size:
        chunk = sock.recv(max(4096, size - len(data)))
        if not chunk:
            raise ConnectionError("closed")
        data += chunk
    return data[:size], data[size:]


def ws_read_frame(sock, pending):
    """返回 (opcode, payload, pending)。服务端帧不掩码。"""
    header, pending = _read_exact(sock, 2, pending)
    opcode = header[0] & 0x0F
    masked = header[1] & 0x80
    length = header[1] & 0x7F
    if length == 126:
        raw, pending = _read_exact(sock, 2, pending)
        length = struct.unpack("!H", raw)[0]
    elif length == 127:
        raw, pending = _read_exact(sock, 8, pending)
        length = struct.unpack("!Q", raw)[0]
    mask = b""
    if masked:
        mask, pending = _read_exact(sock, 4, pending)
    payload, pending = _read_exact(sock, length, pending)
    if masked:
        payload = bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
    return opcode, payload, pending


def ws_send_masked(sock, opcode, payload=b""):
    mask = os.urandom(4)
    masked = bytes(byte ^ mask[index % 4] for index, byte in enumerate(payload))
    header = bytes([0x80 | opcode])
    if len(payload) < 126:
        header += bytes([0x80 | len(payload)])
    elif len(payload) < 65536:
        header += bytes([0x80 | 126]) + struct.pack("!H", len(payload))
    else:
        header += bytes([0x80 | 127]) + struct.pack("!Q", len(payload))
    sock.sendall(header + mask + masked)


# ------------------------------------------------------------------------ 连接


class Connection:
    """一条 WS 连接的全部观测值。字段名即 JSON 字段名。"""

    def __init__(self, name, kind, host, port, path, cookie_header):
        self.name = name
        self.kind = kind
        self.host = host
        self.port = port
        self.path = path
        self.cookie_header = cookie_header
        self.lock = threading.Lock()
        self.frames = 0
        self.bytes = 0
        self.text_frames = 0
        self.seq_gaps = 0
        self.protocol_errors = 0
        self.audio_frames = 0
        self.audio_bytes = 0
        self.audio_discontinuities = 0
        self.audio_synthetic_pts = 0
        self.audio_info = None
        self.close = None
        self.first_frame_ms = None
        self.last_seq = None
        self.samples = []  # (t, frames, bytes)
        self.started = None
        self.sock = None
        self.stop = threading.Event()
        self.thread = None

    def start(self):
        self.started = time.monotonic()
        self.thread = threading.Thread(target=self._run, name=f"ws-{self.name}", daemon=True)
        self.thread.start()

    def _record(self, frames, payload_bytes, first_marker=True):
        now = time.monotonic()
        with self.lock:
            self.frames += frames
            self.bytes += payload_bytes
            if first_marker and self.first_frame_ms is None:
                self.first_frame_ms = round((now - self.started) * 1000, 1)
            self.samples.append((now - self.started, self.frames, self.bytes))

    def _run(self):
        pending = b""
        try:
            self.sock, pending = ws_connect(self.host, self.port, self.path, self.cookie_header)
            self.sock.settimeout(30)
            while not self.stop.is_set():
                try:
                    opcode, payload, pending = ws_read_frame(self.sock, pending)
                except socket.timeout:
                    continue
                if opcode == 0x9:  # ping → pong
                    ws_send_masked(self.sock, 0xA, payload)
                    continue
                if opcode == 0x8:  # close
                    code = struct.unpack("!H", payload[:2])[0] if len(payload) >= 2 else None
                    reason = payload[2:].decode(errors="replace")
                    with self.lock:
                        self.close = {"code": code, "reason": reason}
                    return
                if opcode == 0x1:
                    with self.lock:
                        self.text_frames += 1
                        if self.kind == "audio":
                            self._handle_audio_text(payload)
                    continue
                if opcode == 0x2:
                    self._handle_binary(payload)
        except (ConnectionError, OSError, RuntimeError) as error:
            # 主动关闭时 recv 会以 EBADF / 已关闭套接字返回，这不是被测对象的异常。
            if self.stop.is_set():
                return
            with self.lock:
                if self.close is None:
                    self.close = {"code": None, "reason": f"client: {error}"}

    def _handle_audio_text(self, payload):
        try:
            message = json.loads(payload)
        except ValueError:
            self.protocol_errors += 1
            return
        if message.get("type") == "audio_info":
            self.audio_info = message
        elif message.get("type") == "error":
            self.close = {"code": None, "reason": f"server: {message.get('code')}"}

    def _handle_binary(self, payload):
        if self.kind == "preview":
            if len(payload) < WIRE_HEADER_SIZE or struct.unpack("!I", payload[:4])[0] != WIRE_MAGIC_ZLM2:
                with self.lock:
                    self.protocol_errors += 1
                return
            seq = struct.unpack("!I", payload[24:28])[0]
            with self.lock:
                if self.last_seq is not None and seq != (self.last_seq + 1) % 2**32:
                    self.seq_gaps += 1
                self.last_seq = seq
            self._record(1, len(payload))
            return

        if len(payload) < AUDIO_HEADER_SIZE or struct.unpack("!I", payload[:4])[0] != AUDIO_MAGIC_ZLA1:
            with self.lock:
                self.protocol_errors += 1
            return
        flags = payload[5]
        with self.lock:
            self.audio_frames += 1
            self.audio_bytes += len(payload) - AUDIO_HEADER_SIZE
            if flags & 0x01:
                self.audio_discontinuities += 1
            if flags & 0x02:
                self.audio_synthetic_pts += 1
        self._record(1, len(payload) - AUDIO_HEADER_SIZE)

    def snapshot(self):
        with self.lock:
            return {
                "name": self.name,
                "kind": self.kind,
                "path": self.path,
                "frames": self.frames,
                "payload_bytes": self.bytes,
                "text_frames": self.text_frames,
                "seq_gaps": self.seq_gaps,
                "protocol_errors": self.protocol_errors,
                "first_frame_ms": self.first_frame_ms,
                "close": self.close,
                "audio_info": self.audio_info,
                "audio_frames": self.audio_frames,
                "audio_payload_bytes": self.audio_bytes,
                "audio_discontinuities": self.audio_discontinuities,
                "audio_synthetic_pts": self.audio_synthetic_pts,
            }

    def stop_and_close(self):
        self.stop.set()
        try:
            if self.sock is not None:
                ws_send_masked(self.sock, 0x8, struct.pack("!H", 1000))
                self.sock.close()
        except OSError:
            pass
        if self.thread is not None:
            self.thread.join(timeout=3)


# ------------------------------------------------------------------------- 主流程


def split_url(base_url):
    without_scheme = base_url.split("://", 1)[1]
    host, _, port = without_scheme.partition("/")[0].partition(":")
    return host, int(port or 80)


def summarize(connections, duration, label):
    preview = [c.snapshot() for c in connections if c.kind == "preview"]
    audio = [c.snapshot() for c in connections if c.kind == "audio"]
    # 分母用整段时长：建连那几秒的帧数也在总数里，把两头都扣掉只会让「帧率」变成一个
    # 既有前 5 秒被扣、又被计入总数的四不像。稳态看 `windows` 的时间序列。
    steady_window = max(duration, 1.0)

    def rate(entries, field, bytes_field=None):
        total_frames = sum(entry[field] for entry in entries)
        total_bytes = sum(entry[bytes_field] for entry in entries) if bytes_field else 0
        return {
            "frames": total_frames,
            "frames_per_second": round(total_frames / steady_window, 2),
            "kbit_per_second": round(total_bytes * 8 / 1000 / steady_window, 1) if bytes_field else None,
        }

    return {
        "label": label,
        "duration_s": round(duration, 1),
        "preview": {
            **rate(preview, "frames", "payload_bytes"),
            "connections": len(preview),
            "seq_gaps": sum(entry["seq_gaps"] for entry in preview),
            "protocol_errors": sum(entry["protocol_errors"] for entry in preview),
            "first_frame_ms_max": max((entry["first_frame_ms"] or 0) for entry in preview) if preview else None,
            "unexpected_closes": [
                entry["close"] for entry in preview
                if entry["close"] and entry["close"].get("code") not in (1000, 1001)
            ],
        },
        "audio": {
            **rate(audio, "audio_frames", "audio_payload_bytes"),
            "connections": len(audio),
            "discontinuities": sum(entry["audio_discontinuities"] for entry in audio),
            "synthetic_pts": sum(entry["audio_synthetic_pts"] for entry in audio),
            "capabilities": sorted({json.dumps(entry["audio_info"], sort_keys=True) for entry in audio}),
            "protocol_errors": sum(entry["protocol_errors"] for entry in audio),
            "unexpected_closes": [
                entry["close"] for entry in audio
                if entry["close"] and entry["close"].get("code") not in (1000, 1001)
            ],
        },
        "connections": preview + audio,
    }


def main():
    parser = argparse.ArgumentParser(description="Preview/audio WebSocket soak client")
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="ABCd@123")
    parser.add_argument("--preview", type=int, default=16, help="预览连接数")
    parser.add_argument("--audio", type=int, default=0, help="其中前 N 路再开一条音频连接")
    parser.add_argument("--duration", type=float, default=1800, help="稳态时长（秒）")
    parser.add_argument("--role", default="main")
    parser.add_argument("--label", default="run")
    parser.add_argument("--json", default="", help="汇总写入的 JSON 路径")
    parser.add_argument("--window", type=float, default=30, help="时间序列采样间隔（秒）")
    parser.add_argument("--camera-prefix", default="")
    args = parser.parse_args()

    host, port = split_url(args.base_url)
    client = HttpClient(args.base_url, args.username, args.password)
    client.login()
    cameras = [camera for camera in client.cameras() if camera["id"].startswith(args.camera_prefix)]
    if len(cameras) < args.preview:
        raise SystemExit(f"需要 {args.preview} 路相机，实际只有 {len(cameras)} 路（prefix={args.camera_prefix!r}）")

    # 音频通道按**登录会话**限一条：压测里要同时听 N 路，就必须有 N 个会话，
    # 否则第二条会把第一条以 4001 顶掉 —— 那测到的是接管逻辑，不是并发收听。
    sessions = [client]
    for _ in range(max(args.audio - 1, 0)):
        extra = HttpClient(args.base_url, args.username, args.password)
        extra.login()
        sessions.append(extra)

    established = []
    for index in range(args.preview):
        camera = cameras[index]
        session = sessions[index] if index < args.audio else client
        preview = Connection(
            f"preview-{camera['id']}", "preview", host, port,
            f"/api/v1/cameras/{camera['id']}/streams/{args.role}/ws", session.cookie_header,
        )
        preview.start()
        established.append(preview)
        if index < args.audio:
            audio = Connection(
                f"audio-{camera['id']}", "audio", host, port,
                f"/api/v1/cameras/{camera['id']}/streams/{args.role}/audio/ws", session.cookie_header,
            )
            audio.start()
            established.append(audio)
        time.sleep(0.05)  # 依次建连：同时打 20 条 TCP 会让首帧耗时失去意义

    windows = []
    started = time.monotonic()
    next_window = started + args.window
    try:
        while True:
            time.sleep(0.5)
            now = time.monotonic()
            if now - started >= args.duration:
                break
            if now >= next_window:
                next_window += args.window
                snapshots = [c.snapshot() for c in established]
                windows.append({
                    "t_s": round(now - started, 1),
                    "preview_frames": sum(s["frames"] for s in snapshots if s["kind"] == "preview"),
                    "audio_frames": sum(s["audio_frames"] for s in snapshots if s["kind"] == "audio"),
                })
    except KeyboardInterrupt:
        pass
    finally:
        duration = time.monotonic() - started
        for connection in established:
            connection.stop_and_close()

    summary = summarize(established, duration, args.label)
    summary["windows"] = windows
    text = json.dumps(summary, ensure_ascii=False, indent=2)
    if args.json:
        os.makedirs(os.path.dirname(args.json) or ".", exist_ok=True)
        with open(args.json, "w") as handle:
            handle.write(text)
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
