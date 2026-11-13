#include "../src/algorithm/ipc/ipc_protocol.hpp"
#include "../src/algorithm/ipc/shm_pool.hpp"
#include "../src/algorithm/ipc/unix_socket.hpp"
#include "../src/algorithm/ipc/frame_broker.hpp"
#include "../src/algorithm/ipc/broker_resource_lock.hpp"
#include <Zhulong/frame.hpp>
#include <sys/mman.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>
#include <cassert>
#include <iostream>
#include <vector>
#include <thread>
#include <chrono>
#include <cstring>
#include <csignal>
#include <atomic>
#include <limits>
#include <string>

using namespace zhulong;
using namespace zhulong::ipc;

// =============================================================================
// Test 1: ShmPool 基本创建与多端只读映射验证
// =============================================================================
void test_shm_pool_basic() {
    std::string shm_name = "/zhulong_test_shm_1";
    size_t slot_count = 4;
    size_t slot_size = 1024 * 1024; // 1MB

    ShmPool master;
    std::string err;
    assert(master.init(shm_name, ShmMode::CREATE_READ_WRITE, slot_count, slot_size, &err));
    assert(master.is_valid());
    assert(master.slot_count() == slot_count);
    assert(master.slot_size() == slot_size);

    // 写入测试数据到 slot 0
    uint8_t* wptr = master.slot_write_ptr(0);
    assert(wptr != nullptr);
    std::memset(wptr, 0xAB, 1024);

    // 子进程/客户端以只读模式打开
    ShmPool reader;
    assert(reader.init(shm_name, ShmMode::OPEN_READ_ONLY, 0, 0, &err));
    assert(reader.is_valid());
    assert(reader.slot_count() == slot_count);
    assert(reader.slot_size() == slot_size);

    const uint8_t* rptr = reader.slot_read_ptr(0);
    assert(rptr != nullptr);
    assert(rptr[0] == 0xAB);
    assert(rptr[1023] == 0xAB);

    reader.close();
    master.close();

    // 验证 unlink 后无法再以只读打开
    ShmPool reader2;
    assert(!reader2.init(shm_name, ShmMode::OPEN_READ_ONLY, 0, 0, &err));

    // 校验 invalid ShmPool CREATE_READ_WRITE 参数拒绝
    {
        ShmPool bad_shm;
        std::string bad_err;
        assert(!bad_shm.init("/zhulong_test_bad_shm_0", ShmMode::CREATE_READ_WRITE, 0, 1024, &bad_err));
        assert(!bad_shm.init("/zhulong_test_bad_shm_1", ShmMode::CREATE_READ_WRITE, 2, 0, &bad_err));
        assert(!bad_shm.init("/zhulong_test_bad_shm_2", ShmMode::CREATE_READ_WRITE,
                             static_cast<size_t>(std::numeric_limits<uint32_t>::max()) + 1ULL, 1, &bad_err));
    }

    std::cout << "[PASS] test_shm_pool_basic" << std::endl;
}

// =============================================================================
// Test 2: FrameBroker 多客户端并发分发、数据完整性与 ACK 代次保护
// =============================================================================
void test_frame_broker_dispatch_and_leasing() {
    uint64_t stream_id = 1001;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 2 * 1024 * 1024; // 2MB
    opt.max_in_flight_per_client = 2;
    opt.lease_timeout = std::chrono::milliseconds(300);

    std::string err;
    assert(broker.start(stream_id, "/tmp", opt, &err));
    assert(broker.is_running());

    // 客户端 1 连接并握手
    UnixSocketClient client1;
    assert(client1.connect(broker.socket_path(), &err));

    HandshakeReq req1{};
    req1.api_version = 1;
    std::strncpy(req1.client_name, "client_1", sizeof(req1.client_name) - 1);
    assert(client1.send_msg(MSG_HANDSHAKE_REQ, &req1, sizeof(req1)));

    IpcHeader resp_hdr1{};
    std::vector<uint8_t> payload1;
    assert(client1.recv_msg(resp_hdr1, payload1, 1000));
    assert(resp_hdr1.msg_type == MSG_HANDSHAKE_RESP);
    HandshakeResp resp1{};
    std::memcpy(&resp1, payload1.data(), sizeof(resp1));
    assert(resp1.client_id > 0);

    ShmPool shm_client1;
    assert(shm_client1.init(resp1.shm_name, ShmMode::OPEN_READ_ONLY, 0, 0, &err));

    // 等待服务端状态更新
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 1);

    // 构造测试帧
    std::vector<uint8_t> dummy_bgr(640 * 480 * 3, 0x7F);
    dummy_bgr[0] = 0x12;
    dummy_bgr[100] = 0x34;

    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN; // BGR24
    frame.pts = 99999;
    frame.host_ptr = dummy_bgr.data();

    // 发布帧
    assert(broker.publish_frame(frame));

    // 客户端接收帧分发消息
    IpcHeader dispatch_hdr{};
    std::vector<uint8_t> dispatch_payload;
    assert(client1.recv_msg(dispatch_hdr, dispatch_payload, 1000));
    assert(dispatch_hdr.msg_type == MSG_FRAME_DISPATCH);

    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, dispatch_payload.data(), sizeof(dispatch));
    assert(dispatch.pts == 99999);
    assert(dispatch.width == 640);
    assert(dispatch.height == 480);
    assert(dispatch.format == ZHULONG_PIXEL_FORMAT_BGR24);

    // 校验共享内存中的数据内容
    const uint8_t* slot_data = shm_client1.slot_read_ptr(dispatch.slot_index);
    assert(slot_data != nullptr);
    assert(slot_data[0] == 0x12);
    assert(slot_data[100] == 0x34);

    // ⚠️ REQ-3：broker 实际产出的分发包中 reserved_fd 必须恒为 -1。
    //    fd 是进程本地句柄，写入线格式会让接收端对无关资源执行 mmap/ioctl。
    assert(dispatch.reserved_fd == -1);

    // 发送过期的错误 generation ACK (测试防 ABA / 过期 ACK)
    FrameAckMsg stale_ack{};
    stale_ack.stream_id = stream_id;
    stale_ack.frame_id = dispatch.frame_id;
    stale_ack.slot_index = dispatch.slot_index;
    stale_ack.generation = dispatch.generation - 1; // 伪造旧代次
    stale_ack.client_id = resp1.client_id;
    assert(client1.send_msg(MSG_FRAME_ACK, &stale_ack, sizeof(stale_ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    // 发送正确 generation ACK
    FrameAckMsg valid_ack{};
    valid_ack.stream_id = stream_id;
    valid_ack.frame_id = dispatch.frame_id;
    valid_ack.slot_index = dispatch.slot_index;
    valid_ack.generation = dispatch.generation;
    valid_ack.client_id = resp1.client_id;
    assert(client1.send_msg(MSG_FRAME_ACK, &valid_ack, sizeof(valid_ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    ClientInfo cinfo{};
    assert(broker.get_client_info(resp1.client_id, cinfo));
    assert(cinfo.in_flight_count == 0);
    assert(cinfo.delivered_frames == 1);
    assert(cinfo.dropped_frames == 0);

    client1.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_dispatch_and_leasing" << std::endl;
}

// =============================================================================
// Test 3: 慢客户端背压隔离 (慢消费者丢帧，不影响高速消费者与 Broker)
// =============================================================================
void test_frame_broker_backpressure_isolation() {
    uint64_t stream_id = 1002;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2; // 最多允许 2 帧在途
    opt.lease_timeout = std::chrono::milliseconds(200);

    assert(broker.start(stream_id, "/tmp", opt));

    // Fast Client
    UnixSocketClient fast_client;
    assert(fast_client.connect(broker.socket_path()));
    HandshakeReq req1{};
    std::strncpy(req1.client_name, "fast", sizeof(req1.client_name) - 1);
    fast_client.send_msg(MSG_HANDSHAKE_REQ, &req1, sizeof(req1));
    IpcHeader hdr1{};
    std::vector<uint8_t> p1;
    fast_client.recv_msg(hdr1, p1, 1000);
    HandshakeResp resp1{};
    std::memcpy(&resp1, p1.data(), sizeof(resp1));

    // Slow Client
    UnixSocketClient slow_client;
    assert(slow_client.connect(broker.socket_path()));
    HandshakeReq req2{};
    std::strncpy(req2.client_name, "slow", sizeof(req2.client_name) - 1);
    slow_client.send_msg(MSG_HANDSHAKE_REQ, &req2, sizeof(req2));
    IpcHeader hdr2{};
    std::vector<uint8_t> p2;
    slow_client.recv_msg(hdr2, p2, 1000);
    HandshakeResp resp2{};
    std::memcpy(&resp2, p2.data(), sizeof(resp2));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 2);

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    // 连续推 5 帧
    // fast_client 每收到一帧立即 ACK
    // slow_client 不 ACK
    for (int i = 0; i < 5; ++i) {
        frame.pts = 1000 + i;
        assert(broker.publish_frame(frame));

        // fast client 接收并回复 ACK
        IpcHeader fh{};
        std::vector<uint8_t> fp;
        if (fast_client.recv_msg(fh, fp, 200)) {
            FrameDispatchMsg d{};
            std::memcpy(&d, fp.data(), sizeof(d));
            FrameAckMsg ack{};
            ack.stream_id = stream_id;
            ack.frame_id = d.frame_id;
            ack.slot_index = d.slot_index;
            ack.generation = d.generation;
            ack.client_id = resp1.client_id;
            fast_client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack));
        }

        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }

    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    ClientInfo fast_info{};
    ClientInfo slow_info{};
    assert(broker.get_client_info(resp1.client_id, fast_info));
    assert(broker.get_client_info(resp2.client_id, slow_info));

    // fast 客户端交付全部 5 帧，零丢帧
    assert(fast_info.delivered_frames == 5);
    assert(fast_info.dropped_frames == 0);

    // slow 客户端由于在途达到上限，经历了平滑丢帧
    assert(slow_info.delivered_frames == 2);
    assert(slow_info.dropped_frames == 3);

    fast_client.disconnect();
    slow_client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_backpressure_isolation" << std::endl;
}

// =============================================================================
// Test 4: 客户端异常崩溃 / 断开自愈 (立即回收租约，零死锁)
// =============================================================================
void test_frame_broker_crash_reclaim() {
    uint64_t stream_id = 1003;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2; // 仅 2 个槽位
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2;
    opt.lease_timeout = std::chrono::milliseconds(1000);

    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    std::strncpy(req.client_name, "doomed_client", sizeof(req.client_name) - 1);
    client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req));
    IpcHeader hdr{};
    std::vector<uint8_t> p;
    client.recv_msg(hdr, p, 1000);
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    // 推入 2 帧，占满全部 2 个槽位
    frame.pts = 1;
    assert(broker.publish_frame(frame));
    frame.pts = 2;
    assert(broker.publish_frame(frame));

    // 此时客户端尚未 ACK，若推第 3 帧应因无空闲槽位而 drop
    frame.pts = 3;
    assert(!broker.publish_frame(frame));

    // 模拟客户端突然“崩溃”（直接 close socket 断连）
    client.disconnect();

    // 等待 Broker 感知到断开连接并瞬间回收所有槽位
    std::this_thread::sleep_for(std::chrono::milliseconds(100));
    assert(broker.client_count() == 0);

    // 新客户端接入
    UnixSocketClient new_client;
    assert(new_client.connect(broker.socket_path()));
    HandshakeReq req2{};
    std::strncpy(req2.client_name, "new_client", sizeof(req2.client_name) - 1);
    new_client.send_msg(MSG_HANDSHAKE_REQ, &req2, sizeof(req2));
    new_client.recv_msg(hdr, p, 1000);

    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    // 验证崩溃租约已 100% 释放：再次推帧成功！
    frame.pts = 4;
    assert(broker.publish_frame(frame));

    new_client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_crash_reclaim" << std::endl;
}

