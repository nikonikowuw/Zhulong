#include <platform/dmabuf/dmabuf_allocator.hpp>
#include <iostream>
#include <cassert>
#include <cstring>

using namespace zhulong::platform;

int main() {
    auto& allocator = DmaBufAllocator::instance();
    std::cout << "[Test] DmaBufAllocator type: " << allocator.type_name()
              << ", supported: " << (allocator.is_supported() ? "true" : "false")
              << std::endl;

    if (allocator.is_supported()) {
        const size_t test_size = 1024 * 1024; // 1MB
        auto buf = allocator.allocate(test_size);
        assert(buf != nullptr);
        assert(buf->fd() >= 0);
        assert(buf->size() == test_size);
        assert(buf->mmap_addr() != nullptr);

        // 测试 CPU 写同步
        assert(buf->sync_start(DMA_SYNC_WRITE));
        std::memset(buf->mmap_addr(), 0x5A, test_size);
        assert(buf->sync_end(DMA_SYNC_WRITE));

        // 测试 CPU 读同步
        assert(buf->sync_start(DMA_SYNC_READ));
        const uint8_t* rptr = static_cast<const uint8_t*>(buf->mmap_addr());
        assert(rptr[0] == 0x5A);
        assert(rptr[test_size - 1] == 0x5A);
        assert(buf->sync_end(DMA_SYNC_READ));

        // 测试移动语义
        int old_fd = buf->fd();
        DmaBuffer moved = std::move(*buf);
        assert(buf->fd() == -1);
        assert(buf->mmap_addr() == nullptr);
        assert(moved.fd() == old_fd);
        assert(moved.size() == test_size);

        std::cout << "[Test] DmaBufAllocator hardware allocation verified." << std::endl;
    } else {
        // 在不支持 DMA-BUF 的环境（如 macOS 或普通容器）下，断言优雅降级
        assert(!allocator.is_supported());
        assert(allocator.type() == DmaAllocatorType::NONE);
        auto buf = allocator.allocate(4096);
        assert(buf == nullptr);
        std::cout << "[Test] DmaBufAllocator graceful fallback verified." << std::endl;
    }

    // 验证 RAII 保护与空对象安全性
    {
        DmaBuffer dummy(-1, 0, nullptr);
        assert(dummy.fd() == -1);
        assert(dummy.size() == 0);
        assert(dummy.mmap_addr() == nullptr);
        assert(!dummy.sync_start());
        assert(!dummy.sync_end());
    }

    std::cout << "[Test] ZhulongDmaBufAllocatorTests PASSED" << std::endl;
    return 0;
}
