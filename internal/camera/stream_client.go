package camera

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// defaultClientBufferSize 单个 WebSocket 客户端发送缓冲队列容量（64 包，约 2.5s @ 25fps）。
	defaultClientBufferSize = 64

	// wsWriteWait 写入超时时间。
	wsWriteWait = 5 * time.Second

	// wsPongWait 接收对端 Pong 的最大超时。
	wsPongWait = 60 * time.Second

	// wsPingPeriod 心跳 Ping 发送间隔（必须小于 wsPongWait）。
	wsPingPeriod = (wsPongWait * 9) / 10

	// wsMaxMessageSize 客户端上行控制消息最大字节数。
	wsMaxMessageSize = 512
)

// StreamClient 封装单个 WebSocket 客户端长连接会话。
type StreamClient struct {
	conn       *websocket.Conn
	sendChan   chan []byte
	dispatcher *StreamDispatcher
	logger     *zap.Logger

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
		sendChan:   make(chan []byte, defaultClientBufferSize),
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
		case msg, ok := <-c.sendChan:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				c.logger.Debug("ws write binary failed", zap.Error(err))
				return
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