#include <sys/wait.h>
#include <unistd.h>

// =============================================================================
// Test 5: 真实独立包进程跨进程接收与推理全链路验证
// =============================================================================
void test_consumer_subprocess_e2e(const char* consumer_bin) {
    if (!consumer_bin) return;

    uint64_t stream_id = 1004;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    // 启动独立子进程：包形态的算法可执行文件
    pid_t pid = fork();
    if (pid == 0) {
        // 子进程
        execl(consumer_bin, consumer_bin,
              "--broker", broker.socket_path().c_str(),
              "--name", "sub_worker",
              "--max-frames", "3",
              nullptr);
        _exit(127);
    }

    assert(pid > 0);

    // 等待子进程连接（最高等待 3 秒，避免并发测试时 CPU 抢占偶发超时）
    for (int i = 0; i < 150; ++i) {
        if (broker.client_count() == 1) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
    }
    assert(broker.client_count() == 1);

    // 推送 3 帧
    std::vector<uint8_t> dummy(640 * 480 * 3, 100);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    for (int i = 0; i < 3; ++i) {
        frame.pts = 100 + i;
        assert(broker.publish_frame(frame));
        std::this_thread::sleep_for(std::chrono::milliseconds(30));
    }

    // 等待子进程处理完 3 帧退出
    int status = 0;
    pid_t w = waitpid(pid, &status, 0);
    assert(w == pid);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);

    broker.stop();
    std::cout << "[PASS] test_consumer_subprocess_e2e" << std::endl;
}

// =============================================================================
// Test 6: 半包超时拒绝与流脱节防御
// =============================================================================
void test_partial_packet_desync_defense() {
    uint64_t stream_id = 1005;
    FrameBroker broker;
    BrokerOptions opt;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "partial_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 1);

    // 仅写入 4 字节魔数半包 (远小于 16 字节 IpcHeader)
    uint32_t partial_magic = IPC_MAGIC;
    assert(client.send_msg(MSG_UNKNOWN, nullptr, 0)); // 发送合法空包，重置
    // 通过底层 socket 直接发送 4 字节半包
    // UnixSocketClient 没有暴露 raw fd，但可以通过 ::send 模拟
    // 或者用新 socket 也可以
    // 这里我们直接测试一个发送了握手后的客户端发送半包
    struct sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    std::strncpy(addr.sun_path, broker.socket_path().c_str(), sizeof(addr.sun_path) - 1);
    int raw_sock = ::socket(AF_UNIX, SOCK_STREAM, 0);
    assert(raw_sock >= 0);
    assert(::connect(raw_sock, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) == 0);

    // 发送握手使 raw_sock 注册进 broker
    IpcHeader h{};
    h.magic = IPC_MAGIC;
    h.msg_type = MSG_HANDSHAKE_REQ;
    h.version = IPC_VERSION;
    h.payload_size = sizeof(HandshakeReq);
    HandshakeReq raw_req{};
    raw_req.api_version = 1;
    std::strncpy(raw_req.client_name, "raw_client", sizeof(raw_req.client_name) - 1);
    assert(::send(raw_sock, &h, sizeof(h), 0) == sizeof(h));
    assert(::send(raw_sock, &raw_req, sizeof(raw_req), 0) == sizeof(raw_req));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 2);

    // raw_sock 仅发送 4 字节半包
    assert(::send(raw_sock, &partial_magic, sizeof(partial_magic), 0) == sizeof(partial_magic));

    // 等待服务端 100ms 超时检测与 DESYNC 断开回收
    std::this_thread::sleep_for(std::chrono::milliseconds(250));

    // raw_sock 因半包超时被服务端主动切断回收，broker.client_count() 恢复为 1 (仅剩 client)
    assert(broker.client_count() == 1);

    ::close(raw_sock);
    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_partial_packet_desync_defense" << std::endl;
}

// =============================================================================
// Test 7: 畸变超大 Payload 熔断拒绝
// =============================================================================
void test_oversized_payload_rejection() {
    uint64_t stream_id = 1006;
    FrameBroker broker;
    BrokerOptions opt;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "evil_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 1);

    // 发送带有 1MB 超大畸变 payload_size 的头部
    IpcHeader evil_hdr{};
    evil_hdr.magic = IPC_MAGIC;
    evil_hdr.msg_type = MSG_FRAME_ACK;
    evil_hdr.version = IPC_VERSION;
    evil_hdr.payload_size = 1024 * 1024; // 1MB > 64KB 上限
    // 通过 client 发送恶意头部
    struct sockaddr_un addr{};
    addr.sun_family = AF_UNIX;
    std::strncpy(addr.sun_path, broker.socket_path().c_str(), sizeof(addr.sun_path) - 1);
    int raw_sock = ::socket(AF_UNIX, SOCK_STREAM, 0);
    assert(raw_sock >= 0);
    assert(::connect(raw_sock, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) == 0);

    // raw_sock 发送握手
    IpcHeader h{};
    h.magic = IPC_MAGIC;
    h.msg_type = MSG_HANDSHAKE_REQ;
    h.version = IPC_VERSION;
    h.payload_size = sizeof(HandshakeReq);
    HandshakeReq raw_req{};
    raw_req.api_version = 1;
    std::strncpy(raw_req.client_name, "raw_evil", sizeof(raw_req.client_name) - 1);
    assert(::send(raw_sock, &h, sizeof(h), 0) == sizeof(h));
    assert(::send(raw_sock, &raw_req, sizeof(raw_req), 0) == sizeof(raw_req));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 2);

    // raw_sock 发送超大畸变包
    assert(::send(raw_sock, &evil_hdr, sizeof(evil_hdr), 0) == sizeof(evil_hdr));

    // 等待服务端熔断处理
    std::this_thread::sleep_for(std::chrono::milliseconds(100));

    // raw_sock 应已被熔断断开且未发生 crash
    assert(broker.client_count() == 1);

    ::close(raw_sock);
    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_oversized_payload_rejection" << std::endl;
}

// =============================================================================
// Test 8: 独立多平面 YUV420P 安全分平面拷贝
// =============================================================================
void test_multi_planar_yuv420p_copy() {
    uint64_t stream_id = 1007;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 2 * 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "yuv_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));

    ShmPool shm_client;
    assert(shm_client.init(resp.shm_name, ShmMode::OPEN_READ_ONLY));

    // 构造三块独立的 Plane 内存 (非连续物理地址)
    uint32_t width = 320;
    uint32_t height = 240;
    uint32_t stride = 320;
    uint32_t u_stride = 160;
    uint32_t uv_h = 120;

    std::vector<uint8_t> plane_y(stride * height, 0x11);
    std::vector<uint8_t> plane_u(u_stride * uv_h, 0x22);
    std::vector<uint8_t> plane_v(u_stride * uv_h, 0x33);

    HardwareFrame frame;
    frame.width = width;
    frame.height = height;
    frame.stride = stride;
    frame.format = FRAME_FORMAT_YUV420P;
    frame.pts = 12345;
    frame.host_planes[0] = plane_y.data();
    frame.host_planes[1] = plane_u.data();
    frame.host_planes[2] = plane_v.data();
    frame.plane_strides[0] = stride;
    frame.plane_strides[1] = u_stride;
    frame.plane_strides[2] = u_stride;

    assert(broker.publish_frame(frame));

    IpcHeader d_hdr{};
    std::vector<uint8_t> d_payload;
    assert(client.recv_msg(d_hdr, d_payload, 1000));
    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, d_payload.data(), sizeof(dispatch));

    const uint8_t* slot_base = shm_client.slot_read_ptr(dispatch.slot_index);
    assert(slot_base != nullptr);

    // 校验 Y, U, V 分平面拷贝的数据准确性
    const uint8_t* y_data = slot_base + dispatch.offsets[0];
    const uint8_t* u_data = slot_base + dispatch.offsets[1];
    const uint8_t* v_data = slot_base + dispatch.offsets[2];

    assert(y_data[0] == 0x11);
    assert(y_data[stride * height - 1] == 0x11);
    assert(u_data[0] == 0x22);
    assert(u_data[u_stride * uv_h - 1] == 0x22);
    assert(v_data[0] == 0x33);
    assert(v_data[u_stride * uv_h - 1] == 0x33);

    // 发送 ACK
    FrameAckMsg ack{};
    ack.stream_id = stream_id;
    ack.frame_id = dispatch.frame_id;
    ack.slot_index = dispatch.slot_index;
    ack.generation = dispatch.generation;
    ack.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_multi_planar_yuv420p_copy" << std::endl;
}

