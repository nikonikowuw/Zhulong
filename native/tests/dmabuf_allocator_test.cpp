#include <platform/dmabuf/dmabuf_allocator.hpp>
#include <iostream>
#include <cassert>
#include <cstring>
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
            std::memset(buf->mmap_addr(), 0xA5, 4096);
        } // 离开作用域自动执行 sync_end

        {
            ScopedCpuSync read_guard(*buf, DMA_SYNC_READ);
            assert(read_guard.is_locked());
            assert(rptr[0] == 0xA5);
            assert(rptr[4095] == 0xA5);

            // 测试 ScopedCpuSync 移动语义
            ScopedCpuSync moved_guard = std::move(read_guard);
            assert(!read_guard.is_locked());
            assert(moved_guard.is_locked());
        }

        // 3. 测试大尺寸二维几何分配（16MB 4K 帧缓冲），验证 DRM Dumb 步长二维化
        const size_t large_size = 16 * 1024 * 1024; // 16MB
        auto large_buf = allocator.allocate(large_size);
        assert(large_buf != nullptr);
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

        std::cout << "[Test] DmaBufAllocator hardware allocation & ScopedCpuSync verified." << std::endl;
    } else {
        // 在不支持 DMA-BUF 的环境（如 macOS 或普通容器）下，断言优雅降级
        assert(!allocator.is_supported());
        assert(allocator.type() == DmaAllocatorType::NONE);
        assert(allocator.device_path().empty());
        auto buf = allocator.allocate(4096);
        assert(buf == nullptr);
        std::cout << "[Test] DmaBufAllocator graceful fallback verified." << std::endl;
    }

    // 5. 验证 RAII 保护与空对象安全性
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
    }

    std::cout << "[Test] ZhulongDmaBufAllocatorTests PASSED" << std::endl;
    return 0;
}
