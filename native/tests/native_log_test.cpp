#include "common/log.hpp"

#include <array>
#ifdef NDEBUG
#undef NDEBUG
#endif
#include <cassert>
#include <cerrno>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <limits.h>
#include <string>
#include <thread>
#include <unistd.h>

namespace {
struct Captured {
    uintptr_t token{};
    int32_t level{};
    uint64_t stream_id{};
    std::string module;
    std::string text;
    int count{};
} captured;

void capture(uintptr_t token, int32_t level, uint64_t stream_id,
    const char *module, size_t module_len, const char *text, size_t text_len) {
    captured.token = token;
    captured.level = level;
    captured.stream_id = stream_id;
    captured.module.assign(module, module_len);
    captured.text.assign(text, text_len);
    ++captured.count;
}

std::string read_pipe(int fd) {
    std::string output;
    char buffer[8192];
    for (;;) {
        const ssize_t count = ::read(fd, buffer, sizeof(buffer));
        if (count > 0) {
            output.append(buffer, static_cast<size_t>(count));
            continue;
        }
        if (count < 0 && errno == EINTR) continue;
        break;
    }
    return output;
}
}

int main() {
    assert(zhulong::set_log_sink(capture, 9));
    zhulong::log_write(zhulong::LogLevel::Debug, 4, "pipeline.test", "filtered");
    assert(captured.count == 0);
    zhulong::log_write(zhulong::LogLevel::Info, 42, "pipeline.test", "record_%d", 7);
    assert(captured.count == 1);
    assert(captured.token == 9);
    assert(captured.level == static_cast<int32_t>(zhulong::LogLevel::Info));
    assert(captured.stream_id == 42);
    assert(captured.module == "pipeline.test");
    assert(captured.text == "record_7");
    assert(zhulong::set_log_sink(nullptr, 0));

    // Without a runner writer or Go sink, producers drop silently instead of doing stderr I/O.
    int pipe_fds[2];
    assert(::pipe(pipe_fds) == 0);
    const int saved_stderr = ::dup(STDERR_FILENO);
    assert(saved_stderr >= 0);
    assert(::dup2(pipe_fds[1], STDERR_FILENO) >= 0);
    const uint64_t before_drop = zhulong::log_dropped();
    zhulong::log_write(zhulong::LogLevel::Info, 0, "native.test", "no_writer");
    assert(zhulong::log_dropped() == before_drop + 1);
    assert(::dup2(saved_stderr, STDERR_FILENO) >= 0);
    ::close(saved_stderr);
    ::close(pipe_fds[1]);
    assert(read_pipe(pipe_fds[0]).empty());
    ::close(pipe_fds[0]);

    // The runner writer must escape controls, replace invalid UTF-8 and close
    // an oversized JSON record rather than emitting a half line.
    assert(::pipe(pipe_fds) == 0);
    const int original_stderr = ::dup(STDERR_FILENO);
    assert(original_stderr >= 0);
    assert(::dup2(pipe_fds[1], STDERR_FILENO) >= 0);
    assert(zhulong::start_runner_log_writer());
    zhulong::log_write(zhulong::LogLevel::Info, 77, "runner.test", "quote=\" newline=\n invalid=\xff");
    std::array<char, 2048> controls{};
    controls.fill('\x01');
    zhulong::log_write(zhulong::LogLevel::Warn, 0, "runner.test", "%.*s",
        static_cast<int>(controls.size()), controls.data());
    zhulong::stop_runner_log_writer();
    assert(::dup2(original_stderr, STDERR_FILENO) >= 0);
    ::close(original_stderr);
    ::close(pipe_fds[1]);
    const std::string output = read_pipe(pipe_fds[0]);
    ::close(pipe_fds[0]);
    const auto first_end = output.find('\n');
    assert(first_end != std::string::npos);
    const std::string first = output.substr(0, first_end + 1);
    assert(first.find("\"zhulongLog\":true") != std::string::npos);
    assert(first.find("\"streamId\":77") != std::string::npos);
    assert(first.find("\\\"") != std::string::npos);
    assert(first.find("\\u000a") != std::string::npos);
    assert(first.find("\xef\xbf\xbd") != std::string::npos);
    const auto second_end = output.find('\n', first_end + 1);
    assert(second_end != std::string::npos);
    const std::string second = output.substr(first_end + 1, second_end - first_end);
    assert(second.find("\"truncated\":true}") != std::string::npos);
    assert(second.size() <= static_cast<size_t>(PIPE_BUF));
    assert(second.back() == '\n');

    // Stall the writer behind a full pipe. Producers must keep returning while
    // the bounded ring fills, and accepted records must remain complete JSON lines.
    assert(::pipe(pipe_fds) == 0);
    const int stress_stderr = ::dup(STDERR_FILENO);
    assert(stress_stderr >= 0);
    assert(::dup2(pipe_fds[1], STDERR_FILENO) >= 0);
    const uint64_t drops_before_stress = zhulong::log_dropped();
    assert(zhulong::start_runner_log_writer());
    const auto producer_started = std::chrono::steady_clock::now();
    for (int i = 0; i < 20000; ++i) {
        zhulong::log_write(zhulong::LogLevel::Info, 8, "runner.stress", "record_%d", i);
    }
    const auto producer_elapsed = std::chrono::steady_clock::now() - producer_started;
    assert(producer_elapsed < std::chrono::seconds(5));
    assert(zhulong::log_dropped() > drops_before_stress);
    std::string stress_output;
    std::thread stress_reader([&] { stress_output = read_pipe(pipe_fds[0]); });
    zhulong::stop_runner_log_writer();
    assert(::dup2(stress_stderr, STDERR_FILENO) >= 0);
    ::close(stress_stderr);
    ::close(pipe_fds[1]);
    stress_reader.join();
    ::close(pipe_fds[0]);
    size_t line_start = 0;
    while (line_start < stress_output.size()) {
        const size_t line_end = stress_output.find('\n', line_start);
        assert(line_end != std::string::npos);
        const std::string line = stress_output.substr(line_start, line_end - line_start);
        assert(!line.empty() && line.size() < static_cast<size_t>(PIPE_BUF));
        assert(line.front() == '{' && line.back() == '}');
        assert(line.find("\"zhulongLog\":true") != std::string::npos);
        line_start = line_end + 1;
    }

    // A single record written immediately after startup must not be lost to
    // admission contention. Regression: enqueue() used try_to_lock, which raced
    // the writer thread's wait_for re-acquire under the scheduler and dropped a
    // healthy record -- including this first Error -- in ~2.7% of process starts.
    // Assertions run after stderr is restored so a failure prints its reason.
    int lost_records = 0;
    int truncated_records = 0;
    for (int attempt = 0; attempt < 400; ++attempt) {
        assert(::pipe(pipe_fds) == 0);
        const int contention_stderr = ::dup(STDERR_FILENO);
        assert(contention_stderr >= 0);
        assert(::dup2(pipe_fds[1], STDERR_FILENO) >= 0);
        assert(zhulong::start_runner_log_writer());
        const uint64_t drops_before = zhulong::log_dropped();
        zhulong::log_write(zhulong::LogLevel::Error, 0, "algorithm.runner", "plugin_load_failed reason=dlopen");
        if (zhulong::log_dropped() != drops_before) ++lost_records;
        zhulong::stop_runner_log_writer();
        assert(::dup2(contention_stderr, STDERR_FILENO) >= 0);
        ::close(contention_stderr);
        ::close(pipe_fds[1]);
        const std::string contention_output = read_pipe(pipe_fds[0]);
        ::close(pipe_fds[0]);
        if (contention_output.find("\"zhulongLog\":true") == std::string::npos ||
            contention_output.find("plugin_load_failed reason=dlopen") == std::string::npos) {
            ++truncated_records;
        }
    }
    if (lost_records != 0 || truncated_records != 0) {
        std::fprintf(stderr, "runner admission race: %d/400 dropped, %d/400 missing stderr line\n",
            lost_records, truncated_records);
        return 1;
    }

    // A failed sink write is counted and never escapes the writer thread.
    const int full = ::open("/dev/full", O_WRONLY | O_CLOEXEC);
    if (full >= 0) {
        const int stderr_before_full = ::dup(STDERR_FILENO);
        assert(stderr_before_full >= 0);
        assert(::dup2(full, STDERR_FILENO) >= 0);
        const uint64_t failures_before = zhulong::log_writer_failures();
        assert(zhulong::start_runner_log_writer());
        zhulong::log_write(zhulong::LogLevel::Error, 0, "runner.test", "write_error_test");
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
        zhulong::stop_runner_log_writer();
        assert(::dup2(stderr_before_full, STDERR_FILENO) >= 0);
        ::close(stderr_before_full);
        ::close(full);
        assert(zhulong::log_writer_failures() > failures_before);
    }
    return 0;
}