// =============================================================================
// Test 9: 协议结构体 8 字节自然对齐与编译/运行时断言
// =============================================================================
void test_protocol_alignment_assertions() {
    assert(sizeof(IpcHeader) == 16);
    assert(sizeof(HandshakeReq) == 40);
    assert(sizeof(HandshakeResp) == 80);
    assert(sizeof(IpcMotionBox) == 16);
    assert(LEGACY_FRAME_DISPATCH_SIZE == 96);
    assert(sizeof(FrameDispatchMsg) == 184);
    assert(sizeof(FrameAckMsg) == 32);

    assert(offsetof(FrameDispatchMsg, stream_id) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, frame_id) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, generation) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, pts) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, capture_time_ms) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, has_motion) % 8 == 0);
    assert(offsetof(FrameDispatchMsg, motion_boxes) % 8 == 0);

    assert(offsetof(FrameAckMsg, stream_id) % 8 == 0);
    assert(offsetof(FrameAckMsg, frame_id) % 8 == 0);
    assert(offsetof(FrameAckMsg, generation) % 8 == 0);

    std::cout << "[PASS] test_protocol_alignment_assertions" << std::endl;
}

// =============================================================================
// Test 10: 发送失败租约即时回收与在途状态回滚
// =============================================================================
void test_send_failure_rollback() {
    uint64_t stream_id = 1008;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2; // 仅 2 个槽位
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "rollback_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    // 客户端直接 disconnect 关闭其 socket
    client.disconnect();
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    // 发布帧，断连/发送失败不会使槽位死锁在 LEASED 状态
    broker.publish_frame(frame);
    broker.publish_frame(frame);

    // 再次推帧必须依然能正常获取 FREE 槽位
    assert(broker.publish_frame(frame));

    broker.stop();
    std::cout << "[PASS] test_send_failure_rollback" << std::endl;
}

// =============================================================================
// Test 11: AC4 多路 Broker 的独立 lease/ACK 与非对称隔离
//   两路 broker 各接一个客户端：一路从不 ACK（在途打满后被丢帧），另一路正常 ACK。
//   断言两路的 delivered/dropped/in_flight 计数互不影响——
//   即“一路先耗尽”不会拖垮另一路的投递与租约。
// =============================================================================
void test_multi_broker_asymmetric_isolation() {
    const uint64_t stream_stalled = 3001;
    const uint64_t stream_healthy = 3002;

    BrokerOptions opt;
    // ⚠️ 槽位必须 ≥ 帧数：否则第 3 帧会因**槽位耗尽**而非客户端背压返回 false，
    // 测不出“卡死路在途打满后平滑丢帧、健康路不受影响”这一语义。
    opt.slot_count = 6;
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2;
    // 长租约：确保两路计数差异只可能来自背压，而非超时驱逐
    opt.lease_timeout = std::chrono::milliseconds(10000);

    FrameBroker stalled_broker, healthy_broker;
    assert(stalled_broker.start(stream_stalled, "/tmp", opt));
    assert(healthy_broker.start(stream_healthy, "/tmp", opt));

    IpcHeader hdr{};
    std::vector<uint8_t> payload;

    // 客户端 A：连上后从不 ACK
    UnixSocketClient stalled_client;
    assert(stalled_client.connect(stalled_broker.socket_path()));
    HandshakeReq stalled_req{};
    std::strncpy(stalled_req.client_name, "stalled", sizeof(stalled_req.client_name) - 1);
    stalled_client.send_msg(MSG_HANDSHAKE_REQ, &stalled_req, sizeof(stalled_req));
    HandshakeResp stalled_resp{};
    assert(stalled_client.recv_msg(hdr, payload, 1000));
    std::memcpy(&stalled_resp, payload.data(), sizeof(stalled_resp));

    // 客户端 B：每帧立即 ACK
    UnixSocketClient healthy_client;
    assert(healthy_client.connect(healthy_broker.socket_path()));
    HandshakeReq healthy_req{};
    std::strncpy(healthy_req.client_name, "healthy", sizeof(healthy_req.client_name) - 1);
    healthy_client.send_msg(MSG_HANDSHAKE_REQ, &healthy_req, sizeof(healthy_req));
    HandshakeResp healthy_resp{};
    assert(healthy_client.recv_msg(hdr, payload, 1000));
    std::memcpy(&healthy_resp, payload.data(), sizeof(healthy_resp));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(stalled_broker.client_count() == 1);
    assert(healthy_broker.client_count() == 1);

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    const int frames = 5;
    for (int i = 0; i < frames; ++i) {
        // 两路各自推送，槽位与客户端集合完全独立
        frame.pts = 5000 + i;
        assert(stalled_broker.publish_frame(frame));
        assert(healthy_broker.publish_frame(frame));

        // 健康客户端收到即 ACK，保持在途为 0
        IpcHeader fh{};
        std::vector<uint8_t> fp;
        if (healthy_client.recv_msg(fh, fp, 500)) {
            FrameDispatchMsg dispatch{};
            std::memcpy(&dispatch, fp.data(), sizeof(dispatch));
            FrameAckMsg ack{};
            ack.stream_id = stream_healthy;
            ack.frame_id = dispatch.frame_id;
            ack.slot_index = dispatch.slot_index;
            ack.generation = dispatch.generation;
            ack.client_id = healthy_resp.client_id;
            healthy_client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack));
        }
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }

    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    ClientInfo stalled_info{}, healthy_info{};
    assert(stalled_broker.get_client_info(stalled_resp.client_id, stalled_info));
    assert(healthy_broker.get_client_info(healthy_resp.client_id, healthy_info));

    // 健康路：全部交付、零丢帧，不受另一路耗尽影响
    assert(healthy_info.delivered_frames == static_cast<uint64_t>(frames));
    assert(healthy_info.dropped_frames == 0);

    // 卡死路：在途打满（=2）后剩余帧全部丢弃。
    // 卡死客户端从不 ACK，其 2 个槽位在长租约下持续 LEASED，
    // 后续帧改投新槽位——槽位充足，因此 publish 仍返回 true，但该客户端被计入 dropped。
    assert(stalled_info.delivered_frames == 2);
    assert(stalled_info.dropped_frames == static_cast<uint64_t>(frames - 2));

    // 两路的租约与客户端计数互不串扰
    assert(stalled_info.in_flight_count == 2);
    assert(healthy_info.in_flight_count == 0);
    assert(stalled_broker.client_count() == 1);
    assert(healthy_broker.client_count() == 1);

    stalled_client.disconnect();
    healthy_client.disconnect();
    stalled_broker.stop();
    healthy_broker.stop();
    std::cout << "[PASS] test_multi_broker_asymmetric_isolation" << std::endl;
}

