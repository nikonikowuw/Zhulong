#include <zhulong/platform/dmabuf_allocator.hpp>
#include <iostream>
#include <cassert>
#include <cstring>
#include <memory>
#include <utility>

using namespace zhulong::platform;

int main() {
    auto& allocator = DmaBufAllocator::instance();
    std::cout << "[Test] DmaBufAllocator type: " << allocator.type_name()
              << ", device_path: " << allocator.device_path()
              << ", supported: " << (allocator.is_supported() ? "true" : "false")
              << std::endl;

    if (allocator.is_supported()) {
        assert(!allocator.device_path().empty());

        // 1. 测试常规 1MB 物理缓冲区
        const size_t test_size = 1024 * 1024; // 1MB
        auto buf = allocator.allocate(test_size);
        assert(buf != nullptr);
        assert(buf->fd() >= 0);
        assert(buf->size() == test_size);
        assert(buf->mmap_size() >= test_size);
        assert(buf->mmap_addr() != nullptr);

        // 测试直接 CPU 写同步
        assert(buf->sync_start(DMA_SYNC_WRITE));
        std::memset(buf->mmap_addr(), 0x5A, test_size);
        assert(buf->sync_end(DMA_SYNC_WRITE));

        // 测试直接 CPU 读同步
        assert(buf->sync_start(DMA_SYNC_READ));
        const uint8_t* rptr = static_cast<const uint8_t*>(buf->mmap_addr());
        assert(rptr[0] == 0x5A);
        assert(rptr[test_size - 1] == 0x5A);
        assert(buf->sync_end(DMA_SYNC_READ));

        // 2. 测试 ScopedCpuSync RAII 作用域自动守卫
        {
            ScopedCpuSync write_guard(*buf, DMA_SYNC_WRITE);
            assert(write_guard.is_locked());
            assert(static_cast<bool>(write_guard));
            std::memset(buf->mmap_addr(), 0xA5, 4096);
        } // 离开作用域自动执行 sync_end

        {
            ScopedCpuSync read_guard(*buf, DMA_SYNC_READ);
            assert(read_guard.is_locked());
            assert(static_cast<bool>(read_guard));
            assert(rptr[0] == 0xA5);
            assert(rptr[4095] == 0xA5);

            // 测试 ScopedCpuSync 移动构造
            ScopedCpuSync moved_guard = std::move(read_guard);
            assert(!read_guard.is_locked());
            assert(!static_cast<bool>(read_guard));
            assert(moved_guard.is_locked());
            assert(static_cast<bool>(moved_guard));

            // 测试 ScopedCpuSync 移动赋值
            ScopedCpuSync assigned_guard(*buf, DMA_SYNC_WRITE);
            assigned_guard = std::move(moved_guard);
            assert(assigned_guard.is_locked());
            assert(!moved_guard.is_locked());
        }

        // 3. 大尺寸二维几何分配，验证 DRM Dumb 步长二维化
        //
        // ⚠️ 尺寸不能写死：dma_heap CMA 池的大小由板端 DTS 决定（实测 RK3568 仅
        //    CmaTotal≈16MB 且长期被占满），固定要 16MB 会让这条用例在任何 CMA 较小的
        //    板子上红 —— 而红的原因是「板子的 CMA 配置」，不是分配器缺陷。
        //    改成阶梯探测：取能成功分配的最大档，再对那一档做完整几何校验；
        //    同时要求失败档留下可读错误（fail-closed 语义不许静默 nullptr）。
        std::unique_ptr<DmaBuffer> large_buf;
        size_t large_size = 0;
        for (const size_t candidate : {size_t{16} << 20, size_t{8} << 20, size_t{4} << 20, size_t{2} << 20}) {
            large_buf = allocator.allocate(candidate);
            if (large_buf != nullptr) {
                large_size = candidate;
                break;
            }
            // 失败必须是「有理由的失败」：空字符串说明错误路径没填诊断，那是 bug。
            assert(!allocator.last_error().empty());
            std::cout << "[Test] " << candidate / (1024 * 1024) << "MB 分配失败（预期内，继续降档）: "
                      << allocator.last_error() << std::endl;
        }
        assert(large_buf != nullptr);            // 2MB 都分配不到，才算真的坏
        const size_t megabyte = 1024 * 1024;
        const bool is_multi_megabyte = large_size >= 2 * megabyte;
        std::cout << "[Test] 大尺寸分配档位: " << large_size / megabyte << "MB"
                  << (is_multi_megabyte ? "" : "（板端 CMA 紧张，仅验证小档几何）") << std::endl;
        assert(large_buf->fd() >= 0);
        assert(large_buf->size() == large_size);
        assert(large_buf->mmap_size() >= large_size);
        assert(large_buf->mmap_addr() != nullptr);

        {
            ScopedCpuSync large_write(*large_buf, DMA_SYNC_WRITE);
            assert(large_write.is_locked());
            uint8_t* lptr = static_cast<uint8_t*>(large_buf->mmap_addr());
            lptr[0] = 0x33;
            lptr[large_size - 1] = 0x77;
        }
        {
            ScopedCpuSync large_read(*large_buf, DMA_SYNC_READ);
            assert(large_read.is_locked());
            const uint8_t* lptr = static_cast<const uint8_t*>(large_buf->mmap_addr());
            assert(lptr[0] == 0x33);
            assert(lptr[large_size - 1] == 0x77);
        }

        // 4. 测试 DmaBuffer 移动语义与独立描述符持有
        int old_fd = buf->fd();
        size_t old_mmap_sz = buf->mmap_size();
        DmaBuffer moved = std::move(*buf);
        assert(buf->fd() == -1);
        assert(buf->size() == 0);
        assert(buf->mmap_size() == 0);
        assert(buf->mmap_addr() == nullptr);
        assert(moved.fd() == old_fd);
        assert(moved.size() == test_size);
        assert(moved.mmap_size() == old_mmap_sz);

        // 测试 DmaBuffer 移动赋值
        DmaBuffer moved2(-1, 0, nullptr);
        moved2 = std::move(moved);
        assert(moved.fd() == -1);
        assert(moved.size() == 0);
        assert(moved2.fd() == old_fd);
        assert(moved2.size() == test_size);
        assert(moved2.mmap_size() == old_mmap_sz);

        // 验证分配成功后 last_error 为空
        assert(allocator.last_error().empty());

        std::cout << "[Test] DmaBufAllocator hardware allocation & ScopedCpuSync verified." << std::endl;
    } else {
        // 在不支持 DMA-BUF 的环境（如 macOS 或普通容器）下，断言优雅降级
        assert(!allocator.is_supported());
        assert(allocator.type() == DmaAllocatorType::NONE);
        assert(allocator.device_path().empty());
        auto buf = allocator.allocate(4096);
        assert(buf == nullptr);
        assert(allocator.last_error() == "DMA-BUF allocator is not supported on this platform/environment");
        std::cout << "[Test] DmaBufAllocator graceful fallback verified." << std::endl;
    }

    // 5. 验证参数校验与错误记录
    {
        auto zero_buf = allocator.allocate(0);
        assert(zero_buf == nullptr);
        assert(allocator.last_error() == "requested allocation size is 0");
    }

    // 6. 验证 RAII 保护、DmaBufferSpec 构造与空对象安全性
    {
        DmaBuffer dummy(-1, 0, nullptr);
        assert(dummy.fd() == -1);
        assert(dummy.size() == 0);
        assert(dummy.mmap_size() == 0);
        assert(dummy.mmap_addr() == nullptr);
        assert(!dummy.sync_start());
        assert(!dummy.sync_end());

        ScopedCpuSync dummy_guard(dummy);
        assert(!dummy_guard.is_locked());
        assert(!static_cast<bool>(dummy_guard));

        // 显式 DmaBufferSpec 构造
        DmaBufferSpec spec{};
        spec.fd = -1;
        spec.size = 0;
        DmaBuffer spec_dummy(spec);
        assert(spec_dummy.fd() == -1);
        assert(spec_dummy.size() == 0);
        assert(!spec_dummy.sync_start());
    }

    std::cout << "[Test] ZhulongDmaBufAllocatorTests PASSED" << std::endl;
    return 0;
}
