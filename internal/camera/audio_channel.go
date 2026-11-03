package camera

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

const (
	// AudioChannelMaxCount 是同一条宿主上同时存在的音频通道数硬上限。
	//
	// 它是防滥用常量而不是配置项：正常使用下「每客户端一条」已经把上限压到
	// 同时在线的浏览器数，8 条足够覆盖桌面多标签与小团队；再高的数字只会让
	// 一个脚本就能把 16 路相机的音频全部拉满。
	AudioChannelMaxCount = 8

	// audioChannelQueueFrames 是单通道写队列容量（48 帧 ≈ 0.96s）。
	//
	// 音频不像视频可以「等下一个 IDR」：丢一帧就是可闻的咔哒声。但队列也不能
	// 无界——客户端不再读取时必须让它自己死掉，而不是把内存吃掉。
	audioChannelQueueFrames = 48

	// audioChannelWriteWait 是单次写超时。
	audioChannelWriteWait = 5 * time.Second
)

// 音频通道的 WebSocket 关闭码。
//
// 4000 段属于应用自定义区间，前端据此区分「被顶替」与「被限流」，
// 而不是把所有断开都当成网络问题去重试。
const (
	// AudioCloseSuperseded 同一登录会话开了第二条音频通道，旧连接被顶替。
	AudioCloseSuperseded = 4001
	// AudioCloseTooManyChannels 全局通道数超硬上限。
	AudioCloseTooManyChannels = 4002
	// AudioCloseBackpressure 客户端消费过慢，队列溢出。
	AudioCloseBackpressure = 4003
	// AudioCloseNoAudio 该路流没有可播放的音轨。
	AudioCloseNoAudio = 4404
	// AudioCloseInternal 订阅失败或内部错误。
	AudioCloseInternal = 4500
)

// ErrAudioChannelLimit 表示音频通道数已达硬上限。
var ErrAudioChannelLimit = errors.New("audio channel limit reached")

// AudioChannelRegistry 管理「每登录会话最多一条」的音频通道。
//
// 键是登录会话（session token 的哈希前缀），不是用户 ID、不是全局：
// 全局一条会让多人时只有一个人能听，按账号一条又在单账号系统里退化为全局一条。
type AudioChannelRegistry struct {
	logger *zap.Logger
	// bySession 的键是 sha256(token) 前 16 位十六进制，永不含 token 原文。
	bySession map[string]*AudioChannel
	max       int
	// 计数仅用于可运维性（谁在听、被顶替了几次、拒绝了几次），不参与决策。
	superseded atomic.Uint64
	rejected   atomic.Uint64
	opened     atomic.Uint64
	mu         sync.Mutex
}

// NewAudioChannelRegistry 构造注册表。
func NewAudioChannelRegistry(logger *zap.Logger) *AudioChannelRegistry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AudioChannelRegistry{
		bySession: make(map[string]*AudioChannel),
		max:       AudioChannelMaxCount,
		logger:    logger.Named("camera.audio"),
	}
}

// sessionChannelKey 把登录会话令牌折叠成可落日志的键。
//
// 只保留哈希前缀：日志里出现 token 原文等于把凭据写进磁盘。
func sessionChannelKey(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// Open 注册一条新通道，返回被它顶替的旧通道（若有）。
//
// 调用方必须先订阅成功再注册：反过来的话，订阅失败会把一条健康的旧通道顶掉，
// 用户听到的是「点一下就彻底没声音了」。
func (r *AudioChannelRegistry) Open(key string, channel *AudioChannel) (*AudioChannel, error) {
	r.mu.Lock()
	previous, existed := r.bySession[key]
	if !existed && len(r.bySession) >= r.max {
		r.mu.Unlock()
		r.rejected.Add(1)
		r.logger.Warn("audio channel rejected: limit reached",
			zap.Int("channels", len(r.bySession)),
			zap.Int("limit", r.max),
		)
		return nil, ErrAudioChannelLimit
	}
	r.bySession[key] = channel
	r.mu.Unlock()

	r.opened.Add(1)
	if existed {
		r.superseded.Add(1)
	}
	return previous, nil
}

// Remove 仅当注册表里记录的仍是该实例时才移除，避免「新通道刚注册、旧通道的
// 延迟关闭把新表项删掉」这种顶替时序造成的幽灵条目。
func (r *AudioChannelRegistry) Remove(key string, channel *AudioChannel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.bySession[key]; ok && current == channel {
		delete(r.bySession, key)
	}
}

// CloseAll 关闭全部音频通道（服务停机时用）。
//
// 先快照再逐个关闭：Close 会回头调用 Remove 改表，直接在持锁遍历里关会自锁。
func (r *AudioChannelRegistry) CloseAll() {
	r.mu.Lock()
	channels := make([]*AudioChannel, 0, len(r.bySession))
	for _, channel := range r.bySession {
		channels = append(channels, channel)
	}
	r.mu.Unlock()

	for _, channel := range channels {
		channel.Close()
	}
}

// Len 返回当前音频通道数。
func (r *AudioChannelRegistry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bySession)
}

// Stats 返回按需通道的累计计数快照（仅用于日志与运维观测）。
func (r *AudioChannelRegistry) Stats() (opened, superseded, rejected uint64) {
	return r.opened.Load(), r.superseded.Load(), r.rejected.Load()
}