// =============================================================================
// Test 12: AC7 宿主崩溃后用同一 stream_id 重启
//   子进程创建 broker 并映射共享内存后不清理即被 SIGKILL（模拟宿主崩溃）。
//   父进程随后以同一 stream_id 重新 start：必须成功，且槽位与租约是干净的。
// =============================================================================
void test_broker_host_crash_then_restart() {
    const uint64_t stream_id = 3003;
    const std::string base_dir = "/tmp";

    int ready_pipe[2];
    assert(pipe(ready_pipe) == 0);

    pid_t pid = fork();
    assert(pid >= 0);

    if (pid == 0) {
        // 子进程：创建 broker、接入客户端、推入帧，然后阻塞等待被杀死
        close(ready_pipe[0]);

        FrameBroker broker;
        BrokerOptions opt;
        opt.slot_count = 2;
        opt.slot_size = 1024 * 1024;
        opt.max_in_flight_per_client = 2;
        opt.lease_timeout = std::chrono::milliseconds(10000);
        if (!broker.start(stream_id, base_dir, opt)) _exit(10);

        UnixSocketClient client;
        if (!client.connect(broker.socket_path())) _exit(11);
        HandshakeReq req{};
        std::strncpy(req.client_name, "crash_host", sizeof(req.client_name) - 1);
        if (!client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req))) _exit(12);
        IpcHeader hdr{};
        std::vector<uint8_t> payload;
        if (!client.recv_msg(hdr, payload, 1000)) _exit(13);

        std::vector<uint8_t> dummy(640 * 480 * 3, 0);
        HardwareFrame frame;
        frame.width = 640;
        frame.height = 480;
        frame.stride = 640 * 3;
        frame.format = FRAME_FORMAT_UNKNOWN;
        frame.host_ptr = dummy.data();
        frame.pts = 1;
        if (!broker.publish_frame(frame)) _exit(14);

        const char ready = 'R';
        if (write(ready_pipe[1], &ready, 1) != 1) _exit(15);
        close(ready_pipe[1]);

        // 刻意不调用 broker.stop()：模拟宿主被强制杀死，shm 与 socket 残留
        for (;;) pause();
    }

    // 父进程：等子进程就绪后 SIGKILL，不做任何清理
    close(ready_pipe[1]);
    char ready = 0;
    assert(read(ready_pipe[0], &ready, 1) == 1);
    assert(ready == 'R');
    close(ready_pipe[0]);

    assert(kill(pid, SIGKILL) == 0);
    int status = 0;
    assert(waitpid(pid, &status, 0) == pid);
    assert(WIFSIGNALED(status) && WTERMSIG(status) == SIGKILL);

    const std::string expected_socket =
        base_dir + "/zhulong_broker_" + std::to_string(stream_id) + ".sock";
    // 崩溃残留证据：socket 文件未被 unlink
    assert(access(expected_socket.c_str(), F_OK) == 0);

    // 以同一 stream_id 重新 start：预防性 unlink 必须清掉残留并成功重建
    FrameBroker revived;
    BrokerOptions opt;
    opt.slot_count = 2;
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2;
    std::string err;
    assert(revived.start(stream_id, base_dir, opt, &err));
    assert(revived.socket_path() == expected_socket);

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    // 崩溃客户端的租约不得残留：无客户端时两次 publish 都应成功
    frame.pts = 100;
    assert(revived.publish_frame(frame));
    frame.pts = 101;
    assert(revived.publish_frame(frame));

    // 新客户端接入后仍能正常投递，证明槽位与租约状态是干净的
    UnixSocketClient fresh_client;
    assert(fresh_client.connect(revived.socket_path()));
    HandshakeReq fresh_req{};
    std::strncpy(fresh_req.client_name, "fresh", sizeof(fresh_req.client_name) - 1);
    fresh_client.send_msg(MSG_HANDSHAKE_REQ, &fresh_req, sizeof(fresh_req));
    IpcHeader fh{};
    std::vector<uint8_t> fp;
    assert(fresh_client.recv_msg(fh, fp, 1000));

    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    frame.pts = 102;
    assert(revived.publish_frame(frame));
    assert(fresh_client.recv_msg(fh, fp, 1000));
    assert(fh.msg_type == MSG_FRAME_DISPATCH);

    fresh_client.disconnect();
    revived.stop();

    // 正常 stop 后 socket 文件必须被清理
    assert(access(expected_socket.c_str(), F_OK) != 0);
    std::cout << "[PASS] test_broker_host_crash_then_restart" << std::endl;
}

void test_consumer_multi_broker_e2e(const char* consumer_bin) {
    if (!consumer_bin) return;

    uint64_t stream1 = 2001;
    uint64_t stream2 = 2002;
    FrameBroker broker1, broker2;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    assert(broker1.start(stream1, "/tmp", opt));
    assert(broker2.start(stream2, "/tmp", opt));

    // 启动连接多路 broker 的独立包进程
    pid_t pid = fork();
    if (pid == 0) {
        execl(consumer_bin, consumer_bin,
              "--broker", broker1.socket_path().c_str(),
              "--broker", broker2.socket_path().c_str(),
              "--name", "multi_worker",
              "--max-frames", "2",
              nullptr);
        _exit(127);
    }

    assert(pid > 0);

    // 等待两路 Broker 均建立客户端连接（最高等待 3 秒）
    for (int i = 0; i < 150; ++i) {
        if (broker1.client_count() == 1 && broker2.client_count() == 1) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
    }
    assert(broker1.client_count() == 1);
    assert(broker2.client_count() == 1);

    // 向两路分别推送 2 帧
    std::vector<uint8_t> dummy(640 * 480 * 3, 100);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    for (int i = 0; i < 2; ++i) {
        frame.pts = 1000 + i;
        assert(broker1.publish_frame(frame));
        frame.pts = 2000 + i;
        assert(broker2.publish_frame(frame));
        std::this_thread::sleep_for(std::chrono::milliseconds(30));
    }

    int status = 0;
    pid_t w = waitpid(pid, &status, 0);
    assert(w == pid);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);

    broker1.stop();
    broker2.stop();
    std::cout << "[PASS] test_consumer_multi_broker_e2e" << std::endl;
}

void test_consumer_broker_connect_failure_d6(const char* consumer_bin) {
    if (!consumer_bin) return;

    pid_t pid = fork();
    if (pid == 0) {
        execl(consumer_bin, consumer_bin,
              "--broker", "/tmp/zhulong_definitely_nonexistent_broker.sock",
              "--name", "fail_worker",
              nullptr);
        _exit(127);
    }

    assert(pid > 0);
    int status = 0;
    pid_t w = waitpid(pid, &status, 0);
    assert(w == pid);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 5);
    std::cout << "[PASS] test_consumer_broker_connect_failure_d6" << std::endl;
}

// =============================================================================
// 无进展看门狗：卡在推理里的一路必须在阈值后被**受控**终止
// =============================================================================
//
// 这条路径以前完全没有出口：Worker 卡在 process() 里既不读标志位也不发 ACK，
// 宿主只能看到 in-flight 配额被慢慢耗满（engine.h 没有 client-stats ABI 可查），
// 进程就那么挂着。现在骨架自己发现并结束进程，把「卡死」翻译成可诊断、可重启的失败。
//
// 断言三件事：退出码是 10（不是 SIGABRT、不是超时后的 0）、确实发生在阈值附近
// 而不是等 5s 假推理跑完、以及 stderr 真的留下了 stage=worker_stalled（否则运维
// 依然什么都看不到）。
void test_stall_watchdog_terminates_stuck_worker(const char* consumer_bin) {
    if (!consumer_bin) return;

    const uint64_t stream_id = 3004;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    int err_pipe[2];
    assert(::pipe(err_pipe) == 0);

    pid_t pid = fork();
    if (pid == 0) {
        ::close(err_pipe[0]);
        // 收走 stderr：既要验证诊断记录真的发出，也避免污染测试输出。
        if (::dup2(err_pipe[1], STDERR_FILENO) != STDERR_FILENO) {
            _exit(127);
        }
        ::close(err_pipe[1]);
        ::setenv("ZHULONG_STUB_DETECT_SLEEP_MS", "5000", 1);
        ::setenv("ZHULONG_WORKER_STALL_TIMEOUT_MS", "300", 1);
        execl(consumer_bin, consumer_bin,
              "--broker", broker.socket_path().c_str(),
              "--name", "stall_watchdog_consumer",
              "--max-frames", "1",
              nullptr);
        _exit(127);
    }
    assert(pid > 0);
    ::close(err_pipe[1]);

    // 没有握手完成的 client 时 publish_frame 直接丢帧，看门狗也就无从计时。
    for (int i = 0; i < 150; ++i) {
        if (broker.client_count() == 1) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
    }
    assert(broker.client_count() == 1);

    std::vector<uint8_t> dummy(640 * 480 * 3, 100);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();
    frame.pts = 100;
    assert(broker.publish_frame(frame));

    // 子进程退出 → 管道 EOF。最坏情况（看门狗失效）是 5s 后以 0 退出，不会挂死。
    std::string captured;
    char buf[512];
    ssize_t n = 0;
    while ((n = ::read(err_pipe[0], buf, sizeof(buf))) > 0) {
        captured.append(buf, static_cast<size_t>(n));
    }
    ::close(err_pipe[0]);

    int status = 0;
    assert(::waitpid(pid, &status, 0) == pid);
    assert(WIFEXITED(status));
    // 10 = kExitWorkerStalled（native/sdk/src/internal/stall_watch.hpp）。
    // 刻意写字面量：这是对外的退出码契约，与 Go 侧常量各自独立钉住。
    assert(WEXITSTATUS(status) == 10);
    assert(captured.find("worker_stalled") != std::string::npos);

    broker.stop();
    std::cout << "[PASS] test_stall_watchdog_terminates_stuck_worker" << std::endl;
}

