/**
 * @file decoder_test.cpp
 * @brief IDecodeNode, FFmpegDecodeNode 与 BoundedQueue 单元测试
 */

#include "nodes/decode/decoder.hpp"
#include "nodes/decode/mpp_decoder.hpp"
#include "nodes/capture/rtsp_input.hpp"
#include "pipeline/bounded_queue.hpp"
#include "pipeline/stream.hpp"
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

        // clear() 只清空元素，不改变取消标记，且不会“复活”已取消队列
        {
            BoundedQueue<int> q(4, OverflowStrategy::DROP_NEWEST);
            CHECK(q.push(1));
            CHECK(q.push(2));
            q.clear();
            CHECK(q.empty());
            CHECK(!q.is_cancelled());
            CHECK(q.push(3));
            q.cancel();
            q.clear();
            CHECK(q.is_cancelled());
            CHECK(!q.push(4));
        }

        // ====================================================================
        // 2. FFmpegDecodeNode 真实 H.264 关键帧端到端解码测试
        // ====================================================================
        {
            auto decoder = IDecodeNode::create(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, zhulong::DecoderBackend::FFMPEG_SW);
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
            auto decoder = IDecodeNode::create(Zhulong_CODEC_H265, nullptr, 0, 1, 90000, zhulong::DecoderBackend::FFMPEG_SW);
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

        // ====================================================================
        // 4. 纯 SDP Extradata 初始化与解码测试 (切片内无 SPS/PPS)
        // ====================================================================
        {
            std::vector<std::string> h264_extra_nalus = {
                "Z0LACtxHsBEAAAMAAQAAAwAyjxIngA==", // SPS
                "aM4PyA=="                          // PPS
            };
            std::vector<uint8_t> extradata;
            for (const auto &b64 : h264_extra_nalus) {
                auto nalu = base64_decode(b64);
                extradata.push_back(0x00);
                extradata.push_back(0x00);
                extradata.push_back(0x00);
                extradata.push_back(0x01);
                extradata.insert(extradata.end(), nalu.begin(), nalu.end());
            }

            auto slice_nalu = base64_decode("ZYiEOiYoAAkCycnJ111111114A=="); // IDR slice
            std::vector<uint8_t> slice_packet;
            slice_packet.push_back(0x00);
            slice_packet.push_back(0x00);
            slice_packet.push_back(0x00);
            slice_packet.push_back(0x01);
            slice_packet.insert(slice_packet.end(), slice_nalu.begin(), slice_nalu.end());

            auto decoder = IDecodeNode::create(
                Zhulong_CODEC_H264,
                extradata.data(), extradata.size(),
                1, 90000,
                zhulong::DecoderBackend::FFMPEG_SW
            );
            CHECK(decoder != nullptr);

            int ret = decoder->send_packet(slice_packet.data(), slice_packet.size(), 5000, 5000);
            CHECK(ret >= 0);
            decoder->send_packet(nullptr, 0, 0, 0);

            HardwareFrame::Ptr frame;
            int recv_ret = decoder->receive_frame(frame);
            CHECK(recv_ret == 0);
            CHECK(frame != nullptr);
            CHECK(frame->width == 64 && frame->height == 48);
            CHECK(frame->format == zhulong::FRAME_FORMAT_YUV420P);
            CHECK(frame->has_pts);
            CHECK(frame->pts == 5000);
            CHECK(frame->time_base_num == 1 && frame->time_base_den == 90000);
        }

        // ====================================================================
        // 5. Stream 按需解码会话生命周期与多次恢复产帧测试
        // ====================================================================
        {
            zhulong::initialize_ffmpeg();

            // 构造包含完整 SPS/PPS/IDR 的数据包
            std::vector<std::string> h264_nalus = {
                "Z0LACtxHsBEAAAMAAQAAAwAyjxIngA==",
                "aM4PyA==",
                "ZYiEOiYoAAkCycnJ111111114A=="
            };
            std::vector<uint8_t> key_pkt;
            for (const auto &b64 : h264_nalus) {
                auto nalu = base64_decode(b64);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x01);
                key_pkt.insert(key_pkt.end(), nalu.begin(), nalu.end());
            }

            zhulong::Stream stream(1001, "rtsp://127.0.0.1/test-session", {});

            // 首次接入 AI 消费者
            stream.consumers.emplace(1, Zhulong_CONSUMER_AI);
            auto retired = stream.sync_decode_pipeline();
            CHECK(!retired);
            CHECK(stream.has_active_decoder());

            Zhulong_packet_view packet{};
            packet.data = key_pkt.data();
            packet.size = key_pkt.size();
            packet.codec = Zhulong_CODEC_H264;
            packet.key_frame = 1;
            packet.has_pts = 1;
            packet.pts = 1000;
            packet.time_base_num = 1;
            packet.time_base_den = 90000;

            // 喂包 (在流式硬件解码器中，首帧在后续包到达或流推进时输出)
            stream.dispatch(packet);
            packet.pts = 1040;
            stream.dispatch(packet);

            // 等待产帧
            HardwareFrame::ConstPtr frame1;
            bool got_frame = false;
            for (int i = 0; i < 40; ++i) {
                if (stream.acquire_frame(frame1, 50ms)) {
                    got_frame = true;
                    break;
                }
            }
            CHECK(got_frame && frame1 != nullptr);
            CHECK(frame1->pts == 1000);
            CHECK(frame1->stream_id == 1001);

            // 释放首个 AI 消费者 -> 休眠解码会话
            stream.consumers.erase(1);
            retired = stream.sync_decode_pipeline();
            CHECK(retired != nullptr);
            zhulong::Stream::stop_decode_session(std::move(retired));
            CHECK(!stream.has_active_decoder());

            // 再次接入新 AI 消费者 -> 重新激活会话
            stream.consumers.emplace(2, Zhulong_CONSUMER_AI);
            retired = stream.sync_decode_pipeline();
            CHECK(!retired);
            CHECK(stream.has_active_decoder());

            // 再次喂包
            packet.pts = 2000;
            stream.dispatch(packet);
            packet.pts = 2040;
            stream.dispatch(packet);

            HardwareFrame::ConstPtr frame2;
            got_frame = false;
            for (int i = 0; i < 40; ++i) {
                if (stream.acquire_frame(frame2, 50ms)) {
                    got_frame = true;
                    break;
                }
            }
            CHECK(got_frame && frame2 != nullptr);
            CHECK(frame2->pts == 2000);

            // 清理停机
            stream.request_stop();
            stream.join();
        }

        // ====================================================================
        // 6. 压缩包满载拥塞与关键帧自愈重同步测试
        // ====================================================================
        {
            std::vector<std::string> h264_nalus = {
                "Z0LACtxHsBEAAAMAAQAAAwAyjxIngA==",
                "aM4PyA==",
                "ZYiEOiYoAAkCycnJ111111114A=="
            };
            std::vector<uint8_t> key_pkt;
            for (const auto &b64 : h264_nalus) {
                auto nalu = base64_decode(b64);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x00);
                key_pkt.push_back(0x01);
                key_pkt.insert(key_pkt.end(), nalu.begin(), nalu.end());
            }

            zhulong::Video meta;
            meta.view.codec = Zhulong_CODEC_H264;
            meta.view.time_base_num = 1;
            meta.view.time_base_den = 90000;

            zhulong::DecodeSession session(2002, meta);

            Zhulong_packet_view pkt_key{};
            pkt_key.data = key_pkt.data();
            pkt_key.size = key_pkt.size();
            pkt_key.codec = Zhulong_CODEC_H264;
            pkt_key.key_frame = 1;
            pkt_key.has_pts = 1;
            pkt_key.pts = 1000;

            Zhulong_packet_view pkt_non_key = pkt_key;
            pkt_non_key.key_frame = 0;

            // 新建会话必须先等待关键帧：GOP 中途接入时的孤立非关键帧一律拒绝
            CHECK(!session.push_packet(pkt_non_key));
            CHECK(session.packet_queue_size() == 0);
            // 该拒绝由生产者侧关键帧门控拦截，必须被计数
            CHECK(session.counters().keyframe_gate_drops == 1);

            // 模拟大量非关键帧将队列喂满（60容量）
            int pushed_count = 0;
            for (int i = 0; i < 80; ++i) {
                pkt_non_key.pts = 1000 + i * 40;
                if (session.push_packet(pkt_non_key)) {
                    pushed_count++;
                }
            }
            CHECK(pushed_count <= 60);

            // 在等待关键帧状态下，再喂入非关键帧必须被直接拦截拒绝
            CHECK(!session.push_packet(pkt_non_key));
            // 上述 81 次非关键帧均在等待关键帧状态下被生产者门控拦截
            CHECK(session.counters().keyframe_gate_drops == 82);
            // 队列从未被填满，不应发生拥塞重同步
            CHECK(session.counters().congestion_resyncs == 0);

            // 当新的关键帧到达时，必须能成功入队并恢复自愈
            pkt_key.pts = 9000;
            CHECK(session.push_packet(pkt_key));

            // 解码器创建未失败，不应有解码器不可用丢包
            CHECK(session.counters().decoder_unavailable_drops == 0);

            session.request_stop();
            session.join();
        }

        // ====================================================================
        // 7. 异常与损坏码流隔离测试 (异常数据不终止解码循环)
        // ====================================================================
        {
            zhulong::Video meta;
            meta.view.codec = Zhulong_CODEC_H264;
            zhulong::DecodeSession session(3003, meta);

            // 喂入损坏的垃圾数据包
            std::vector<uint8_t> corrupted_data = {0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03};
            Zhulong_packet_view bad_pkt{};
            bad_pkt.data = corrupted_data.data();
            bad_pkt.size = corrupted_data.size();
            bad_pkt.codec = Zhulong_CODEC_H264;
            bad_pkt.key_frame = 1;

            CHECK(session.push_packet(bad_pkt));

            // 等待一段时间，解码器内部应安全 flush 并保持线程运行，无崩溃
            std::this_thread::sleep_for(100ms);
            CHECK(session.is_active());

            session.request_stop();
            session.join();
        }

        // ====================================================================
        // 8. 多消费者并发 acquire_frame 只读借用与延迟释放测试
        // ====================================================================
        {
            auto frame = std::make_shared<HardwareFrame>();
            frame->width = 64;
            frame->height = 48;
            frame->pts = 12345;
            std::atomic<int> release_count{0};
            frame->release_fn = [&](HardwareFrame *) {
                release_count.fetch_add(1);
            };

            HardwareFrame::ConstPtr consumer1 = frame;
            HardwareFrame::ConstPtr consumer2 = frame;
            frame.reset();

            CHECK(release_count.load() == 0);
            consumer1.reset();
            CHECK(release_count.load() == 0);
            consumer2.reset();
            CHECK(release_count.load() == 1);
        }

        // ====================================================================
        // 9. 解码器后端工厂与自动降级测试 (DecoderBackend::AUTO / FFMPEG_SW / MPP_HW)
        // ====================================================================
        {
            using zhulong::DecoderBackend;
            // 9.1 AUTO 模式：在非 RK 硬件上平滑回退至 FFmpeg 软解
            auto auto_dec = IDecodeNode::create(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, DecoderBackend::AUTO);
            CHECK(auto_dec != nullptr);

            // 9.2 FFMPEG_SW 模式：显式创建 FFmpeg 软解
            auto sw_dec = IDecodeNode::create(Zhulong_CODEC_H265, nullptr, 0, 1, 90000, DecoderBackend::FFMPEG_SW);
            CHECK(sw_dec != nullptr);

            // 9.3 MPP_HW 模式：若当前环境不支持 MPP，应显式抛出异常
            if (!zhulong::MppDecodeNode::is_supported()) {
                bool threw = false;
                try {
                    auto mpp_dec = IDecodeNode::create(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, DecoderBackend::MPP_HW);
                    (void)mpp_dec;
                } catch (const std::runtime_error &) {
                    threw = true;
                }
                CHECK(threw);
            } else {
                auto mpp_dec = IDecodeNode::create(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, DecoderBackend::MPP_HW);
                CHECK(mpp_dec != nullptr);
            }
        }

        // ====================================================================
        // 10. MppDecodeNode 真实硬件端到端解码测试 (H.264 & H.265 on Rockchip VPU)
        // ====================================================================
        if (zhulong::MppDecodeNode::is_supported()) {
            std::cout << "Testing MppDecodeNode on hardware accelerator..." << std::endl;
            // 10.1 真实 H.264 硬解测试
            {
                auto decoder = IDecodeNode::create(Zhulong_CODEC_H264, nullptr, 0, 1, 90000, zhulong::DecoderBackend::MPP_HW);
                CHECK(decoder != nullptr);

                std::vector<std::string> h264_nalus = {
                    "Z0LACtxHsBEAAAMAAQAAAwAyjxIngA==",
                    "aM4PyA==",
                    "ZYiEOiYoAAkCycnJ111111114A=="
                };
                std::vector<uint8_t> annex_b;
                for (const auto &b64 : h264_nalus) {
                    auto nalu = base64_decode(b64);
                    annex_b.push_back(0x00);
                    annex_b.push_back(0x00);
                    annex_b.push_back(0x00);
                    annex_b.push_back(0x01);
                    annex_b.insert(annex_b.end(), nalu.begin(), nalu.end());
                }

                int ret = decoder->send_packet(annex_b.data(), annex_b.size(), 1000, 1000);
                CHECK(ret >= 0);
                decoder->send_packet(nullptr, 0, 0, 0);

                HardwareFrame::Ptr frame;
                int recv_ret = 1;
                for (int i = 0; i < 50; ++i) {
                    recv_ret = decoder->receive_frame(frame);
                    if (recv_ret == 0) {
                        break;
                    }
                    std::this_thread::sleep_for(5ms);
                }
                CHECK(recv_ret == 0);
                CHECK(frame != nullptr);
                CHECK(frame->width == 64);
                CHECK(frame->height == 48);
                CHECK(frame->stride >= 64);
                CHECK(frame->vstride >= 48);
                CHECK(frame->format == zhulong::FRAME_FORMAT_NV12);
                CHECK(frame->dma_fd >= 0); // 必须导出有效内核 DMA-BUF 描述符！
                CHECK(frame->pts == 1000);
                CHECK(frame->release_fn != nullptr);

                // 测试释放闭包安全归还显存
                frame.reset();
                CHECK(frame == nullptr);
            }

            // 10.2 真实 H.265 (HEVC) 硬解测试
            {
                auto decoder = IDecodeNode::create(Zhulong_CODEC_H265, nullptr, 0, 1, 90000, zhulong::DecoderBackend::MPP_HW);
                CHECK(decoder != nullptr);

                std::vector<std::string> h265_nalus = {
                    "QAEMAf//BAgAAAMAn6gAAAMAAB66AkA=",
                    "QgEBBAgAAAMAn6gAAAMAAB6gIIMWW6SkwvAWgIAAAAMAgAAADIQ=",
                    "RAHAcYES",
                    "KAGt4NEX/9ORgYfYydg="
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
                int recv_ret = 1;
                for (int i = 0; i < 50; ++i) {
                    recv_ret = decoder->receive_frame(frame);
                    if (recv_ret == 0) {
                        break;
                    }
                    std::this_thread::sleep_for(5ms);
                }
                CHECK(recv_ret == 0);
                CHECK(frame != nullptr);
                CHECK(frame->width == 64);
                CHECK(frame->height == 48);
                CHECK(frame->format == zhulong::FRAME_FORMAT_NV12);
                CHECK(frame->dma_fd >= 0);
                CHECK(frame->pts == 2000);
            }
        }

        std::cout << "All Decoder & BoundedQueue tests passed successfully!" << std::endl;
        return 0;
    } catch (const std::exception &ex) {
        std::cerr << "Decoder test failed: " << ex.what() << std::endl;
        return 1;
    }
}
