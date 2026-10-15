/**
 * @file tensor.hpp
 * @brief 与推理后端解耦的通用多维张量容器
 *
 * 核心设计原则：
 * 1. 形状与类型自描述：dims_ 记录形状，dtype_ 记录元素类型，data_ 为 packed 连续字节缓冲。
 * 2. 溢出与非法形状防御：非空 shape 中任一维度 <= 0 抛出 std::invalid_argument；
 *    元素数与字节数的乘法溢出抛出 std::length_error，绝不允许静默回绕。
 * 3. ⚠️ 动态 shape 分配不在当前 API 支持范围内：构造时即按 dims_ 一次性分配完毕。
 */

#pragma once

#include <vector>
#include <cstdint>
#include <cstddef>
#include <limits>
#include <stdexcept>

namespace zhulong {

/**
 * @brief 张量元素数据类型枚举
 */
enum class DataType {
    FLOAT32, /**< 32 位浮点（模型输出经 want_float 转换后的统一类型） */
    UINT8,   /**< 8 位无符号整数（图像像素） */
    INT8,    /**< 8 位有符号整数（量化模型权重） */
    INT32,   /**< 32 位有符号整数（形状、索引） */
    INT64    /**< 64 位有符号整数 */
};

/**
 * @brief 获取指定数据类型的单元素字节大小
 *
 * @param dtype 数据类型
 * @return size_t 单元素字节数（未知类型返回 1）
 */
inline size_t data_type_size(DataType dtype) {
    switch (dtype) {
        case DataType::FLOAT32: return sizeof(float);
        case DataType::UINT8:   return sizeof(uint8_t);
        case DataType::INT8:    return sizeof(int8_t);
        case DataType::INT32:   return sizeof(int32_t);
        case DataType::INT64:   return sizeof(int64_t);
        default: return 1;
    }
}

/**
 * @brief 多维张量容器（形状 + 类型 + packed 连续数据）
 */
class Tensor {
public:
    /**
     * @brief 构造空张量（element_count() 为 0，shape() 为空）
     */
    Tensor() = default;

    /**
     * @brief 按形状与类型分配张量并清零初始化数据区
     *
     * @param dims 形状维度列表（可含零/负维度，由 element_count 抛出异常）
     * @param dtype 元素数据类型
     * @throw std::invalid_argument 任一维度 <= 0
     * @throw std::length_error 元素数或字节数溢出 size_t
     */
    Tensor(std::vector<int64_t> dims, DataType dtype)
        : dims_(std::move(dims)), dtype_(dtype) {
        size_t total_elements = element_count();
        const size_t element_size = data_type_size(dtype_);
        if (element_size != 0 && total_elements > std::numeric_limits<size_t>::max() / element_size) {
            throw std::length_error("tensor byte size overflows size_t");
        }
        data_.resize(total_elements * element_size);
    }

    /**
     * @brief 绑定外部 DMA-BUF 构造零拷贝张量
     *
     * @param fd DMA-BUF 文件描述符 (>= 0)
     * @param dims 形状维度列表
     * @param dtype 元素数据类型
     * @param va 外部映射的虚拟内存首地址（可选，若提供则 raw_data() 指向 va）
     */
    static Tensor from_dma_buf(int fd, std::vector<int64_t> dims, DataType dtype, void* va = nullptr) {
        Tensor t;
        t.dims_ = std::move(dims);
        t.dtype_ = dtype;
        t.dma_buf_fd_ = fd;
        t.is_external_ = true;
        t.external_va_ = va;
        size_t total_elements = t.element_count();
        size_t element_size = data_type_size(t.dtype_);
        if (element_size != 0 && total_elements > std::numeric_limits<size_t>::max() / element_size) {
            throw std::length_error("tensor byte size overflows size_t");
        }
        t.external_size_ = total_elements * element_size;
        return t;
    }

    /**
     * @brief 获取底层 DMA-BUF 文件描述符（>=0 有效，<0 表示普通内存）
     */
    [[nodiscard]] int dma_buf_fd() const noexcept { return dma_buf_fd_; }

    /**
     * @brief 是否为外部绑定缓冲区（非 Tensor 自身管理的 vector 内存）
     */
    [[nodiscard]] bool is_external() const noexcept { return is_external_; }

    /**
     * @brief 获取张量形状
     */
    const std::vector<int64_t>& shape() const noexcept { return dims_; }

    /**
     * @brief 获取张量元素数据类型
     */
    DataType dtype() const noexcept { return dtype_; }

    /**
     * @brief 计算并返回张量总元素数
     *
     * ⚠️ 不是简单乘积：逐维累乘时逐次检查溢出，并在任一维度 <= 0 时抛异常。
     *
     * @return size_t 元素总数（空 shape 返回 0）
     * @throw std::invalid_argument 任一维度 <= 0
     * @throw std::length_error 元素数溢出 size_t
     */
    size_t element_count() const {
        if (dims_.empty()) return 0;
        size_t count = 1;
        for (auto d : dims_) {
            if (d <= 0) throw std::invalid_argument("tensor dimensions must be positive");
            const size_t dimension = static_cast<size_t>(d);
            if (count > std::numeric_limits<size_t>::max() / dimension) {
                throw std::length_error("tensor element count overflows size_t");
            }
            count *= dimension;
        }
        return count;
    }

    /**
     * @brief 获取张量数据区字节大小
     */
    size_t byte_size() const noexcept {
        return is_external_ ? external_size_ : data_.size();
    }

    /**
     * @brief 获取可写原始数据首地址
     */
    void* raw_data() noexcept { return is_external_ ? external_va_ : data_.data(); }

    /**
     * @brief 获取只读原始数据首地址
     */
    const void* raw_data() const noexcept { return is_external_ ? external_va_ : data_.data(); }

    /**
     * @brief 按指定类型重解释数据区首地址（不校验 T 与 dtype_ 是否一致）
     */
    template<typename T>
    T* data() noexcept {
        return reinterpret_cast<T*>(raw_data());
    }

    /**
     * @brief 按指定类型只读重解释数据区首地址（不校验 T 与 dtype_ 是否一致）
     */
    template<typename T>
    const T* data() const noexcept {
        return reinterpret_cast<const T*>(raw_data());
    }

private:
    std::vector<int64_t> dims_;          /**< 形状维度列表 */
    DataType dtype_{DataType::FLOAT32};  /**< 元素数据类型 */
    std::vector<uint8_t> data_;          /**< packed 连续数据区 */
    int dma_buf_fd_{-1};                 /**< 外部绑定的 DMA-BUF 文件描述符 */
    bool is_external_{false};            /**< 是否为外部缓冲区 */
    void* external_va_{nullptr};         /**< 外部虚拟地址指针 */
    size_t external_size_{0};            /**< 外部缓冲区字节大小 */
};

} // namespace zhulong

