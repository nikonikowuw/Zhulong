#!/usr/bin/env python3
"""按需音频的降级与回滚演练（父任务 Phase 5.5 / AC-2、AC-4 的证据）。

每个演练只做一件事：用**协议层**的观测（能力帧、WebSocket 关闭码、帧计数）证明某条
降级路径真的成立。不看日志猜、不依赖 UI，也不需要浏览器。

用例：
  1. no_audio    —— 无音轨相机：能力帧 present=false；音频通道先发 error 再以 4404 关闭；
  2. latm        —— MP4A-LATM：能力帧 codec=aac 且真的收到 ZLA1 帧（固定 FFmpeg 剥 LATM）；
  3. broken      —— 音频 SETUP 失败：视频照常出帧，能力帧 present=false（Native 已退纯视频）；
  4. supersede   —— 同一登录会话开第二条音频通道：旧连接以 4001 被接管；
  5. dual        —— 两个登录会话各听一路：两条都在出帧（互不影响）；
  6. limit       —— 全局 8 条上限：第 9 个会话被 4002 拒绝；
  7. old_client  —— 忽略文本帧的旧客户端：二进制帧序列不受能力帧影响（等价于旧 bundle）。

用法（对一台已初始化的宿主；相机需预先建好并指向对应桩位）：
  scripts/audio_drill.py --base-url http://192.168.19.71:8000 \\
      --plain-camera soak01 --audio-camera soak13 --audio-camera2 soak14 \\
      --latm-camera latm1 --broken-camera broken1 \\
      --json .trellis/tasks/11-03-dual-track-soak/research/board-rollback-drill.json

缺哪台相机就跳过哪条用例（报告里标 skipped），这样现场只有一台真机时也能跑
「无音频」与「接管」两条。
"""

import argparse
import importlib.util
import json
import os
import struct
import sys
import time
from pathlib import Path

_SPEC = importlib.util.spec_from_file_location(
    "soak_preview", Path(__file__).with_name("soak_preview.py")
)
soak = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(soak)


class Drill:
    """一条演练连接：只读帧，不做重连（重连逻辑本身不是本演练的对象）。"""

    def __init__(self, host, port, path, cookie):
        self.path = path
        self.close = None
        self.texts = []
        self.binaries = []
        self.sock, self._pending = soak.ws_connect(host, port, path, cookie)
        self.sock.settimeout(0.5)

    def read_for(self, seconds, want_binary=None, want_text=None):
        """读到超时 / 关闭 / 任一 want 满足。不传 want 就是「读满整段时间」。"""
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if self.close is not None:
                return
            if want_binary is not None or want_text is not None:
                satisfied = True
                if want_binary is not None:
                    satisfied = satisfied and len(self.binaries) >= want_binary
                if want_text is not None:
                    satisfied = satisfied and len(self.texts) >= want_text
                if satisfied:
                    return
            try:
                opcode, payload, self._pending = soak.ws_read_frame(self.sock, self._pending)
            except Exception:
                return
            if opcode == 0x9:
                soak.ws_send_masked(self.sock, 0xA, payload)
            elif opcode == 0x8:
                code = struct.unpack("!H", payload[:2])[0] if len(payload) >= 2 else None
                self.close = {"code": code, "reason": payload[2:].decode(errors="replace")}
                return
            elif opcode == 0x1:
                try:
                    self.texts.append(json.loads(payload))
                except ValueError:
                    pass
            elif opcode == 0x2:
                self.binaries.append(payload)

    def close_quietly(self):
        try:
            soak.ws_send_masked(self.sock, 0x8, struct.pack("!H", 1000))
        except Exception:
            pass
        try:
            self.sock.close()
        except Exception:
            pass


def camera_id(client, name):
    for camera in client.cameras():
        if camera["name"] == name or camera["id"] == name:
            return camera["id"]
    return None


def run_case(name, fn):
    started = time.monotonic()
    try:
        return {"case": name, "ok": True, "detail": fn(),
                "elapsed_ms": round((time.monotonic() - started) * 1000)}
    except AssertionError as error:
        return {"case": name, "ok": False, "error": str(error),
                "elapsed_ms": round((time.monotonic() - started) * 1000)}
    except Exception as error:  # noqa: BLE001 - 演练要如实记录任何异常
        return {"case": name, "ok": False, "error": f"{type(error).__name__}: {error}",
                "elapsed_ms": round((time.monotonic() - started) * 1000)}