// =============================================================================
// Test: Runner --algorithm-id 透传至 MSG_RESULT 载荷 (algorithm_id) 跨进程端到端验证
// =============================================================================
void test_consumer_result_algorithm_id_e2e(const char* consumer_bin) {
    if (!consumer_bin) return;

    uint64_t stream_id = 2007;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    const auto seed = [&](uint16_t type, const std::string& json) {
        TaskConfigMsg header{};
        header.config_json_len=static_cast<uint32_t>(json.size());
        std::strncpy(header.algorithm_id,"e2e_algo",sizeof(header.algorithm_id)-1);
        std::string payload(sizeof(header),'\0');
        std::memcpy(payload.data(),&header,sizeof(header)); payload+=json;
        assert(broker.broadcast_message(type,payload.data(),payload.size())==0);
    };
    seed(MSG_TASK_REGIONS,R"({"tripwires":[{"id":"entry","name":"入口","points":[[0.2,0.5],[0.8,0.5]],"direction":"a_to_b"}]})");
    seed(MSG_TASK_CONFIG,R"({"threshold":0.7})");

    // 捕获 runner 经 MSG_RESULT 上报的载荷。回调由 broker 接收线程异步触发，
    // 因此下方必须做有界轮询等待，不能依赖 waitpid 之后立即断言。
    std::atomic<bool> received{false};
    std::string received_payload;
    broker.set_on_result([&](uint64_t /*s_id*/, uint32_t /*client_id*/, const uint8_t* payload, size_t size) {
        received_payload = std::string(reinterpret_cast<const char*>(payload), size);
        received.store(true);
    });

    // 启动独立子进程：包形态的算法可执行文件，显式传入 --algorithm-id
    pid_t pid = fork();
    if (pid == 0) {
        execl(consumer_bin, consumer_bin,
              "--broker", broker.socket_path().c_str(),
              "--name", "algo_id_worker",
              "--algorithm-id", "e2e_algo",
              "--max-frames", "3",
              nullptr);
        _exit(127);
    }

    assert(pid > 0);

    // 等待子进程连接（最高等待 3 秒，避免并发测试时 CPU 抢占偶发超时）
    for (int i = 0; i < 150; ++i) {
        if (broker.client_count() == 1) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(20));
    }
    assert(broker.client_count() == 1);

    // 推送 3 帧；MockDetector 返回 count>0 的检测结果，runner 必定上报 MSG_RESULT
    std::vector<uint8_t> dummy(640 * 480 * 3, 100);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.host_ptr = dummy.data();

    for (int i = 0; i < 3; ++i) {
        frame.pts = 100 + i;
        assert(broker.publish_frame(frame));
        std::this_thread::sleep_for(std::chrono::milliseconds(30));
    }

    // 等待子进程处理完 3 帧退出
    int status = 0;
    pid_t w = waitpid(pid, &status, 0);
    assert(w == pid);
    assert(WIFEXITED(status) && WEXITSTATUS(status) == 0);

    // ⚠️ waitpid 返回不代表 broker 接收线程的回调已执行，必须有界轮询等待置位
    for (int i = 0; i < 50; ++i) {
        if (received.load()) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }
    assert(received.load());

    // 先 stop 再读载荷：回调由 server 的 detach 读线程执行，本用例会收到多次
    // MSG_RESULT；stop() 会等待所有读线程退出，此后读取 received_payload 才无竞态。
    broker.stop();

    assert(received_payload.find("\"algorithm_id\":\"e2e_algo\"") != std::string::npos);

    assert(received_payload.find("tripwires") != std::string::npos);
    assert(received_payload.find("a_to_b") != std::string::npos);
    assert(received_payload.find("params-ok") != std::string::npos);
    assert(received_payload.find("|revision=1|view=known") != std::string::npos);

    std::cout << "[PASS] test_consumer_result_algorithm_id_e2e" << std::endl;
}

void test_frame_broker_result_egress() {
    uint64_t stream_id = 999;
    FrameBroker broker;
    std::string err;
    assert(broker.start(stream_id, "/tmp", {}, &err));

    std::atomic<bool> received{false};
    std::string received_payload;
    uint64_t received_stream_id = 0;

    broker.set_on_result([&](uint64_t s_id, uint32_t /*client_id*/, const uint8_t* payload, size_t size) {
        received_stream_id = s_id;
        received_payload = std::string(reinterpret_cast<const char*>(payload), size);
        received.store(true);
    });

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));

    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "result_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader hdr{};
    std::vector<uint8_t> resp_payload;
    assert(client.recv_msg(hdr, resp_payload, 500));
    assert(hdr.msg_type == MSG_HANDSHAKE_RESP);

    std::string sample_result = "{\"stream_id\":999,\"task_type\":\"detection\",\"detections\":[]}";
    assert(client.send_msg(MSG_RESULT, sample_result.data(), sample_result.size()));

    // Wait for callback
    for (int i = 0; i < 50; ++i) {
        if (received.load()) break;
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }
    assert(received.load());
    assert(received_stream_id == stream_id);
    assert(received_payload == sample_result);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_result_egress" << std::endl;
}

// =============================================================================
// Test 13: FrameBroker 下行控制消息广播验证 (MSG_GALLERY_*)
// =============================================================================
void test_frame_broker_control_broadcast() {
    uint64_t stream_id = 9002;
    BrokerOptions opts;
    opts.slot_count = 2;
    opts.slot_size = 1024 * 1024;
    FrameBroker broker;
    std::string err;
    assert(broker.start(stream_id, "/tmp", opts, &err));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));

    HandshakeReq req{};
    req.client_id = 1;
    req.api_version = 1;
    std::strncpy(req.client_name, "broadcast_test_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> resp_payload;
    assert(client.recv_msg(resp_hdr, resp_payload, 2000));
    assert(resp_hdr.msg_type == MSG_HANDSHAKE_RESP);

    // FrameBroker 广播下行控制消息
    std::string gallery_json = R"({"face_id":"f1","person_id":"p1"})";
    size_t sent_count = broker.broadcast_message(MSG_GALLERY_UPSERT, gallery_json.data(), gallery_json.size());
    assert(sent_count == 1);

    // 客户端应立即接收到 MSG_GALLERY_UPSERT 消息
    IpcHeader gallery_hdr{};
    std::vector<uint8_t> gallery_payload;
    assert(client.recv_msg(gallery_hdr, gallery_payload, 2000));
    assert(gallery_hdr.msg_type == MSG_GALLERY_UPSERT);
    assert(std::string(gallery_payload.begin(), gallery_payload.end()) == gallery_json);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_control_broadcast" << std::endl;
}

// =============================================================================
// Test 12b: MSG_TASK_CONFIG 必须被缓存并在握手后重放
//   真实故障形态：宿主在包进程还没连上（或崩溃重启期间）推送任务级配置，广播打到 0 个
//   客户端；没有缓存 + 重放，那次配置就永久丢了，包进程只会跑在编译期默认值上。
//   断言：
//   - 先推（无客户端）再连，握手后收到同一条配置；
//   - 推第二条后重连，重放的是**最新**那条；
//   - 其他消息类型不参与重放（不能顺手把底库增量也重放一遍）。
// =============================================================================
void test_frame_broker_task_config_cache_replay() {
    const uint64_t stream_id = 9003;
    BrokerOptions opts;
    opts.slot_count = 2;
    opts.slot_size = 1024 * 1024;
    FrameBroker broker;
    std::string err;
    assert(broker.start(stream_id, "/tmp", opts, &err));

    const auto encode = [](const std::string& id, const std::string& json) {
        TaskConfigMsg header{};
        header.config_json_len = static_cast<uint32_t>(json.size());
        std::strncpy(header.algorithm_id, id.c_str(), sizeof(header.algorithm_id)-1);
        std::string payload(sizeof(header), '\0');
        std::memcpy(payload.data(), &header, sizeof(header));
        return payload + json;
    };
    const std::string face = encode("face", R"({"threshold":0.55})");
    const std::string vehicle = encode("vehicle", R"({"threshold":0.8})");
    const std::string regions = encode("face", R"({"tripwires":[{"id":"entry"}]})");
    assert(broker.broadcast_message(MSG_TASK_CONFIG, face.data(), face.size()) == 0);
    assert(broker.broadcast_message(MSG_TASK_CONFIG, vehicle.data(), vehicle.size()) == 0);
    assert(broker.broadcast_message(MSG_TASK_REGIONS, regions.data(), regions.size()) == 0);
    const auto handshake_and_expect = [&](const std::string& expected_regions) {
        UnixSocketClient client;
        assert(client.connect(broker.socket_path()));
        HandshakeReq req{};
        req.client_id = 1;
        req.api_version = 1;
        std::strncpy(req.client_name, "task_config_client", sizeof(req.client_name)-1);
        assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));
        IpcHeader hdr{};
        std::vector<uint8_t> payload;
        assert(client.recv_msg(hdr, payload, 2000));
        assert(hdr.msg_type == MSG_HANDSHAKE_RESP);
        bool got_face=false, got_vehicle=false, got_regions=false;
        for (int i=0;i<3;++i) {
            assert(client.recv_msg(hdr,payload,2000));
            const std::string value(payload.begin(),payload.end());
            if (hdr.msg_type == MSG_TASK_REGIONS) { assert(value==expected_regions); got_regions=true; }
            else { assert(hdr.msg_type==MSG_TASK_CONFIG); if(value==face) got_face=true; else {assert(value==vehicle);got_vehicle=true;} }
        }
        assert(got_face && got_vehicle && got_regions);
        client.disconnect();
    };
    handshake_and_expect(regions);
    const std::string cleared = encode("face", "{}");
    assert(broker.broadcast_message(MSG_TASK_REGIONS, cleared.data(), cleared.size()) == 0);
    handshake_and_expect(cleared);

    broker.stop();
    std::cout << "[PASS] test_frame_broker_task_config_cache_replay" << std::endl;
}

