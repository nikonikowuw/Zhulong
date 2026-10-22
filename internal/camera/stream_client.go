package camera

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// 默认 WebSocket 队列容量（帧）。GOP 缓存使用相同帧数上限，保证 complete GOP 可补发。
	defaultClientBufferSize = 128

	// wsWriteWait 写入超时时间。
	wsWriteWait = 5 * time.Second

	// wsPongWait 接收对端 Pong 的最大超时。
	//
	// 边缘现场的价值是「快速发现死连接」而不是「尽量不误断」。
	// 60s 的容忍度意味着一次断网后旧连接会占着资源 —— 期间所有帧都在往黑洞里写。
	wsPongWait = 20 * time.Second

	// wsPingPeriod 心跳 Ping 发送间隔（必须显著小于 wsPongWait）。
	//
	// 不按 9/10 派生：视频帧本身就是天然活性信号，Ping 只是兜底；
	// 固定 5s 能让「对端已死」在 20s 内被发现，而不是拖到 54s。
	wsPingPeriod = 5 * time.Second

	// wsMaxMessageSize 客户端上行控制消息最大字节数。
	wsMaxMessageSize = 512
)

// outFrame 携带 PTS 供解码器呈现、DTS 供服务端测量写出进度，并携带帧分类供安全重同步。
type outFrame struct {
	data  []byte
	ptsUS int64
	dtsUS int64
	frame byte // WireFrame* 帧分类
	gopID uint32
}

// StreamClient 封装单个 WebSocket 客户端长连接会话。
type StreamClient struct {
	conn       *websocket.Conn
	sendChan   chan outFrame
	dispatcher *StreamDispatcher
	logger     *zap.Logger

	// 只有 WebSocket 写成功才推进基准；首帧先以最早排队的 DTS 初始化，确保写阻塞时预算仍会增长。
	needsKey          bool
	lastProgressDTSUS int64
	hasProgressDTS    bool

	done      chan struct{}
	closeOnce sync.Once
}

// NewStreamClient 构造单个客户端。
func NewStreamClient(conn *websocket.Conn, dispatcher *StreamDispatcher, logger *zap.Logger) *StreamClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &StreamClient{
		conn:       conn,
		sendChan:   make(chan outFrame, defaultClientBufferSize),
		dispatcher: dispatcher,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

// Close 关闭客户端发送管道与连接。
func (c *StreamClient) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close()
		}
		if c.dispatcher != nil {
			c.dispatcher.UnregisterClient(c)
		}
	})
}

// CloseWithReason 携带状态码主动关闭连接。
func (c *StreamClient) CloseWithReason(statusCode int, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			deadline := time.Now().Add(time.Second)
			_ = c.conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(statusCode, reason),
				deadline,
			)
			_ = c.conn.Close()
		}
		if c.dispatcher != nil {
			c.dispatcher.UnregisterClient(c)
		}
	})
}

// WritePump 运行写事件循环（将视频二进制帧推送到 WebSocket）。
func (c *StreamClient) WritePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case f, ok := <-c.sendChan:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, f.data); err != nil {
				c.logger.Debug("ws write binary failed", zap.Error(err))
				return
			}
			if c.dispatcher != nil {
				c.dispatcher.markFrameWritten(c, f)
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// ReadPump 运行读事件循环（监听客户端 Ping/Pong 与断开事件）。
func (c *StreamClient) ReadPump() {
	defer func() {
		c.Close()
	}()

	c.conn.SetReadLimit(wsMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.logger.Debug("ws read error", zap.Error(err))
			}
			break
		}
	}
}
