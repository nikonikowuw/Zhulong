/**
 * @file frame_test.cpp
 * @brief HardwareFrame 硬件帧对象与多算法零拷贝生命周期单元测试
 */

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

int main() {
    try {
        using zhulong::HardwareFrame;

        // 1. 基础几何步长与 UV 平面偏移计算测试
        {
            HardwareFrame frame;
            frame.width = 1920;
            frame.height = 1080;
            frame.stride = 1920;
            frame.vstride = 1088; // RK MPP / 昇腾对齐高度
            frame.dma_fd = 42;
            frame.pts = 123456;

            // 必须严格等于 stride * vstride (2088960)，而非 1920 * 1080 (2073600)
            CHECK(frame.y_plane_size() == 1920UL * 1088UL);
            CHECK(frame.y_plane_size() == 2088960UL);

            // 当 vstride 为 0 时，应安全回退到 stride * height
            frame.vstride = 0;
            CHECK(frame.y_plane_size() == 1920UL * 1080UL);
        }

        // 2. 单所有权 RAII 释放回调触发测试
        {
            std::atomic<int> release_count{0};
            {
                auto frame = std::make_shared<HardwareFrame>();
                frame->release_fn = [&](HardwareFrame* /*self*/) {
                    release_count.fetch_add(1);
                };
                CHECK(release_count.load() == 0);
            }
            CHECK(release_count.load() == 1);
        }

        // 3. 1:N 模拟多算法多线程并发只读共享与生命周期测试
        {
            std::atomic<int> release_count{0};
            std::atomic<bool> algos_finished{false};

            auto frame = std::make_shared<HardwareFrame>();
            frame->width = 1920;
            frame->height = 1080;
            frame->stride = 1920;
            frame->vstride = 1088;
            frame->dma_fd = 10;
            frame->pts = 999999;
            frame->release_fn = [&](HardwareFrame* self) {
                CHECK(self->dma_fd == 10);
                release_count.fetch_add(1);
            };

            constexpr int kAlgoCount = 4;
            std::vector<std::thread> algo_threads;
            algo_threads.reserve(kAlgoCount);

            for (int i = 0; i < kAlgoCount; ++i) {
                // 每个算法异步持有 std::shared_ptr<HardwareFrame> 共享所有权
                algo_threads.emplace_back([shared_frame = frame, i]() {
                    // 验证只读访问底层字段一致性
                    CHECK(shared_frame->width == 1920);
                    CHECK(shared_frame->height == 1080);
                    CHECK(shared_frame->dma_fd == 10);

                    // 模拟各算法硬件前处理 (RGA / VPC / CUDA) 执行耗时差异
                    std::this_thread::sleep_for(std::chrono::milliseconds(2 + (i * 2)));

                    // 前处理提交完毕，释放持有的 frame 引用
                });
            }

            // 解码器线程本身完成分发，立即放弃自己持有的引用
            frame.reset();
            CHECK(frame == nullptr);
            // 此时算法线程还在执行，release_fn 绝不应该被提前触发！
            CHECK(release_count.load() == 0);

            // 等待所有算法线程完成前处理并退出
            for (auto& t : algo_threads) {
                t.join();
            }

            // 最后一个算法线程退出后，release_fn 必须且仅被触发 1 次！
            CHECK(release_count.load() == 1);
        }

        // 4. 移动构造与移动赋值防 Double-Free 测试
        {
            std::atomic<int> release_count{0};
            {
                HardwareFrame frame1;
                frame1.dma_fd = 55;
                frame1.release_fn = [&](HardwareFrame* /*self*/) {
                    release_count.fetch_add(1);
                };

                // 移动构造
                HardwareFrame frame2(std::move(frame1));
                CHECK(frame1.release_fn == nullptr);
                CHECK(frame1.dma_fd == -1);
                CHECK(frame2.dma_fd == 55);
                CHECK(release_count.load() == 0);

                // 移动赋值
                HardwareFrame frame3;
                frame3 = std::move(frame2);
                CHECK(frame2.release_fn == nullptr);
                CHECK(frame3.dma_fd == 55);
                CHECK(release_count.load() == 0);
            }
            // frame1, frame2, frame3 均析构完毕，仅 frame3 触发 1 次释放
            CHECK(release_count.load() == 1);
        }

        // 5. release_fn 抛出异常时的析构安全测试
        {
            {
                HardwareFrame throwing_frame;
                throwing_frame.release_fn = [](HardwareFrame* /*self*/) {
                    throw std::runtime_error("Simulated hardware free failure");
                };
            } // 离开作用域，~HardwareFrame() 必须安全捕获异常，不得引发 std::terminate 崩溃
        }

        std::cout << "All HardwareFrame unit tests passed successfully!" << std::endl;
        return 0;
    } catch (const std::exception& ex) {
        std::cerr << "HardwareFrame test failed: " << ex.what() << std::endl;
        return 1;
    }
}