// =============================================================================
// Test 13: REQ-1 槽位容量不足必须显式失败，禁止静默发旧帧
//   构造 payload 超过 slot_size 的帧，断言：
//   - publish_frame 返回 false；
//   - 客户端**未收到任何** MSG_FRAME_DISPATCH（旧实现在此会发旧像素）；
//   - total_dropped() 递增；
//   - 该客户端在途配额被完整归还（in_flight_count == 0）；
//   - 随后一个合法帧能正常发布并送达（证明槽位已回到 FREE，门禁不误伤）。
// =============================================================================
void test_frame_broker_slot_capacity_reject() {
    const uint64_t stream_id = 1010;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2;
    opt.slot_size = 256 * 1024; // 256KB，故意小于 640x480x3 = 900KB
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "capacity_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 1);

    const uint64_t dropped_before = broker.total_dropped();

    // --- 超大帧：900KB > 256KB 槽位 ---
    std::vector<uint8_t> oversize(640 * 480 * 3, 0x5A);
    HardwareFrame big;
    big.width = 640;
    big.height = 480;
    big.stride = 640 * 3;
    big.format = FRAME_FORMAT_UNKNOWN; // BGR24 通路
    big.pts = 1;
    big.host_ptr = oversize.data();

    assert(broker.publish_frame(big) == false);
    assert(broker.total_dropped() == dropped_before + 1);

    // ⚠️ 核心断言：客户端绝不能收到通知。旧实现在此会发出 MSG_FRAME_DISPATCH，
    //    而槽位里是上一帧（首帧时是未初始化）像素。
    IpcHeader no_hdr{};
    std::vector<uint8_t> no_payload;
    assert(client.recv_msg(no_hdr, no_payload, 300) == false);

    // 配额必须已完整归还（否则该客户端会被静默饿死）
    ClientInfo info{};
    assert(broker.get_client_info(resp.client_id, info));
    assert(info.in_flight_count == 0);
    assert(info.delivered_frames == 0);
    assert(info.dropped_frames == 1);

    // --- 合法帧：必须能发布并送达，证明槽位已回到 FREE ---
    const size_t legal_bytes = 200 * 1024; // 200KB < 256KB
    std::vector<uint8_t> legal(legal_bytes, 0x11);
    HardwareFrame small;
    small.width = 320;
    small.height = 213; // 320 * 213 * 3 ≈ 204KB 行跨度按 320*3 计算
    small.stride = 320 * 3;
    small.format = FRAME_FORMAT_UNKNOWN;
    small.pts = 2;
    small.host_ptr = legal.data();
    // 保证 payload = stride * height <= slot_size
    small.height = static_cast<uint32_t>(legal_bytes / (320 * 3));
    assert(static_cast<size_t>(small.stride) * small.height <= opt.slot_size);

    assert(broker.publish_frame(small) == true);

    IpcHeader ok_hdr{};
    std::vector<uint8_t> ok_payload;
    assert(client.recv_msg(ok_hdr, ok_payload, 1000));
    assert(ok_hdr.msg_type == MSG_FRAME_DISPATCH);
    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, ok_payload.data(), sizeof(dispatch));
    assert(dispatch.pts == 2);
    // ⚠️ REQ-3：即使本次发布未携带 SCM_RIGHTS fd，reserved_fd 也必须恒为 -1。
    assert(dispatch.reserved_fd == -1);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_slot_capacity_reject" << std::endl;
}

// =============================================================================
// Test 14: REQ-6 畸形 ACK 不得蚕食在途配额
//   发送 slot_index 越界的 ACK，断言 in_flight_count 保持不变；
//   随后发合法 ACK 仍能正常归还（证明槽位未被破坏）。
// =============================================================================
void test_frame_broker_malformed_ack_keeps_quota() {
    const uint64_t stream_id = 1011;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "malformed_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.pts = 7;
    frame.host_ptr = dummy.data();
    assert(broker.publish_frame(frame));

    IpcHeader d_hdr{};
    std::vector<uint8_t> d_payload;
    assert(client.recv_msg(d_hdr, d_payload, 1000));
    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, d_payload.data(), sizeof(dispatch));

    ClientInfo before{};
    assert(broker.get_client_info(resp.client_id, before));
    assert(before.in_flight_count == 1);

    // --- 畸形 ACK：slot_index 远超 slot_count ---
    FrameAckMsg bad{};
    bad.stream_id = stream_id;
    bad.frame_id = dispatch.frame_id;
    bad.slot_index = 0xFFFFFFFFu;
    bad.generation = dispatch.generation;
    bad.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &bad, sizeof(bad)));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    // ⚠️ 核心断言：配额不得被扣（旧实现会扣掉且不还槽位）
    ClientInfo after_bad{};
    assert(broker.get_client_info(resp.client_id, after_bad));
    assert(after_bad.in_flight_count == 1);

    // --- 合法 ACK：仍能正常归还，证明槽位未被畸形 ACK 破坏 ---
    FrameAckMsg good{};
    good.stream_id = stream_id;
    good.frame_id = dispatch.frame_id;
    good.slot_index = dispatch.slot_index;
    good.generation = dispatch.generation;
    good.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &good, sizeof(good)));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    ClientInfo after_good{};
    assert(broker.get_client_info(resp.client_id, after_good));
    assert(after_good.in_flight_count == 0);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_malformed_ack_keeps_quota" << std::endl;
}

// =============================================================================
// Test: broker 分发带运动检测元数据的帧
// =============================================================================
void test_frame_broker_motion_dispatch() {
    const uint64_t stream_id = 999;
    FrameBroker broker;
    assert(broker.start(stream_id));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));

    HandshakeReq req{};
    req.client_id = 0;
    std::strcpy(req.client_name, "motion_tester");
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.pts = 100;
    frame.host_ptr = dummy.data();

    FrameMotionInfo motion{};
    motion.has_motion = 1;
    motion.motion_box_count = 2;
    motion.motion_boxes[0] = IpcMotionBox{0.1f, 0.2f, 0.3f, 0.4f};
    motion.motion_boxes[1] = IpcMotionBox{0.5f, 0.6f, 0.7f, 0.8f};

    assert(broker.publish_frame(frame, motion));

    IpcHeader d_hdr{};
    std::vector<uint8_t> d_payload;
    assert(client.recv_msg(d_hdr, d_payload, 1000));
    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, d_payload.data(), sizeof(dispatch));

    assert(dispatch.has_motion == 1);
    assert(dispatch.motion_box_count == 2);
    assert(std::abs(dispatch.motion_boxes[0].x1 - 0.1f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[0].y1 - 0.2f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[0].x2 - 0.3f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[0].y2 - 0.4f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[1].x1 - 0.5f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[1].y1 - 0.6f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[1].x2 - 0.7f) < 1e-4f);
    assert(std::abs(dispatch.motion_boxes[1].y2 - 0.8f) < 1e-4f);

    FrameAckMsg ack{};
    ack.stream_id = stream_id;
    ack.frame_id = dispatch.frame_id;
    ack.slot_index = dispatch.slot_index;
    ack.generation = dispatch.generation;
    ack.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack)));

    // 不传运动元数据的调用（默认参数）必须表现为「全帧放行」的 fail-open，
    // 而不是 has_motion = 0 把整条流的推理关掉。
    assert(broker.publish_frame(frame));
    FrameDispatchMsg default_dispatch{};
    assert(client.recv_msg(d_hdr, d_payload, 1000));
    std::memcpy(&default_dispatch, d_payload.data(), sizeof(default_dispatch));
    assert(default_dispatch.has_motion == 1);
    assert(default_dispatch.motion_box_count == 0);
    assert(default_dispatch.motion_boxes[0].x1 == 0.0f);

    ack.frame_id = default_dispatch.frame_id;
    ack.slot_index = default_dispatch.slot_index;
    ack.generation = default_dispatch.generation;
    assert(client.send_msg(MSG_FRAME_ACK, &ack, sizeof(ack)));

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_motion_dispatch" << std::endl;
}

// =============================================================================
// Test: broker 创建必须等待宿主资源目录锁
//
// 宿主启动清扫（internal/algorunner）与 FrameBroker::start 共享 broker 资源目录锁。
// 清扫判断「无人使用」依赖 /proc，而 shm_open→mmap 与 bind→listen 之间存在窗口，
// 此时资源已可见却尚不可用。本测试持有锁并断言：创建侧在此期间不得发布任何资源，
// 释放锁后才完成创建。没有这把锁，清扫会删掉正被创建的 broker 资源。
// =============================================================================
void test_frame_broker_start_waits_for_resource_lock() {
    const uint64_t stream_id = 1200;
    std::string base_dir = "/tmp/zhulong_lock_test_" + std::to_string(::getpid());
    ::mkdir(base_dir.c_str(), 0755);
    const std::string sock = base_dir + "/zhulong_broker_" + std::to_string(stream_id) + ".sock";

    std::string err;
    // 与创建侧同源：shm 目录只在存在可枚举挂载点的平台上纳入锁集。
    auto held = BrokerResourceLock::acquire(BrokerResourceLock::resource_dirs(base_dir), &err);
    assert(held);
    assert(held->size() >= 1); // 去重后至少一把锁（base_dir；/dev/shm 可能与其同源或不存在）

    std::atomic<bool> finished{false};
    std::atomic<bool> started{false};
    FrameBroker broker;
    std::thread creator([&]() {
        BrokerOptions opt;
        opt.slot_count = 2;
        started.store(broker.start(stream_id, base_dir, opt, &err));
        finished.store(true);
    });

    // 锁在创建侧手里等待时，任何资源都不得出现。
    std::this_thread::sleep_for(std::chrono::milliseconds(200));
    bool released_early = finished.load();
    if (!released_early) {
        struct stat st{};
        released_early = (::stat(sock.c_str(), &st) == 0);
    }

    held.reset(); // 释放锁：创建侧应当立刻完成
    creator.join();

    assert(!released_early);
    assert(finished.load());
    assert(started.load());
    struct stat st{};
    assert(::stat(sock.c_str(), &st) == 0);
    assert(broker.is_running());

    broker.stop();
    ::unlink(sock.c_str());
    ::rmdir(base_dir.c_str());
    std::cout << "[PASS] test_frame_broker_start_waits_for_resource_lock" << std::endl;
}