// AudioChannel 是一条按需音频通道：一条 WebSocket、一条音频订阅、一个有界发送队列。
type AudioChannel struct {
	registry *AudioChannelRegistry
	logger   *zap.Logger
	conn     *websocket.Conn
	stream   MediaStream
	sub      PacketSource
	send     chan []byte
	done     chan struct{}
	closeOne sync.Once
	key      string
	cameraID string
	role     string
	seq      atomic.Uint32
	// dropped 统计因写队列满而丢弃的帧数（仅在关闭前被读取，用于日志）。
	dropped atomic.Uint64
	closed  atomic.Bool
}

// NewAudioChannel 构造一条音频通道。订阅与流引用由调用方注入所有权：
// 通道关闭时会先同步排空订阅，再释放流。
func NewAudioChannel(
	registry *AudioChannelRegistry,
	conn *websocket.Conn,
	stream MediaStream,
	sub PacketSource,
	key, cameraID, role string,
	logger *zap.Logger,
) *AudioChannel {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &AudioChannel{
		registry: registry,
		logger:   logger,
		conn:     conn,
		stream:   stream,
		sub:      sub,
		key:      key,
		cameraID: cameraID,
		role:     role,
		send:     make(chan []byte, audioChannelQueueFrames),
		done:     make(chan struct{}),
	}
}

// SendInfo 在二进制帧之前同步写出文本帧。
//
// 此刻写协程还没启动，连接只有一个写者，因此不需要任何锁。
func (c *AudioChannel) SendInfo(message any) error {
	if c.conn == nil {
		return errors.New("audio channel has no connection")
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(audioChannelWriteWait))
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

// Pump 把音频订阅泵进发送队列。
//
// 它绝不阻塞原生回调：订阅本身是有界队列（48 包），这里再满就直接关通道，
// 因为「让服务端等一个不读数据的浏览器」没有任何正确解。
func (c *AudioChannel) Pump(ctx context.Context) {
	for {
		pkt, err := c.sub.Next(ctx)
		if err != nil {
			c.closeWithReason(audioCloseCodeFor(err), audioCloseReason(err))
			return
		}
		frame := PackAudioPacket(pkt, c.seq.Add(1)-1)
		select {
		case c.send <- frame:
		case <-c.done:
			return
		default:
			c.dropped.Add(1)
			c.closeWithReason(AudioCloseBackpressure, "backpressure")
			return
		}
	}
}

// WritePump 把队列里的音频帧写到连接上。
func (c *AudioChannel) WritePump() {
	ticker := time.NewTicker(wsPingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case frame, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(audioChannelWriteWait))
			if err := c.conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				c.logger.Debug("audio ws write failed", zap.Error(err))
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(audioChannelWriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// ReadPump 监听客户端 Ping/Pong 与断开事件。
func (c *AudioChannel) ReadPump() {
	defer c.Close()

	c.conn.SetReadLimit(wsMaxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.logger.Debug("audio ws read failed", zap.Error(err))
			}
			return
		}
	}
}

// Close 幂等关闭通道：连接 → 订阅（同步排空）→ 流引用 → 注册表。
//
// 顺序是硬约束：注册表必须在 unsubscribe 返回之后才移除，否则会出现
// 「注册表说没有音频通道、但原生回调还在跑」的窗口（新通道会与旧回调并存）。
func (c *AudioChannel) Close() {
	c.closeWithReason(websocket.CloseNormalClosure, "")
}

// Supersede 由接管方调用：关闭旧连接并告知前端原因。
func (c *AudioChannel) Supersede() {
	c.closeWithReason(AudioCloseSuperseded, "superseded")
}

func (c *AudioChannel) closeWithReason(code int, reason string) {
	c.closeOne.Do(func() {
		c.closed.Store(true)
		close(c.done)
		if c.conn != nil {
			if code != websocket.CloseNormalClosure && code != 0 {
				_ = c.conn.WriteControl(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(code, reason),
					time.Now().Add(time.Second),
				)
			}
			_ = c.conn.Close()
		}
		if c.sub != nil {
			_ = c.sub.Close()
		}
		if c.stream != nil {
			_ = c.stream.Close()
		}
		if c.registry != nil {
			c.registry.Remove(c.key, c)
		}
		c.logger.Info("audio channel closed",
			zap.String("session", c.key),
			zap.String("camera_id", c.cameraID),
			zap.String("role", c.role),
			zap.Int("code", code),
			zap.String("reason", reason),
			zap.Uint32("seq", c.seq.Load()),
			zap.Uint64("dropped", c.dropped.Load()),
		)
	})
}

// audioCloseCodeFor 把订阅终态映射成 WebSocket 关闭码。
//
// 背压单独成码：前端需要知道「是我太慢」而不是「服务端坏了」，
// 否则它会立刻重连，然后再次被背压踢掉，变成肉眼可见的抖动循环。
func audioCloseCodeFor(err error) int {
	if errors.Is(err, engine.ErrBackpressure) {
		return AudioCloseBackpressure
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, engine.ErrClosed) {
		return websocket.CloseNormalClosure
	}
	return AudioCloseInternal
}

func audioCloseReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, engine.ErrBackpressure) {
		return "backpressure"
	}
	return "audio stream ended"
}
