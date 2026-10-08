import { describe, expect, it } from 'vitest';
import {
  extractNalus,
  parseZlm1Packet,
  WIRE_FLAG_HAS_DTS,
  WIRE_FLAG_HAS_PTS,
  WIRE_FLAG_KEYFRAME,
  WIRE_MAGIC,
} from './wireParser';

describe('wireParser', () => {
  function createTestPacket(options: {
    codec?: number;
    flags?: number;
    pts?: bigint;
    dts?: bigint;
    payload?: Uint8Array;
    corruptMagic?: boolean;
  }): ArrayBuffer {
    const payload = options.payload || new Uint8Array([0x00, 0x00, 0x00, 0x01, 0x67, 0x42]);
    const buffer = new ArrayBuffer(24 + payload.length);
    const view = new DataView(buffer);

    view.setUint32(0, options.corruptMagic ? 0x12345678 : WIRE_MAGIC, false);
    view.setUint8(4, options.codec ?? 1); // default H.264
    view.setUint8(5, options.flags ?? (WIRE_FLAG_KEYFRAME | WIRE_FLAG_HAS_PTS | WIRE_FLAG_HAS_DTS));
    view.setUint16(6, 0, false);
    view.setBigInt64(8, options.pts ?? 90000n, false);
    view.setBigInt64(16, options.dts ?? 90000n, false);

    const payloadView = new Uint8Array(buffer, 24);
    payloadView.set(payload);

    return buffer;
  }

  it('correctly parses a valid H.264 keyframe ZLM1 packet', () => {
    const buffer = createTestPacket({
      codec: 1,
      flags: WIRE_FLAG_KEYFRAME | WIRE_FLAG_HAS_PTS,
      pts: 180000n, // 2000 ms
      dts: 180000n,
    });

    const parsed = parseZlm1Packet(buffer);
    expect(parsed.codec).toBe('h264');
    expect(parsed.codecByte).toBe(1);
    expect(parsed.isKeyFrame).toBe(true);
    expect(parsed.hasPts).toBe(true);
    expect(parsed.hasDts).toBe(false);
    expect(parsed.pts).toBe(180000n);
    expect(parsed.ptsMs).toBe(2000);
    expect(parsed.payload.length).toBe(6);
  });

  it('correctly parses H.265 non-keyframe packet', () => {
    const buffer = createTestPacket({
      codec: 2,
      flags: WIRE_FLAG_HAS_PTS | WIRE_FLAG_HAS_DTS,
      pts: 90000n,
      dts: 90000n,
    });

    const parsed = parseZlm1Packet(buffer);
    expect(parsed.codec).toBe('h265');
    expect(parsed.codecByte).toBe(2);
    expect(parsed.isKeyFrame).toBe(false);
    expect(parsed.hasPts).toBe(true);
    expect(parsed.hasDts).toBe(true);
  });

  it('throws when buffer is too short', () => {
    const shortBuffer = new ArrayBuffer(20);
    expect(() => parseZlm1Packet(shortBuffer)).toThrow('Buffer too short');
  });

  it('throws on invalid wire magic', () => {
    const corrupted = createTestPacket({ corruptMagic: true });
    expect(() => parseZlm1Packet(corrupted)).toThrow('Invalid ZLM1 wire magic');
  });

  it('extracts multiple NALUs from Annex B stream', () => {
    // 00 00 00 01 SPS 00 00 01 PPS
    const raw = new Uint8Array([
      0x00, 0x00, 0x00, 0x01, 0x67, 0x42,
      0x00, 0x00, 0x01, 0x68, 0xce,
    ]);

    const nalus = extractNalus(raw);
    expect(nalus.length).toBe(2);
    expect(nalus[0]!.length).toBe(6);
    expect(nalus[1]!.length).toBe(5);
  });
});
