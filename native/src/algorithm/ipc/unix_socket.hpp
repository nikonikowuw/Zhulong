/**
 * @file unix_socket.hpp
 * @brief 基于 Unix Domain Socket 的点对点定长二进制 IPC 服务端与客户端通信通道
 *
 * 核心设计原则：
 * 1. 结构化二进制报文：每次传输均由 16 字节 IpcHeader 前导，后接变长 Payload（<= 64KB）。
 * 2. 线程安全原子写：各客户端通道拥有独立的 send_mutex，保证并发调用 send_msg 时整条报文完整写入，不发生字节交叉。
 * 3. 四态读取与半包熔断：底层 read_all 实现精确的半包流脱节（DESYNC_ERROR）探测，一旦出现错位立即断开并触发清理。
 */

#pragma once

#include "ipc_protocol.hpp"
#include <string>
#include <vector>
#include <functional>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <atomic>
#include <thread>

namespace zhulong::ipc {

/**
 * @brief 单个已连接客户端的通道控制块
 *
 * 包含该客户端的 socket 文件描述符及写操作互斥锁，支持多线程安全发送。
 */
struct ClientChannel {
    std::atomic<int> fd{-1};     /**< 客户端已连接的 socket 文件描述符（-1 表示已断开） */
    std::mutex send_mutex;       /**< 写操作互斥锁，保证整条 IPC 报文（Header + Payload）连续原子发送 */
};

/**
 * @brief 基于 Unix Domain Socket 的 IPC 服务端（FrameBroker 宿主角色）
 *
 * 监听指定文件路径，接收来自各算法 Runner 子进程的连接请求；
 * 采用多线程模式：一个 accept 线程负责握手建连，每个连接拥有独立的读取循环线程。
 */
class UnixSocketServer {
public:
    using MessageCallback = std::function<void(uint32_t client_id, const IpcHeader& header, const uint8_t* payload)>;
    using ConnectCallback = std::function<void(uint32_t client_id)>;
    using DisconnectCallback = std::function<void(uint32_t client_id)>;

    UnixSocketServer();
    ~UnixSocketServer();

    /**
     * @brief 启动 Unix 域套接字监听服务端
     *
     * @param socket_path 监听的套接字文件绝对路径（如 /tmp/zhulong_broker_1.sock）
     * @param[out] error_msg 失败时返回详细错误原因（可选）
     * @return true 启动成功；false 失败
     */
    bool start(const std::string& socket_path, std::string* error_msg = nullptr);

    /**
     * @brief 优雅停机并释放所有客户端连接与套接字文件
     */
    void stop();

    /**
     * @brief 注册收到客户端消息时的回调函数
     */
    void set_on_message(MessageCallback cb) { on_message_ = std::move(cb); }

    /**
     * @brief 注册新客户端建连时的回调函数
     */
    void set_on_connect(ConnectCallback cb) { on_connect_ = std::move(cb); }

    /**
     * @brief 注册客户端断开连接时的回调函数
     */
    void set_on_disconnect(DisconnectCallback cb) { on_disconnect_ = std::move(cb); }

    /**
     * @brief 向指定客户端发送类型化 IPC 报文（可选携带文件描述符通过 SCM_RIGHTS 传输）
     *
     * @param client_id 目标客户端唯一 ID
     * @param msg_type 消息类型枚举（MessageType）
     * @param payload 负载数据指针（可为空）
     * @param payload_size 负载字节数（<= MAX_IPC_PAYLOAD_SIZE）
     * @param fd_to_send 需附带传递的文件描述符（<0 表示不附带）
     * @return true 发送成功；false 发送失败或对端已断开
     */
    bool send_msg_with_fd(uint32_t client_id, uint16_t msg_type, const void* payload, size_t payload_size, int fd_to_send);

    /**
     * @brief 向指定客户端发送类型化 IPC 报文
     *
     * @param client_id 目标客户端唯一 ID
     * @param msg_type 消息类型枚举（MessageType）
     * @param payload 负载数据指针（可为空）
     * @param payload_size 负载字节数（<= MAX_IPC_PAYLOAD_SIZE）
     * @return true 发送成功；false 发送失败或对端已断开
     */
    bool send_msg(uint32_t client_id, uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0) {
        return send_msg_with_fd(client_id, msg_type, payload, payload_size, -1);
    }

    /**
     * @brief 向所有当前在线的客户端广播同一种 IPC 消息
     *
     * @return size_t 成功分发接收的客户端总数
     */
    size_t broadcast(uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0);

    /**
     * @brief 获取当前在线的客户端总数
     */
    [[nodiscard]] size_t client_count() const;