// =============================================================================
// Test 18: 驱逐后的迟到 ACK 与重复 ACK 不得重复扣减客户端配额
// =============================================================================
void test_frame_broker_evicted_ack_does_not_double_deduct() {
    const uint64_t stream_id = 1018;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 1; // 仅 1 个槽位
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2;
    opt.lease_timeout = std::chrono::milliseconds(80); // 80ms 快速超时
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "eviction_ack_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    std::vector<uint8_t> dummy(640 * 480 * 3, 0);
    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640 * 3;
    frame.format = FRAME_FORMAT_UNKNOWN;
    frame.pts = 100;
    frame.host_ptr = dummy.data();

    // 1. 发布第一帧
    assert(broker.publish_frame(frame));
    IpcHeader d1_hdr{};
    std::vector<uint8_t> d1_payload;
    assert(client.recv_msg(d1_hdr, d1_payload, 1000));
    FrameDispatchMsg disp1{};
    std::memcpy(&disp1, d1_payload.data(), sizeof(disp1));

    ClientInfo cinfo{};
    assert(broker.get_client_info(resp.client_id, cinfo));
    assert(cinfo.in_flight_count == 1);

    // 等待 120ms 超过 lease_timeout (80ms)
    std::this_thread::sleep_for(std::chrono::milliseconds(120));

    // 2. 发布第二帧：触发超时驱逐，在途计数由 1 扣为 0，随后第二帧分配给 client 使在途计数再次为 1
    frame.pts = 101;
    assert(broker.publish_frame(frame));
    IpcHeader d2_hdr{};
    std::vector<uint8_t> d2_payload;
    assert(client.recv_msg(d2_hdr, d2_payload, 1000));
    FrameDispatchMsg disp2{};
    std::memcpy(&disp2, d2_payload.data(), sizeof(disp2));
    assert(disp2.generation > disp1.generation);

    assert(broker.get_client_info(resp.client_id, cinfo));
    assert(cinfo.in_flight_count == 1);

    // 3. 客户端发送针对已驱逐帧（disp1）的历史迟到 ACK
    FrameAckMsg stale_ack{};
    stale_ack.stream_id = stream_id;
    stale_ack.frame_id = disp1.frame_id;
    stale_ack.slot_index = disp1.slot_index;
    stale_ack.generation = disp1.generation; // 旧代次
    stale_ack.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &stale_ack, sizeof(stale_ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(30));

    // ⚠️ 核心断言：迟到 ACK 必须被丢弃，不得把第二帧的在途计数扣减！
    assert(broker.get_client_info(resp.client_id, cinfo));
    assert(cinfo.in_flight_count == 1);

    // 4. 客户端确认第二帧
    FrameAckMsg valid_ack{};
    valid_ack.stream_id = stream_id;
    valid_ack.frame_id = disp2.frame_id;
    valid_ack.slot_index = disp2.slot_index;
    valid_ack.generation = disp2.generation;
    valid_ack.client_id = resp.client_id;
    assert(client.send_msg(MSG_FRAME_ACK, &valid_ack, sizeof(valid_ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(30));

    assert(broker.get_client_info(resp.client_id, cinfo));
    assert(cinfo.in_flight_count == 0);

    // 5. 重复发送同一次 ACK，配额不得再次被扣减
    assert(client.send_msg(MSG_FRAME_ACK, &valid_ack, sizeof(valid_ack)));
    std::this_thread::sleep_for(std::chrono::milliseconds(30));
    assert(broker.get_client_info(resp.client_id, cinfo));
    assert(cinfo.in_flight_count == 0);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_evicted_ack_does_not_double_deduct" << std::endl;
}

// =============================================================================
// Test 19: BrokerOptions 参数校验防御
// =============================================================================
void test_frame_broker_options_validation() {
    FrameBroker broker;
    std::string err;

    BrokerOptions bad1;
    bad1.slot_count = 0;
    assert(!broker.start(1019, "/tmp", bad1, &err));
    assert(!err.empty());

    BrokerOptions bad2;
    bad2.slot_size = 0;
    assert(!broker.start(1019, "/tmp", bad2, &err));

    BrokerOptions bad3;
    bad3.max_in_flight_per_client = 0;
    assert(!broker.start(1019, "/tmp", bad3, &err));

    std::cout << "[PASS] test_frame_broker_options_validation" << std::endl;
}

// =============================================================================
// Test 20: 握手请求非 \0 结尾客户端名字的安全边界
// =============================================================================
void test_frame_broker_handshake_non_null_terminated_name() {
    const uint64_t stream_id = 1020;
    FrameBroker broker;
    assert(broker.start(stream_id, "/tmp"));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));

    HandshakeReq req{};
    req.api_version = 1;
    // 故意填满整个 32 字节，不保留末尾 '\0'
    std::memset(req.client_name, 'X', sizeof(req.client_name));
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    ClientInfo info{};
    assert(broker.get_client_info(resp.client_id, info));
    assert(info.name.size() == sizeof(req.client_name));
    assert(info.name == std::string(sizeof(req.client_name), 'X'));

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_handshake_non_null_terminated_name" << std::endl;
}

// =============================================================================
// Test 21: NV12 格式下 vstride < height 的归一化防御
// =============================================================================
void test_frame_broker_nv12_vstride_normalization() {
    const uint64_t stream_id = 1021;
    FrameBroker broker;
    assert(broker.start(stream_id, "/tmp"));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "nv12_norm_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> p;
    assert(client.recv_msg(resp_hdr, p, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, p.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(20));

    std::vector<uint8_t> dummy_y(640 * 480, 0x12);
    std::vector<uint8_t> dummy_uv(640 * 240, 0x34);

    HardwareFrame frame;
    frame.width = 640;
    frame.height = 480;
    frame.stride = 640;
    // ⚠️ 模拟解码器输出异常元数据：vstride 给出 320（小于 height 480）
    frame.vstride = 320;
    frame.format = FRAME_FORMAT_NV12;
    frame.host_planes[0] = dummy_y.data();
    frame.host_planes[1] = dummy_uv.data();

    assert(broker.publish_frame(frame));

    IpcHeader d_hdr{};
    std::vector<uint8_t> d_payload;
    assert(client.recv_msg(d_hdr, d_payload, 1000));
    FrameDispatchMsg disp{};
    std::memcpy(&disp, d_payload.data(), sizeof(disp));

    // ⚠️ 核心断言：offsets[1] 必须至少是 stride * height = 640 * 480，绝不能是 640 * 320
    assert(disp.offsets[1] == 640 * 480);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_nv12_vstride_normalization" << std::endl;
}

// =============================================================================
// 视图窗口发布：窗口激活时必须物化到 slot（紧打包），绝不转发全幅 fd
// =============================================================================
void test_frame_broker_windowed_publish() {
    const uint64_t stream_id = 1901;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    opt.max_in_flight_per_client = 2;
    opt.lease_timeout = std::chrono::milliseconds(300);

    std::string err;
    assert(broker.start(stream_id, "/tmp", opt, &err));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path(), &err));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "window_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> resp_payload;
    assert(client.recv_msg(resp_hdr, resp_payload, 1000));
    assert(resp_hdr.msg_type == MSG_HANDSHAKE_RESP);
    HandshakeResp resp{};
    std::memcpy(&resp, resp_payload.data(), sizeof(resp));
    ShmPool shm;
    assert(shm.init(resp.shm_name, ShmMode::OPEN_READ_ONLY, 0, 0, &err));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));

    // 源帧：320×240 NV12，stride 320，vstride 240（单块连续）。
    const int src_w = 320;
    const int src_h = 240;
    std::vector<uint8_t> src(static_cast<size_t>(src_w) * src_h * 3 / 2, 0);
    for (int r = 0; r < src_w * src_h; ++r) {
        src[static_cast<size_t>(r)] = static_cast<uint8_t>(r & 0xFF);
    }

    HardwareFrame frame;
    frame.width = src_w;
    frame.height = src_h;
    frame.stride = src_w;
    frame.vstride = src_h;
    frame.format = FRAME_FORMAT_NV12;
    frame.pts = 4242;
    frame.host_ptr = src.data();

    const ViewWindow view{true, 32, 16, 128, 64};
    CropCopyFn crop = [](const HardwareFrame& f, const ViewWindow& v, uint8_t* dst,
                         size_t dst_size, int dst_stride, int /*dst_fd*/) {
        const PackedLayout layout = packed_layout_for(v, f.format);
        if (!layout.valid || dst == nullptr || dst_size < layout.bytes) {
            return false;
        }
        const uint8_t* y = f.host_ptr;
        for (int r = 0; r < v.h; ++r) {
            std::memcpy(dst + static_cast<size_t>(r) * dst_stride,
                        y + static_cast<size_t>(v.y + r) * f.stride + v.x, v.w);
        }
        const uint8_t* uv_src = y + static_cast<size_t>(f.stride) * f.vstride;
        uint8_t* uv_dst = dst + layout.offsets[1];
        for (int r = 0; r < v.h / 2; ++r) {
            std::memcpy(uv_dst + static_cast<size_t>(r) * dst_stride,
                        uv_src + static_cast<size_t>(v.y / 2 + r) * f.stride + v.x, v.w);
        }
        return true;
    };

    ipc::FrameMotionInfo motion{};
    assert(broker.publish_frame(frame, view, motion, crop, 1700000000123LL));

    IpcHeader dispatch_hdr{};
    std::vector<uint8_t> dispatch_payload;
    int dispatched_fd = -1;
    assert(client.recv_msg_with_fd(dispatch_hdr, dispatch_payload, dispatched_fd, 1000));
    assert(dispatch_hdr.msg_type == MSG_FRAME_DISPATCH);

    FrameDispatchMsg dispatch{};
    std::memcpy(&dispatch, dispatch_payload.data(), sizeof(dispatch));
    assert(dispatch.input_view[0] == static_cast<float>(view.x) / src_w);
    assert(dispatch.input_view[1] == static_cast<float>(view.y) / src_h);
    assert(dispatch.input_view[2] == static_cast<float>(view.x + view.w) / src_w);
    assert(dispatch.input_view[3] == static_cast<float>(view.y + view.h) / src_h);
    assert(dispatch.width == view.w);
    assert(dispatch.height == view.h);
    assert(dispatch.format == ZHULONG_PIXEL_FORMAT_NV12);
    assert(dispatch.strides[0] == 128);           // align_up(128, 64) = 128
    assert(dispatch.offsets[1] == 128u * 64u);    // 紧打包：UV 紧随 Y
    assert(dispatch.capture_time_ms == 1700000000123LL);
    assert(dispatch.reserved_fd == -1);

    // 有 dma-heap 的机器走 DMA 槽位（只会写一个目标）；否则走 shm 槽位。
    const PackedLayout layout = packed_layout_for(view, FRAME_FORMAT_NV12);
    void* dma_map = nullptr;
    const uint8_t* slot = nullptr;
    if (dispatched_fd >= 0) {
        assert((dispatch.flags & FRAME_DISPATCH_FLAG_DMA_FD) != 0);
        dma_map = ::mmap(nullptr, layout.bytes, PROT_READ, MAP_SHARED, dispatched_fd, 0);
        assert(dma_map != MAP_FAILED);
        slot = static_cast<const uint8_t*>(dma_map);
    } else {
        slot = shm.slot_read_ptr(dispatch.slot_index);
    }
    assert(slot != nullptr);

    const uint8_t* src_y = src.data() + static_cast<size_t>(view.y) * src_w + view.x;
    assert(slot[0] == src_y[0]);
    assert(slot[1] == src_y[1]);
    const uint8_t* row1 = slot + dispatch.strides[0];
    assert(row1[0] == src_y[src_w]);
    const uint8_t* uv_slot = slot + dispatch.offsets[1];
    const uint8_t* uv_src = src.data() + static_cast<size_t>(src_w) * src_h +
                            static_cast<size_t>(view.y / 2) * src_w + view.x;
    assert(uv_slot[0] == uv_src[0]);

    // 回调失败必须 fail_publish：绝不能把半张帧发给算法。
    bool called = false;
    CropCopyFn failing = [&called](const HardwareFrame&, const ViewWindow&, uint8_t*, size_t,
                                   int, int) {
        called = true;
        return false;
    };
    assert(!broker.publish_frame(frame, view, motion, failing, 0));
    assert(called);

    if (dma_map != nullptr) {
        ::munmap(dma_map, layout.bytes);
    }
    if (dispatched_fd >= 0) {
        ::close(dispatched_fd);
    }
    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_windowed_publish" << std::endl;
}

