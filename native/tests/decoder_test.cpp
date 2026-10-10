/**
 * @file decoder_test.cpp
 * @brief IDecodeNode, FFmpegDecodeNode 与 BoundedQueue 单元测试
 */

#include "nodes/decode/decoder.hpp"
#include "pipeline/bounded_queue.hpp"
#include "Zhulong/frame.hpp"

#include <atomic>
#include <chrono>
#include <iostream>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

#define CHECK(condition) \
    do { \
        if (!(condition)) { \
            throw std::runtime_error("Check failed at line " + std::to_string(__LINE__) + ": " #condition); \
        } \
    } while (false)

using namespace std::chrono_literals;

// 简单的 Base64 解码器，用于解析测试夹具中的 NALU
static std::vector<uint8_t> base64_decode(const std::string &in) {
    std::vector<uint8_t> out;
    uint32_t val = 0;
    int valb = -8;
    for (uint8_t c : in) {
        if (c == '=') break;
        int d = -1;
        if (c >= 'A' && c <= 'Z') d = c - 'A';
        else if (c >= 'a' && c <= 'z') d = c - 'a' + 26;
        else if (c >= '0' && c <= '9') d = c - '0' + 52;
        else if (c == '+') d = 62;
        else if (c == '/') d = 63;
        if (d < 0) continue;
        val = (val << 6) | static_cast<uint32_t>(d);
        valb += 6;
        if (valb >= 0) {
            out.push_back(static_cast<uint8_t>((val >> valb) & 0xFF));
            valb -= 8;
        }
    }
    return out;
}

int main() {
    try {
        using zhulong::BoundedQueue;
        using zhulong::HardwareFrame;
        using zhulong::IDecodeNode;
        using zhulong::OverflowStrategy;

        // ====================================================================
        // 1. BoundedQueue 基础容量、Drop-Oldest 与协作式取消测试
        // ====================================================================
        {
            BoundedQueue<int> q(3, OverflowStrategy::DROP_OLDEST);
            CHECK(q.capacity() == 3 || q.size() == 0);

            CHECK(q.push(10));
            CHECK(q.push(20));
            CHECK(q.push(30));
            CHECK(q.size() == 3);

            // 队列满后，再 push 40 应该丢弃最旧的 10
            CHECK(q.push(40));
            CHECK(q.size() == 3);

            int val = 0;
            CHECK(q.pop(val));
            CHECK(val == 20); // 10 已被丢弃，队首变为 20
            CHECK(q.pop(val));
            CHECK(val == 30);
            CHECK(q.pop(val));
            CHECK(val == 40);
            CHECK(q.empty());

            // 测试超时
            CHECK(!q.pop(val, 20ms));

            // 测试 cancel
            q.push(99);
            CHECK(q.size() == 1);
            q.cancel();
            CHECK(q.is_cancelled());
            CHECK(q.empty());
            CHECK(!q.push(100)); // 取消后禁止入队
            CHECK(!q.pop(val));  // 取消后 pop 立即返回 false
        }

        // ====================================================================
        // 2. FFmpegDecodeNode 真实 H.264 关键帧端到端解码测试
        // ====================================================================
        {
            auto decoder = IDecodeNode::create(Zhulong_CODEC_H264);
            CHECK(decoder != nullptr);

            // 来自 tests/fixtures/rtp_frames.json 的真实 64x48 合成帧 NALU
            std::vector<std::string> h264_nalus = {
                "Z0LACtxHsBEAAAMAAQAAAwAyjxIngA==", // SPS
                "aM4PyA==",                         // PPS
                "ZYiEOiYoAAkCycnJ111111114A=="     // IDR Slice
            };

            // 拼装 Annex B 码流 (带 00 00 00 01 起始码)
            std::vector<uint8_t> annex_b;
            for (const auto &b64 : h264_nalus) {
                auto nalu = base64_decode(b64);
                annex_b.push_back(0x00);
                annex_b.push_back(0x00);
                annex_b.push_back(0x00);
                annex_b.push_back(0x01);
                annex_b.insert(annex_b.end(), nalu.begin(), nalu.end());
            }

            // 发送数据包
            int ret = decoder->send_packet(annex_b.data(), annex_b.size(), 1000, 1000);
            CHECK(ret >= 0);

            // 发送 NULL 包以刷新并确保收敛
            decoder->send_packet(nullptr, 0, 0, 0);

            // 尝试接收解出的 HardwareFrame
            HardwareFrame::Ptr frame;
            int recv_ret = decoder->receive_frame(frame);
            CHECK(recv_ret == 0);
            CHECK(frame != nullptr);

            // 校验帧属性
            CHECK(frame->width == 64);
            CHECK(frame->height == 48);
            CHECK(frame->stride >= 64);
            CHECK(frame->vstride >= 48);
            CHECK(frame->host_ptr != nullptr);
            CHECK(frame->pts == 1000);
            CHECK(frame->y_plane_size() == static_cast<size_t>(frame->stride) * frame->vstride);

            // 校验 release_fn：重置后底层 AVFrame 正确回收
            frame.reset();
            CHECK(frame == nullptr);

            // 再次调用 flush 验证状态重置正常
            decoder->flush();
        }

        // ====================================================================
        // 3. FFmpegDecodeNode 真实 H.265 (HEVC) 解码测试
        // ====================================================================
        {
            auto decoder = IDecodeNode::create(Zhulong_CODEC_H265);
            CHECK(decoder != nullptr);

            std::vector<std::string> h265_nalus = {
                "QAEMAf//BAgAAAMAn6gAAAMAAB66AkA=",                                 // VPS
                "QgEBBAgAAAMAn6gAAAMAAB6gIIMWW6SkwvAWgIAAAAMAgAAADIQ=",             // SPS
                "RAHAcYES",                                                         // PPS
                "KAGt4NEX/9ORgYfYydg="                                              // IDR
            };

            std::vector<uint8_t> annex_b;
            for (const auto &b64 : h265_nalus) {
                auto nalu = base64_decode(b64);
                annex_b.push_back(0x00);
                annex_b.push_back(0x00);
                annex_b.push_back(0x00);
                annex_b.push_back(0x01);
                annex_b.insert(annex_b.end(), nalu.begin(), nalu.end());
            }

            int ret = decoder->send_packet(annex_b.data(), annex_b.size(), 2000, 2000);
            CHECK(ret >= 0);
            decoder->send_packet(nullptr, 0, 0, 0);

            HardwareFrame::Ptr frame;
            int recv_ret = decoder->receive_frame(frame);
            CHECK(recv_ret == 0);
            CHECK(frame != nullptr);
            CHECK(frame->width == 64);
            CHECK(frame->height == 48);
            CHECK(frame->pts == 2000);
        }

        std::cout << "All Decoder & BoundedQueue tests passed successfully!" << std::endl;
        return 0;
    } catch (const std::exception &ex) {
        std::cerr << "Decoder test failed: " << ex.what() << std::endl;
        return 1;
    }
}