def main():
    parser = argparse.ArgumentParser(description="On-demand audio degradation drill")
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="ABCd@123")
    parser.add_argument("--plain-camera", default="soak01", help="无音轨相机")
    parser.add_argument("--audio-camera", default="soak13", help="G.711 音频相机")
    parser.add_argument("--audio-camera2", default="soak14", help="第二台音频相机（双会话用例）")
    parser.add_argument("--latm-camera", default="latm1")
    parser.add_argument("--broken-camera", default="broken1")
    parser.add_argument("--json", default="")
    args = parser.parse_args()

    host, port = soak.split_url(args.base_url)

    def login():
        client = soak.HttpClient(args.base_url, args.username, args.password)
        client.login()
        return client

    admin = login()
    ids = {
        "plain": camera_id(admin, args.plain_camera),
        "audio": camera_id(admin, args.audio_camera),
        "audio2": camera_id(admin, args.audio_camera2),
        "latm": camera_id(admin, args.latm_camera),
        "broken": camera_id(admin, args.broken_camera),
    }
    report = {"base_url": args.base_url, "cameras": ids, "cases": []}

    def preview_path(camera):
        return f"/api/v1/cameras/{camera}/streams/main/ws"

    def audio_path(camera):
        return f"/api/v1/cameras/{camera}/streams/main/audio/ws"

    def case_no_audio():
        if not ids["plain"]:
            return {"skipped": "camera missing"}
        preview = Drill(host, port, preview_path(ids["plain"]), admin.cookie_header)
        try:
            preview.read_for(5, want_binary=3, want_text=1)
            info = next((text for text in preview.texts if text.get("type") == "stream_info"), None)
            assert info is not None, "preview must always send stream_info"
            assert info["audio"]["present"] is False, f"plain camera must advertise no audio: {info['audio']}"
            assert len(preview.binaries) >= 3, "video must still flow on a camera without audio"
        finally:
            preview.close_quietly()

        channel = Drill(host, port, audio_path(ids["plain"]), admin.cookie_header)
        try:
            channel.read_for(5)
            assert any(text.get("type") == "error" for text in channel.texts), \
                f"no-audio camera must send an error frame, got {[t.get('type') for t in channel.texts]}"
            assert channel.close is not None, "audio channel must be closed after the error frame"
            assert channel.close.get("code") == 4404, f"expected close 4404, got {channel.close}"
            error = next(text for text in channel.texts if text.get("type") == "error")
            return {"capability": info["audio"], "error_frame": error,
                    "close": channel.close, "video_frames": len(preview.binaries)}
        finally:
            channel.close_quietly()

    def case_latm():
        if not ids["latm"]:
            return {"skipped": "camera missing"}
        channel = Drill(host, port, audio_path(ids["latm"]), admin.cookie_header)
        try:
            channel.read_for(8, want_binary=5)
            info = next((text for text in channel.texts if text.get("type") == "audio_info"), None)
            assert info is not None, \
                f"latm stream must expose audio_info, got {[t.get('type') for t in channel.texts]}"
            assert info["codec"] == "aac", f"latm must be demuxed to aac, got {info['codec']}"
            assert len(channel.binaries) >= 5, f"expected ZLA1 frames, got {len(channel.binaries)}"
            return {"audio_info": info, "frames": len(channel.binaries), "close": channel.close}
        finally:
            channel.close_quietly()

    def case_broken():
        if not ids["broken"]:
            return {"skipped": "camera missing"}
        preview = Drill(host, port, preview_path(ids["broken"]), admin.cookie_header)
        try:
            preview.read_for(6, want_binary=5, want_text=1)
            info = next((text for text in preview.texts if text.get("type") == "stream_info"), None)
            assert info is not None, "preview must send stream_info even when audio SETUP failed"
            assert len(preview.binaries) >= 5, \
                f"video must survive an audio SETUP failure, got {len(preview.binaries)} frames"
            assert info["audio"]["present"] is False, "audio SETUP failure must degrade to no-audio"
            return {"capability": info["audio"], "video_frames": len(preview.binaries)}
        finally:
            preview.close_quietly()

    def case_supersede():
        if not ids["audio"]:
            return {"skipped": "camera missing"}
        first = Drill(host, port, audio_path(ids["audio"]), admin.cookie_header)
        second = None
        try:
            first.read_for(3, want_binary=3)
            assert first.binaries, "first channel must be producing frames before takeover"
            second = Drill(host, port, audio_path(ids["audio"]), admin.cookie_header)
            second.read_for(3, want_binary=2)
            # 接管是**服务端**发起的关闭：必须继续读旧连接才能看到那条 close 帧。
            first.read_for(3)
            assert first.close is not None, "first channel must be closed by the takeover"
            assert first.close.get("code") == 4001, f"expected 4001 superseded, got {first.close}"
            return {"first_close": first.close, "second_frames": len(second.binaries)}
        finally:
            first.close_quietly()
            if second is not None:
                second.close_quietly()

    def case_dual():
        if not ids["audio"] or not ids["audio2"]:
            return {"skipped": "camera missing"}
        other = login()
        left = Drill(host, port, audio_path(ids["audio"]), admin.cookie_header)
        right = Drill(host, port, audio_path(ids["audio2"]), other.cookie_header)
        try:
            left.read_for(5, want_binary=5)
            right.read_for(5, want_binary=5)
            assert left.close is None and right.close is None, \
                f"two sessions must not interfere: {left.close} / {right.close}"
            assert len(left.binaries) >= 5 and len(right.binaries) >= 5, \
                f"both sessions must receive frames: {len(left.binaries)} / {len(right.binaries)}"
            info_left = next((t for t in left.texts if t.get("type") == "audio_info"), None)
            info_right = next((t for t in right.texts if t.get("type") == "audio_info"), None)
            return {"left_frames": len(left.binaries), "right_frames": len(right.binaries),
                    "left_info": info_left, "right_info": info_right}
        finally:
            left.close_quietly()
            right.close_quietly()

    def case_limit():
        if not ids["audio"]:
            return {"skipped": "camera missing"}
        established = []
        try:
            for _ in range(8):
                session = login()
                drift = Drill(host, port, audio_path(ids["audio"]), session.cookie_header)
                drift.read_for(0.5)
                established.append(drift)
            ninth = login()
            rejected = Drill(host, port, audio_path(ids["audio"]), ninth.cookie_header)
            rejected.read_for(4)
            assert rejected.close is not None, "the 9th session must be rejected"
            assert rejected.close.get("code") == 4002, f"expected 4002 limit, got {rejected.close}"
            return {"ninth_close": rejected.close, "established": len(established)}
        finally:
            for drift in established:
                drift.close_quietly()

    def case_old_client():
        if not ids["plain"]:
            return {"skipped": "camera missing"}
        client = Drill(host, port, preview_path(ids["plain"]), admin.cookie_header)
        try:
            client.read_for(5, want_binary=10)
            assert client.binaries, "old clients must still receive binary frames"
            magic = {struct.unpack("!I", payload[:4])[0] for payload in client.binaries}
            assert magic == {soak.WIRE_MAGIC_ZLM2}, f"unexpected wire magic: {magic}"
            return {"text_frames_ignored": len(client.texts), "binary_frames": len(client.binaries)}
        finally:
            client.close_quietly()

    for name, fn in [
        ("no_audio", case_no_audio),
        ("latm", case_latm),
        ("broken", case_broken),
        ("supersede", case_supersede),
        ("dual_session", case_dual),
        ("channel_limit", case_limit),
        ("old_client", case_old_client),
    ]:
        result = run_case(name, fn)
        report["cases"].append(result)
        print(json.dumps(result, ensure_ascii=False), flush=True)

    if args.json:
        os.makedirs(os.path.dirname(args.json) or ".", exist_ok=True)
        with open(args.json, "w") as handle:
            handle.write(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if all(item["ok"] for item in report["cases"]) else 1


if __name__ == "__main__":
    sys.exit(main())
