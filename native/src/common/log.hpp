#pragma once

#include <cstddef>
#include <cstdint>

namespace zhulong {

enum class LogLevel : int32_t {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
};

using LogSink = void (*)(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len);

bool set_log_sink(LogSink sink, uintptr_t token) noexcept;
bool start_runner_log_writer() noexcept;
void stop_runner_log_writer() noexcept;
uint64_t log_dropped() noexcept;
uint64_t log_writer_failures() noexcept;
void write_runner_stderr(const char *line, size_t line_len) noexcept;
void log_write(LogLevel level, uint64_t stream_id, const char *module,
    const char *format, ...) noexcept;

} // namespace zhulong

#define ZL_LOGD(stream_id, module, ...) \
    ::zhulong::log_write(::zhulong::LogLevel::Debug, (stream_id), (module), __VA_ARGS__)
#define ZL_LOGI(stream_id, module, ...) \
    ::zhulong::log_write(::zhulong::LogLevel::Info, (stream_id), (module), __VA_ARGS__)
#define ZL_LOGW(stream_id, module, ...) \
    ::zhulong::log_write(::zhulong::LogLevel::Warn, (stream_id), (module), __VA_ARGS__)
#define ZL_LOGE(stream_id, module, ...) \
    ::zhulong::log_write(::zhulong::LogLevel::Error, (stream_id), (module), __VA_ARGS__)