// =============================================================================
// Test: publish_frame 必须拒绝空源指针与 stride < width，绝不发送旧槽位字节
// =============================================================================
void test_frame_broker_invalid_source_rejected() {
    const uint64_t stream_id = 1902;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client;
    assert(client.connect(broker.socket_path()));
    HandshakeReq req{};
    req.api_version = 1;
    std::strncpy(req.client_name, "invalid_src_client", sizeof(req.client_name) - 1);
    assert(client.send_msg(MSG_HANDSHAKE_REQ, &req, sizeof(req)));

    IpcHeader resp_hdr{};
    std::vector<uint8_t> resp_payload;
    assert(client.recv_msg(resp_hdr, resp_payload, 1000));
    HandshakeResp resp{};
    std::memcpy(&resp, resp_payload.data(), sizeof(resp));
    std::this_thread::sleep_for(std::chrono::milliseconds(50));
    assert(broker.client_count() == 1);

    // 1. BGR24 空 host_ptr
    HardwareFrame null_bgr{};
    null_bgr.width = 64;
    null_bgr.height = 64;
    null_bgr.stride = 64 * 3;
    null_bgr.format = FRAME_FORMAT_UNKNOWN;
    assert(!broker.publish_frame(null_bgr));

    // 2. BGR24 stride < width * 3
    std::vector<uint8_t> buf(64 * 64 * 3, 1);
    HardwareFrame short_bgr{};
    short_bgr.width = 64;
    short_bgr.height = 64;
    short_bgr.stride = 64; // < 64 * 3
    short_bgr.format = FRAME_FORMAT_UNKNOWN;
    short_bgr.host_ptr = buf.data();
    assert(!broker.publish_frame(short_bgr));

    // 3. NV12 缺失平面且 host_ptr 为空
    HardwareFrame null_nv12{};
    null_nv12.width = 64;
    null_nv12.height = 64;
    null_nv12.stride = 64;
    null_nv12.format = FRAME_FORMAT_NV12;
    null_nv12.host_planes[0] = buf.data(); // 缺少 host_planes[1] 且 host_ptr == nullptr
    assert(!broker.publish_frame(null_nv12));

    // 4. NV12 plane_strides[0] < width
    HardwareFrame short_nv12{};
    short_nv12.width = 64;
    short_nv12.height = 64;
    short_nv12.stride = 64;
    short_nv12.format = FRAME_FORMAT_NV12;
    short_nv12.host_planes[0] = buf.data();
    short_nv12.host_planes[1] = buf.data();
    short_nv12.plane_strides[0] = 32; // < width
    assert(!broker.publish_frame(short_nv12));

    // 客户端不得收到任何错误分发，且配额归零
    IpcHeader no_hdr{};
    std::vector<uint8_t> no_payload;
    assert(!client.recv_msg(no_hdr, no_payload, 150));

    ClientInfo info{};
    assert(broker.get_client_info(resp.client_id, info));
    assert(info.in_flight_count == 0);
    assert(info.delivered_frames == 0);
    assert(info.dropped_frames == 4);

    client.disconnect();
    broker.stop();
    std::cout << "[PASS] test_frame_broker_invalid_source_rejected" << std::endl;
}

void test_shm_pool_move_resets_source_and_broker_publish_stop_race() {
    // 1. ShmPool 移动后源对象元数据必须清零
    ShmPool src_pool;
    assert(src_pool.init("/zhulong_test_shm_move", ShmMode::CREATE_READ_WRITE, 2, 4096));
    ShmPool dst_pool(std::move(src_pool));
    assert(!src_pool.is_valid());
    assert(src_pool.slot_count() == 0);
    assert(src_pool.slot_size() == 0);
    assert(src_pool.total_size() == 0);
    assert(dst_pool.is_valid());
    assert(dst_pool.slot_count() == 2);
    dst_pool.close();

    // 2. 全幅零拷贝无效尺寸必须被拒绝，且并发 publish_frame + stop() 安全收口
    const uint64_t stream_id = 1903;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 2;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    UnixSocketClient client_zc;
    assert(client_zc.connect(broker.socket_path()));
    HandshakeReq req_zc{};
    req_zc.api_version = 1;
    std::strncpy(req_zc.client_name, "zc_guard_client", sizeof(req_zc.client_name) - 1);
    assert(client_zc.send_msg(MSG_HANDSHAKE_REQ, &req_zc, sizeof(req_zc)));
    IpcHeader resp_hdr_zc{};
    std::vector<uint8_t> resp_payload_zc;
    assert(client_zc.recv_msg(resp_hdr_zc, resp_payload_zc, 1000));
    for (int i = 0; i < 50 && broker.client_count() == 0; ++i) {
        std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
    assert(broker.client_count() == 1);

    HardwareFrame bad_zc{};
    bad_zc.dma_fd = 999;
    bad_zc.width = 0;
    bad_zc.height = 0;
    bad_zc.stride = 0;
    bad_zc.format = FRAME_FORMAT_NV12;
    assert(!broker.publish_frame(bad_zc));
    client_zc.disconnect();

    std::vector<uint8_t> buf(64 * 64 * 3, 1);
    HardwareFrame ok_frame{};
    ok_frame.width = 64;
    ok_frame.height = 64;
    ok_frame.stride = 64 * 3;
    ok_frame.format = FRAME_FORMAT_UNKNOWN;
    ok_frame.host_ptr = buf.data();

    std::atomic<bool> stop_flag{false};
    std::thread pub_thread([&]() {
        while (!stop_flag.load(std::memory_order_relaxed)) {
            broker.publish_frame(ok_frame);
        }
    });
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
    broker.stop();
    stop_flag.store(true, std::memory_order_relaxed);
    pub_thread.join();
    std::cout << "[PASS] test_shm_pool_move_resets_source_and_broker_publish_stop_race" << std::endl;
}

int main(int argc, char* argv[]) {
    // 子进程是「包形态」的桩算法可执行文件：自带算法类，argv 只有 broker 相关参数。
    const char* consumer_bin = (argc > 1) ? argv[1] : nullptr;

    test_shm_pool_basic();
    test_frame_broker_dispatch_and_leasing();
    test_frame_broker_backpressure_isolation();
    test_frame_broker_crash_reclaim();
    test_partial_packet_desync_defense();
    test_oversized_payload_rejection();
    test_multi_planar_yuv420p_copy();
    test_protocol_alignment_assertions();
    test_send_failure_rollback();
    test_multi_broker_asymmetric_isolation();
    test_broker_host_crash_then_restart();
    test_frame_broker_slot_capacity_reject();
    test_frame_broker_malformed_ack_keeps_quota();
    test_frame_broker_motion_dispatch();
    test_frame_broker_result_egress();
    test_frame_broker_control_broadcast();
    test_frame_broker_task_config_cache_replay();
    test_frame_broker_start_waits_for_resource_lock();
    test_frame_broker_evicted_ack_does_not_double_deduct();
    test_frame_broker_options_validation();
    test_frame_broker_handshake_non_null_terminated_name();
    test_frame_broker_nv12_vstride_normalization();
    test_frame_broker_windowed_publish();
    test_frame_broker_invalid_source_rejected();
    test_shm_pool_move_resets_source_and_broker_publish_stop_race();
    if (consumer_bin) {
        test_consumer_subprocess_e2e(consumer_bin);
        test_consumer_multi_broker_e2e(consumer_bin);
        test_consumer_broker_connect_failure_d6(consumer_bin);
        test_stall_watchdog_terminates_stuck_worker(consumer_bin);
        test_consumer_result_algorithm_id_e2e(consumer_bin);
    }
    std::cout << "All FrameBroker SHM and IPC integration tests passed successfully!" << std::endl;
    return 0;
}
