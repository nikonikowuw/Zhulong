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

    client.disconnect();
    server.stop();
    ::unlink(sock_path.c_str());

    std::cout << "[Test] ZhulongIpcSocketFdTests PASSED" << std::endl;
    return 0;
}
