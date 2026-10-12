#include "../src/algorithm/ipc/ipc_protocol.hpp"
#include "../src/algorithm/ipc/shm_pool.hpp"
#include "../src/algorithm/ipc/unix_socket.hpp"
#include "../src/algorithm/ipc/frame_broker.hpp"
#include <Zhulong/frame.hpp>
#include <cassert>
#include <iostream>
#include <vector>
#include <thread>
#include <chrono>
#include <cstring>
#include <atomic>

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
// Test 5: 真实独立子进程 Runner 跨进程接收与推理全链路验证
// =============================================================================
void test_runner_subprocess_e2e(const char* runner_bin, const char* plugin_so) {
    if (!runner_bin || !plugin_so) return;

    uint64_t stream_id = 1004;
    FrameBroker broker;
    BrokerOptions opt;
    opt.slot_count = 4;
    opt.slot_size = 1024 * 1024;
    assert(broker.start(stream_id, "/tmp", opt));

    // 启动独立子进程 Runner
    pid_t pid = fork();
    if (pid == 0) {
        // 子进程
        execl(runner_bin, runner_bin,
              "--plugin", plugin_so,
              "--broker", broker.socket_path().c_str(),
              "--name", "sub_worker",
              "--max-frames", "3",
              nullptr);
        _exit(127);
    }

    assert(pid > 0);

    // 等待子进程连接
    for (int i = 0; i < 50; ++i) {
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
    std::cout << "[PASS] test_runner_subprocess_e2e" << std::endl;
}

int main(int argc, char* argv[]) {
    const char* runner_bin = (argc > 1) ? argv[1] : nullptr;
    const char* plugin_so = (argc > 2) ? argv[2] : nullptr;

    test_shm_pool_basic();
    test_frame_broker_dispatch_and_leasing();
    test_frame_broker_backpressure_isolation();
    test_frame_broker_crash_reclaim();
    if (runner_bin && plugin_so) {
        test_runner_subprocess_e2e(runner_bin, plugin_so);
    }
    std::cout << "All FrameBroker SHM and IPC integration tests passed successfully!" << std::endl;
    return 0;
}