    /**
     * @brief 检查服务端是否处于运行状态
     */
    [[nodiscard]] bool is_running() const noexcept { return running_.load(); }

    /**
     * @brief 获取监听的套接字文件路径
     */
    [[nodiscard]] const std::string& path() const noexcept { return socket_path_; }

private:
    void listen_loop();
    void client_read_loop(uint32_t client_id, std::shared_ptr<ClientChannel> channel);
    void remove_client(uint32_t client_id);

    std::string socket_path_;                                                     /**< 套接字文件绑定路径 */
    std::atomic<int> listen_fd_{-1};                                              /**< 监听套接字描述符 */
    std::atomic<bool> running_{false};                                            /**< 服务端运行状态标志 */
    std::atomic<uint32_t> next_client_id_{1};                                     /**< 客户端自增 ID 分配器 */
    std::atomic<size_t> active_workers_{0};                                       /**< 活跃的工作读取线程计数 */

    mutable std::mutex clients_mutex_;                                            /**< 客户端集合互斥锁 */
    std::unordered_map<uint32_t, std::shared_ptr<ClientChannel>> client_channels_; /**< 客户端 ID 到通道实体的映射 */
    std::thread accept_thread_;                                                   /**< 监听接收连接的独立线程 */

    MessageCallback on_message_;                                                  /**< 消息接收回调 */
    ConnectCallback on_connect_;                                                  /**< 连接建立回调 */
    DisconnectCallback on_disconnect_;                                            /**< 连接断开回调 */
};

/**
 * @brief 基于 Unix Domain Socket 的 IPC 客户端（算法 Runner 角色）
 *
 * 负责主动连接宿主 FrameBroker，执行握手并接收帧分发通知与发送确认。
 */
class UnixSocketClient {
public:
    UnixSocketClient();
    ~UnixSocketClient();

    /**
     * @brief 连接到指定的宿主套接字
     *
     * @param socket_path 目标套接字路径（如 /tmp/zhulong_broker_1.sock）
     * @param[out] error_msg 失败时输出错误信息（可选）
     * @return true 连接成功；false 失败
     */
    bool connect(const std::string& socket_path, std::string* error_msg = nullptr);

    /**
     * @brief 主动断开与服务端的连接
     */
    void disconnect();

    /**
     * @brief 向服务端发送单条类型化 IPC 消息（可选携带文件描述符通过 SCM_RIGHTS 传输）
     */
    bool send_msg_with_fd(uint16_t msg_type, const void* payload, size_t payload_size, int fd_to_send);

    /**
     * @brief 向服务端发送单条类型化 IPC 消息
     */
    bool send_msg(uint16_t msg_type, const void* payload = nullptr, size_t payload_size = 0) {
        return send_msg_with_fd(msg_type, payload, payload_size, -1);
    }

    /**
     * @brief 同步/超时接收一条完整的 IPC 消息并提取可选的 SCM_RIGHTS 文件描述符
     *
     * @param[out] out_header 解析出的 16 字节报文头部
     * @param[out] out_payload 自动扩容并装载的负载数据
     * @param[out] out_fd 接收到的文件描述符（若对端未携带则为 -1；若 >= 0 调用方负责关闭）
     * @param timeout_ms 超时毫秒数（<0 表示无限等待，0 表示非阻塞，>0 表示带超时）
     * @return true 成功收到完整报文；false 超时、半包熔断或连接已断开
     */
    bool recv_msg_with_fd(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int& out_fd, int timeout_ms = -1);

    /**
     * @brief 同步/超时接收一条完整的 IPC 消息（忽略或关闭任何附带的 fd）
     *
     * @param[out] out_header 解析出的 16 字节报文头部
     * @param[out] out_payload 自动扩容并装载的负载数据
     * @param timeout_ms 超时毫秒数（<0 表示无限等待，0 表示非阻塞，>0 表示带超时）
     * @return true 成功收到完整报文；false 超时、半包熔断或连接已断开
     */
    bool recv_msg(IpcHeader& out_header, std::vector<uint8_t>& out_payload, int timeout_ms = -1);

    /**
     * @brief 检查客户端是否处于已连接状态
     */
    [[nodiscard]] bool is_connected() const noexcept { return fd_.load() >= 0; }

private:
    std::atomic<int> fd_{-1};      /**< 套接字文件描述符 */
    std::string socket_path_;      /**< 连接的服务端路径 */
    std::mutex send_mutex_;        /**< 发送锁，保证客户端并发写安全 */
    std::mutex recv_mutex_;        /**< 接收锁，保证整包接收期间互斥 */
};

} // namespace zhulong::ipc

