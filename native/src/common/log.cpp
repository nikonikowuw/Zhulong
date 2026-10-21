#include "common/log.hpp"

#include <array>
#include <atomic>
#include <chrono>
#include <cstdarg>
#include <cstdio>
#include <cstring>
#include <mutex>
#include <thread>

#if defined(_WIN32)
#error "Zhulong native logging currently requires POSIX stderr writes"
#else
#include <cerrno>
#include <condition_variable>
#include <limits.h>
#include <unistd.h>
#endif

namespace zhulong {
namespace {

constexpr size_t kModuleCapacity = 128;
constexpr size_t kTextCapacity = 2048;
constexpr size_t kQueueCapacity = 256;
constexpr size_t kLineCapacity = PIPE_BUF;
static_assert(kLineCapacity >= 512, "PIPE_BUF must hold a complete log record");

struct Record {
    int32_t level{};
    uint64_t stream_id{};
    size_t module_len{};
    size_t text_len{};
    bool truncated{};
    std::array<char, kModuleCapacity> module{};
    std::array<char, kTextCapacity + 1> text{};
};

std::atomic<LogSink> g_sink{nullptr};
std::atomic<uintptr_t> g_token{0};
std::atomic<int32_t> g_min_level{static_cast<int32_t>(LogLevel::Info)};
std::atomic<uint64_t> g_dropped{0};
std::atomic<uint64_t> g_writer_failures{0};
std::atomic<bool> g_writer_running{false};
std::atomic<bool> g_writer_stopping{false};
std::mutex g_writer_lifecycle_mutex;
std::mutex g_stderr_write_mutex;
std::mutex g_queue_mutex;
std::condition_variable g_queue_ready;
std::array<Record, kQueueCapacity> g_records{};
size_t g_head = 0;
size_t g_size = 0;
std::thread g_writer;

const char *level_name(int32_t level) noexcept {
    switch (static_cast<LogLevel>(level)) {
    case LogLevel::Debug: return "debug";
    case LogLevel::Info: return "info";
    case LogLevel::Warn: return "warn";
    case LogLevel::Error: return "error";
    }
    return "info";
}

bool append_char(char *out, size_t limit, size_t &used, char value) noexcept {
    if (used >= limit) return false;
    out[used++] = value;
    return true;
}

bool append_bytes(char *out, size_t limit, size_t &used, const char *value, size_t len) noexcept {
    if (len > limit - used) return false;
    std::memcpy(out + used, value, len);
    used += len;
    return true;
}

size_t valid_utf8_width(const unsigned char *s, size_t remaining) noexcept {
    const unsigned char a = s[0];
    if (a < 0x80) return 1;
    if (a >= 0xc2 && a <= 0xdf && remaining >= 2 && (s[1] & 0xc0) == 0x80) return 2;
    if (a >= 0xe0 && a <= 0xef && remaining >= 3 &&
        (s[1] & 0xc0) == 0x80 && (s[2] & 0xc0) == 0x80 &&
        !(a == 0xe0 && s[1] < 0xa0) && !(a == 0xed && s[1] >= 0xa0)) return 3;
    if (a >= 0xf0 && a <= 0xf4 && remaining >= 4 &&
        (s[1] & 0xc0) == 0x80 && (s[2] & 0xc0) == 0x80 && (s[3] & 0xc0) == 0x80 &&
        !(a == 0xf0 && s[1] < 0x90) && !(a == 0xf4 && s[1] >= 0x90)) return 4;
    return 0;
}

bool append_json_string(char *out, size_t limit, size_t &used,
    const char *value, size_t len, bool &truncated) noexcept {
    static constexpr char hex[] = "0123456789abcdef";
    if (!append_char(out, limit, used, '"')) return false;
    size_t i = 0;
    while (i < len) {
        const auto c = static_cast<unsigned char>(value[i]);
        if (c == '"' || c == '\\') {
            if (used + 2 > limit) { truncated = true; break; }
            out[used++] = '\\';
            out[used++] = static_cast<char>(c);
            ++i;
        } else if (c < 0x20) {
            if (used + 6 > limit) { truncated = true; break; }
            out[used++] = '\\'; out[used++] = 'u'; out[used++] = '0'; out[used++] = '0';
            out[used++] = hex[c >> 4]; out[used++] = hex[c & 0x0f];
            ++i;
        } else if (c < 0x80) {
            if (!append_char(out, limit, used, static_cast<char>(c))) { truncated = true; break; }
            ++i;
        } else {
            const size_t width = valid_utf8_width(reinterpret_cast<const unsigned char *>(value + i), len - i);
            if (width == 0) {
                static constexpr char replacement[] = "\xef\xbf\xbd";
                if (!append_bytes(out, limit, used, replacement, sizeof(replacement) - 1)) { truncated = true; break; }
                ++i;
            } else {
                if (!append_bytes(out, limit, used, value + i, width)) { truncated = true; break; }
                i += width;
            }
        }
    }
    return append_char(out, limit, used, '"');
}

bool serialize_record(const Record &record, char *line, size_t &line_len) noexcept {
    size_t used = 0;
    bool truncated = record.truncated;
    char prefix[256];
    const int prefix_len = std::snprintf(prefix, sizeof(prefix),
        "{\"zhulongLog\":true,\"level\":\"%s\",\"module\":",
        level_name(record.level));
    if (prefix_len < 0 || static_cast<size_t>(prefix_len) >= sizeof(prefix) ||
        !append_bytes(line, kLineCapacity, used, prefix, static_cast<size_t>(prefix_len))) return false;
    if (!append_json_string(line, kLineCapacity, used, record.module.data(), record.module_len, truncated)) return false;

    char fields[96];
    const int fields_len = std::snprintf(fields, sizeof(fields), ",\"streamId\":%llu,\"message\":",
        static_cast<unsigned long long>(record.stream_id));
    if (fields_len < 0 || static_cast<size_t>(fields_len) >= sizeof(fields) ||
        !append_bytes(line, kLineCapacity, used, fields, static_cast<size_t>(fields_len))) return false;

    constexpr size_t kSuffixReserve = sizeof("\",\"truncated\":true}\n") - 1;
    const size_t message_limit = kLineCapacity - kSuffixReserve;
    if (!append_json_string(line, message_limit, used, record.text.data(), record.text_len, truncated)) return false;
    if (truncated) {
        static constexpr char suffix[] = ",\"truncated\":true}\n";
        if (!append_bytes(line, kLineCapacity, used, suffix, sizeof(suffix) - 1)) return false;
    } else {
        static constexpr char suffix[] = "}\n";
        if (!append_bytes(line, kLineCapacity, used, suffix, sizeof(suffix) - 1)) return false;
    }
    line_len = used;
    return true;
}

bool write_stderr_bytes(const char *data, size_t len) noexcept {
    std::lock_guard<std::mutex> lock(g_stderr_write_mutex);
    size_t written = 0;
    while (written < len) {
        const ssize_t result = ::write(STDERR_FILENO, data + written, len - written);
        if (result > 0) {
            written += static_cast<size_t>(result);
            continue;
        }
        if (result < 0 && errno == EINTR) continue;
        return false;
    }
    return true;
}

bool write_line(const char *line, size_t len) noexcept {
    return write_stderr_bytes(line, len);
}

void emit_counter_record(const char *name, uint64_t value) noexcept {
    Record record{};
    record.level = static_cast<int32_t>(LogLevel::Warn);
    constexpr char module[] = "native.logging";
    std::memcpy(record.module.data(), module, sizeof(module) - 1);
    record.module_len = sizeof(module) - 1;
    const int size = std::snprintf(record.text.data(), record.text.size(), "%s=%llu", name,
        static_cast<unsigned long long>(value));
    if (size < 0) return;
    record.text_len = static_cast<size_t>(size) < kTextCapacity ? static_cast<size_t>(size) : kTextCapacity;
    char line[kLineCapacity];
    size_t line_len = 0;
    if (serialize_record(record, line, line_len) && !write_line(line, line_len)) {
        g_writer_failures.fetch_add(1, std::memory_order_relaxed);
    }
}

void writer_loop() noexcept {
    uint64_t reported_drops = g_dropped.load(std::memory_order_relaxed);
    uint64_t reported_failures = g_writer_failures.load(std::memory_order_relaxed);
    for (;;) {
        Record record{};
        bool has_record = false;
        {
            std::unique_lock<std::mutex> lock(g_queue_mutex);
            g_queue_ready.wait_for(lock, std::chrono::milliseconds(100), [] {
                return g_size > 0 || g_writer_stopping.load(std::memory_order_acquire);
            });
            if (g_size > 0) {
                record = g_records[g_head];
                g_head = (g_head + 1) % kQueueCapacity;
                --g_size;
                has_record = true;
            } else if (g_writer_stopping.load(std::memory_order_acquire)) {
                break;
            }
        }
        if (!has_record) continue;

        char line[kLineCapacity];
        size_t line_len = 0;
        if (!serialize_record(record, line, line_len) || !write_line(line, line_len)) {
            g_writer_failures.fetch_add(1, std::memory_order_relaxed);
            continue;
        }

        const uint64_t drops = g_dropped.load(std::memory_order_relaxed);
        if (drops > reported_drops) {
            reported_drops = drops;
            emit_counter_record("dropped", drops);
        }
        const uint64_t failures = g_writer_failures.load(std::memory_order_relaxed);
        if (failures > reported_failures) {
            reported_failures = failures;
            emit_counter_record("writer_failures", failures);
        }
    }
}

bool enqueue(const Record &record) noexcept {
    // Blocking here is bounded and cannot deadlock: g_queue_mutex is never held
    // across sink I/O (serialize/write happen after the writer releases it), so
    // the longest hold is the Record copy below. A try_lock producer admission
    // instead loses the race against the writer's wait_for re-acquire and
    // silently drops a healthy record -- including the first Error record after
    // start_runner_log_writer() -- roughly once in 25 process starts.
    std::unique_lock<std::mutex> lock(g_queue_mutex);
    if (g_size == kQueueCapacity || g_writer_stopping.load(std::memory_order_acquire)) {
        g_dropped.fetch_add(1, std::memory_order_relaxed);
        return false;
    }
    const size_t tail = (g_head + g_size) % kQueueCapacity;
    g_records[tail] = record;
    ++g_size;
    lock.unlock();
    g_queue_ready.notify_one();
    return true;
}

} // namespace

bool set_log_sink(LogSink sink, uintptr_t token) noexcept {
    if ((sink == nullptr) != (token == 0)) return false;
    g_token.store(token, std::memory_order_relaxed);
    g_sink.store(sink, std::memory_order_release);
    return true;
}

bool start_runner_log_writer() noexcept {
    std::lock_guard<std::mutex> lock(g_writer_lifecycle_mutex);
    if (g_writer_running.load(std::memory_order_acquire) || g_sink.load(std::memory_order_acquire) != nullptr) return false;
    {
        std::lock_guard<std::mutex> queue_lock(g_queue_mutex);
        g_head = 0;
        g_size = 0;
    }
    g_writer_stopping.store(false, std::memory_order_release);
    g_writer_running.store(true, std::memory_order_release);
    try {
        g_writer = std::thread(writer_loop);
    } catch (...) {
        g_writer_running.store(false, std::memory_order_release);
        return false;
    }
    return true;
}

void stop_runner_log_writer() noexcept {
    std::lock_guard<std::mutex> lock(g_writer_lifecycle_mutex);
    if (!g_writer_running.load(std::memory_order_acquire)) return;
    g_writer_stopping.store(true, std::memory_order_release);
    g_queue_ready.notify_all();
    if (g_writer.joinable()) g_writer.join();
    g_writer_running.store(false, std::memory_order_release);
}

uint64_t log_dropped() noexcept { return g_dropped.load(std::memory_order_relaxed); }
uint64_t log_writer_failures() noexcept { return g_writer_failures.load(std::memory_order_relaxed); }

void write_runner_stderr(const char *line, size_t line_len) noexcept {
    if ((line == nullptr && line_len != 0) || !write_stderr_bytes(line, line_len)) {
        g_writer_failures.fetch_add(1, std::memory_order_relaxed);
    }
}

void log_write(LogLevel level, uint64_t stream_id, const char *module, const char *format, ...) noexcept {
    const int32_t raw_level = static_cast<int32_t>(level);
    if (raw_level < g_min_level.load(std::memory_order_relaxed)) return;
    if (raw_level < static_cast<int32_t>(LogLevel::Debug) || raw_level > static_cast<int32_t>(LogLevel::Error) ||
        module == nullptr || format == nullptr) {
        g_dropped.fetch_add(1, std::memory_order_relaxed);
        return;
    }

    size_t module_len = 0;
    while (module_len <= kModuleCapacity && module[module_len] != '\0') ++module_len;
    if (module_len == 0 || module_len > kModuleCapacity) {
        g_dropped.fetch_add(1, std::memory_order_relaxed);
        return;
    }

    Record record{};
    record.level = raw_level;
    record.stream_id = stream_id;
    record.module_len = module_len;
    std::memcpy(record.module.data(), module, module_len);
    va_list args;
    va_start(args, format);
    const int formatted = std::vsnprintf(record.text.data(), record.text.size(), format, args);
    va_end(args);
    if (formatted < 0) {
        g_dropped.fetch_add(1, std::memory_order_relaxed);
        return;
    }
    record.truncated = static_cast<size_t>(formatted) >= record.text.size();
    record.text_len = static_cast<size_t>(formatted) < kTextCapacity ? static_cast<size_t>(formatted) : kTextCapacity;

    const LogSink sink = g_sink.load(std::memory_order_acquire);
    if (sink != nullptr) {
        const uintptr_t token = g_token.load(std::memory_order_relaxed);
        try {
            sink(token, raw_level, stream_id, record.module.data(), record.module_len,
                record.text.data(), record.text_len);
        } catch (...) {
            g_dropped.fetch_add(1, std::memory_order_relaxed);
        }
        return;
    }
    if (!g_writer_running.load(std::memory_order_acquire)) {
        g_dropped.fetch_add(1, std::memory_order_relaxed);
        return;
    }
    enqueue(record);
}

} // namespace zhulong
