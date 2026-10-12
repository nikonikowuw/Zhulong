#pragma once

#include <vector>
#include <cstdint>
#include <cstddef>
#include <limits>
#include <stdexcept>

namespace zhulong {

enum class DataType {
    FLOAT32,
    UINT8,
    INT8,
    INT32,
    INT64
};

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

class Tensor {
public:
    Tensor() = default;
    
    Tensor(std::vector<int64_t> dims, DataType dtype)
        : dims_(std::move(dims)), dtype_(dtype) {
        size_t total_elements = element_count();
        const size_t element_size = data_type_size(dtype_);
        if (element_size != 0 && total_elements > std::numeric_limits<size_t>::max() / element_size) {
            throw std::length_error("tensor byte size overflows size_t");
        }
        data_.resize(total_elements * element_size);
    }

    const std::vector<int64_t>& shape() const noexcept { return dims_; }
    DataType dtype() const noexcept { return dtype_; }

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

    size_t byte_size() const noexcept {
        return data_.size();
    }

    void* raw_data() noexcept { return data_.data(); }
    const void* raw_data() const noexcept { return data_.data(); }

    template<typename T>
    T* data() noexcept {
        return reinterpret_cast<T*>(data_.data());
    }

    template<typename T>
    const T* data() const noexcept {
        return reinterpret_cast<const T*>(data_.data());
    }

private:
    std::vector<int64_t> dims_;
    DataType dtype_{DataType::FLOAT32};
    std::vector<uint8_t> data_;
};

} // namespace zhulong
