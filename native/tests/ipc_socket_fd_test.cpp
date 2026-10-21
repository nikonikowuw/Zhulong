#include <algorithm/ipc/unix_socket.hpp>
#include <iostream>
#include <cassert>
#include <unistd.h>
#include <fcntl.h>
#include <cstring>
#include <thread>
#include <chrono>

using namespace zhulong::ipc;

int main() {
    std::string sock_path = "/tmp/zhulong_test_scm_rights.sock";
    ::unlink(sock_path.c_str());

    UnixSocketServer server;
    assert(server.start(sock_path));

    UnixSocketClient client;
    assert(client.connect(sock_path));

    // 等待服务端建立连接
    for (int i = 0; i < 50 && server.client_count() == 0; ++i) {
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    assert(server.client_count() == 1);

    // 1. 创建测试临时文件
    char tmp_template[] = "/tmp/zhulong_fd_test_XXXXXX";
    int test_fd = ::mkstemp(tmp_template);
    assert(test_fd >= 0);
    ::unlink(tmp_template); // 断开目录项，文件通过 fd 存续

    const char* magic_content = "Zhulong-DMA-BUF-Zero-Copy-SCM-RIGHTS-Test";
    size_t content_len = std::strlen(magic_content);
    ssize_t written = ::write(test_fd, magic_content, content_len);
    assert(written == static_cast<ssize_t>(content_len));

    // 2. 服务端通过 SCM_RIGHTS 发送包含该 fd 的 IPC 消息
    std::string payload_text = "test_payload_with_fd";
    assert(server.send_msg_with_fd(1, MSG_FRAME_DISPATCH, payload_text.data(), payload_text.size(), test_fd));

    // 3. 客户端接收并提取描述符
    IpcHeader hdr{};
    std::vector<uint8_t> payload;
    int received_fd = -1;
    assert(client.recv_msg_with_fd(hdr, payload, received_fd, 2000));
    assert(hdr.msg_type == MSG_FRAME_DISPATCH);
    assert(payload.size() == payload_text.size());
    assert(std::memcmp(payload.data(), payload_text.data(), payload.size()) == 0);
    assert(received_fd >= 0);
    assert(received_fd != test_fd); // 跨进程/跨套接字传递时为不同序号的副本描述符

    // 验证 FD_CLOEXEC 是否自动设置
    int flags = fcntl(received_fd, F_GETFD);
    assert(flags != -1);
    assert(flags & FD_CLOEXEC);

    // 从接收到的描述符读取数据，验证内容一致
    char read_buf[64] = {0};
    ssize_t read_bytes = ::pread(received_fd, read_buf, sizeof(read_buf) - 1, 0);
    assert(read_bytes == static_cast<ssize_t>(content_len));
    assert(std::strcmp(read_buf, magic_content) == 0);

    ::close(received_fd);
    ::close(test_fd);

    // 4. 测试不带 fd 的常规消息
    assert(server.send_msg(1, MSG_HEARTBEAT));
    int no_fd = -999;
    assert(client.recv_msg_with_fd(hdr, payload, no_fd, 2000));
    assert(hdr.msg_type == MSG_HEARTBEAT);
    assert(no_fd == -1);

    // 5. REQ-3: FrameDispatchMsg.reserved_fd 必须恒为 -1
    //    fd 是进程本地句柄，宿主把 dma_fd 写进线格式会让接收端对无关资源
    //    执行 mmap/ioctl。真实句柄只经 SCM_RIGHTS 传递。
    FrameDispatchMsg dispatch{};
    dispatch.stream_id = 42;
    dispatch.frame_id = 7;
    dispatch.slot_index = 0;
    dispatch.generation = 1;
    dispatch.reserved_fd = -1;
    assert(server.send_msg(1, MSG_FRAME_DISPATCH, &dispatch, sizeof(dispatch)));

    int dispatch_fd = -999;
    assert(client.recv_msg_with_fd(hdr, payload, dispatch_fd, 2000));
    assert(hdr.msg_type == MSG_FRAME_DISPATCH);
    assert(dispatch_fd == -1); // 未携带 SCM_RIGHTS fd
    assert(payload.size() == sizeof(FrameDispatchMsg));
    FrameDispatchMsg received{};
    std::memcpy(&received, payload.data(), sizeof(received));
    assert(received.stream_id == 42);
    assert(received.frame_id == 7);
    assert(received.reserved_fd == -1);
    assert(sizeof(FrameDispatchMsg) == 96); // 线格式未漂移

    // 6. REQ-7: 发送侧必须对称拦截超大 payload（不得静默截断 size_t）
    std::vector<uint8_t> too_large(MAX_IPC_PAYLOAD_SIZE + 1, 0);
    assert(server.send_msg(1, MSG_RESULT, too_large.data(), too_large.size()) == false);
    assert(client.send_msg(MSG_RESULT, too_large.data(), too_large.size()) == false);
    // 恰好等于上限必须被接受
    std::vector<uint8_t> exact(MAX_IPC_PAYLOAD_SIZE, 0);
    assert(server.send_msg(1, MSG_RESULT, exact.data(), exact.size()) == true);
    int exact_fd = -999;
    assert(client.recv_msg_with_fd(hdr, payload, exact_fd, 2000));
    assert(hdr.msg_type == MSG_RESULT);
    assert(payload.size() == MAX_IPC_PAYLOAD_SIZE);
    assert(exact_fd == -1);

    client.disconnect();
    server.stop();
    ::unlink(sock_path.c_str());

    // 7. ⚠️ 对端消失必须让 is_connected() 变假（孤儿忙等回归门禁）
    //
    // 宿主被 kill -9 / OOM 杀掉时不会发 MSG_DISCONNECT，内核只把客户端 socket 置
    // POLLHUP。旧实现在 poll 里把 HUP 当「本次收包失败」返回，但 fd_ 依旧 >= 0，
    // is_connected() 恒真，runner 主循环于是 `continue` 成 100% CPU 忙等孤儿
    // （实测 20s 内烧满 19.98s CPU）。这里直接锁死该行为。
    {
        std::string crash_sock = "/tmp/zhulong_test_peer_crash.sock";
        ::unlink(crash_sock.c_str());

        UnixSocketServer owner;
        assert(owner.start(crash_sock));

        UnixSocketClient orphan;
        assert(orphan.connect(crash_sock));
        for (int i = 0; i < 50 && owner.client_count() == 0; ++i) {
            std::this_thread::sleep_for(std::chrono::milliseconds(5));
        }
        assert(owner.client_count() == 1);
        assert(orphan.is_connected());

        // 模拟宿主崩溃：去掉 MSG_DISCONNECT 的优雅路径，直接销毁服务端套接字。
        owner.stop();

        // 首次 recv 必须在有界时间内返回并锁死连接（不可退化成忙等）。
        bool closed = false;
        for (int attempt = 0; attempt < 20; ++attempt) {
            IpcHeader h{};
            std::vector<uint8_t> p;
            int fd = -1;
            if (!orphan.recv_msg_with_fd(h, p, fd, 200) && !orphan.is_connected()) {
                closed = true;
                break;
            }
        }
        assert(closed);
        assert(!orphan.is_connected());

        // 幂等性：重复探测不得复活连接。
        assert(!orphan.is_connected());
        orphan.disconnect();
        ::unlink(crash_sock.c_str());
    }

    // 8. 空闲超时绝不能误判为掉线（否则正常无帧期就会自杀）
    {
        std::string idle_sock = "/tmp/zhulong_test_peer_idle.sock";
        ::unlink(idle_sock.c_str());

        UnixSocketServer quiet;
        assert(quiet.start(idle_sock));
        UnixSocketClient waiting;
        assert(waiting.connect(idle_sock));
        for (int i = 0; i < 50 && quiet.client_count() == 0; ++i) {
            std::this_thread::sleep_for(std::chrono::milliseconds(5));
        }

        // 对端仍在，只是没有帧：连续多轮超时后必须依旧 is_connected()。
        for (int round = 0; round < 5; ++round) {
            IpcHeader h{};
            std::vector<uint8_t> p;
            int fd = -1;
            assert(!waiting.recv_msg_with_fd(h, p, fd, 50));
            assert(waiting.is_connected());
        }

        // 重连语义：断开后重新 connect 必须复位挂断标记，否则同一对象永久假死。
        waiting.disconnect();
        assert(!waiting.is_connected());
        assert(waiting.connect(idle_sock));
        assert(waiting.is_connected());

        waiting.disconnect();
        quiet.stop();
        ::unlink(idle_sock.c_str());
    }

    std::cout << "[Test] ZhulongIpcSocketFdTests PASSED" << std::endl;
    return 0;
}
