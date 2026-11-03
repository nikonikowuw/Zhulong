/**
 * 实时音频播放后端（AudioWorklet）。
 *
 * 为什么是一个 worklet 而不是每包一个 `AudioBufferSourceNode`：
 * 8 kHz G.711 每 20 ms 一包 = 50 个节点/秒，节点创建与排期抖动会直接变成咔哒声，
 * 而且主线程一旦被 React 渲染占住就整段断音。挪进音频线程后，投递只剩一次
 * `postMessage`，播放位置的推进与主线程彻底解耦。
 *
 * 为什么必须是 `.js` 而不是 `.ts`：它被 `?raw` 当**文本**读入、由 `Blob` 交给
 * `addModule()`，中间没有任何编译步骤（见 `research/worklet-loading.md` §3.1）。
 * 写 TS 会在运行时直接语法错误，且 `tsc -b` 也不会检查它。
 *
 * 本文件的职责只有三件：环形缓冲、线性重采样（inputRate → sampleRate）、欠载淡入。
 * 音量不在这里做 —— 主线程的 `GainNode` 更简单，也能在无音频时独立生效。
 *
 * ⚠️ 不要在这里写 `console`：worklet 的 console 输出在部分浏览器不进主面板，
 * 排查时只会白等。
 */

/**
 * 注册名。必须与 `live-audio-player.ts` 的 `AUDIO_WORKLET_PROCESSOR_NAME` 逐字一致，
 * 否则 `new AudioWorkletNode()` 会以 `InvalidStateError` 失败。
 */
const PROCESSOR_NAME = 'zhulong-live-audio'

/** 环形缓冲容量（按输出采样率折合的秒数）。 */
const RING_SECONDS = 2

/** 欠载恢复时的淡入时长（秒）。硬切会留下可闻的咔哒声。 */
const FADE_IN_SECONDS = 0.01

class LiveAudioProcessor extends AudioWorkletProcessor {
  constructor() {
    super()

    this.capacity = Math.max(128, Math.ceil(sampleRate * RING_SECONDS))
    this.buffer = new Float32Array(this.capacity)

    /** 累计写入 / 读取的**源**样本数；取模后才是缓冲下标。 */
    this.writeTotal = 0
    this.readTotal = 0
    this.inputRate = sampleRate

    /** 是否已有一条正在推进的时间轴。false 时下一包的 startDueFrame 才有效。 */
    this.streaming = false

    /** 尚未吐出的静音帧数（重锚时的预滚，单位是输出帧）。 */
    this.pendingSilence = 0

    this.fadeLength = Math.max(1, Math.round(sampleRate * FADE_IN_SECONDS))
    this.fadeRemaining = 0
    this.silent = true

    this.port.onmessage = (event) => {
      this.handleMessage(event.data)
    }
  }

  handleMessage(message) {
    if (!message || typeof message.type !== 'string') return
    if (message.type === 'push') {
      this.push(message)
    } else if (message.type === 'flush') {
      this.flush()
    }
  }

  /** 清空缓冲并重置读写指针。主线程的 flush / 重锚都落到这里。 */
  flush() {
    this.buffer.fill(0)
    this.writeTotal = 0
    this.readTotal = 0
    this.streaming = false
    this.pendingSilence = 0
    this.fadeRemaining = 0
    this.silent = true
  }

  push(message) {
    const samples = message.samples
    if (!samples || samples.length === 0) return

    const rate =
      typeof message.inputRate === 'number' && message.inputRate > 0
        ? message.inputRate
        : sampleRate
    const startDueFrame =
      typeof message.startDueFrame === 'number' ? message.startDueFrame : 0

    // 采样率变化只可能来自切码流（G.711 8k ↔ AAC 48k）：读指针上的半样本没有意义，
    // 直接按重锚处理。同码流内的每包采样率恒等，不走这条分支。
    if (rate !== this.inputRate) {
      this.flush()
      this.inputRate = rate
    }

    // 没有正在推进的时间轴（首包 / 刚 flush / 欠载后重新接上）：按 startDueFrame
    // 对齐到输出时间轴，早于当前帧（主线程算晚了）就立刻起播。
    if (!this.streaming) {
      const delta = startDueFrame - currentFrame
      this.pendingSilence = delta > 0 ? delta : 0
      this.streaming = true
      this.silent = true
      this.fadeRemaining = 0
    }

    // 溢出：丢**最旧**的样本。音频的价值在实时边缘，宁可牺牲开头也不让延迟无界增长。
    const overflow =
      this.writeTotal - this.readTotal + samples.length - this.capacity
    if (overflow > 0) {
      this.readTotal += overflow
      if (this.readTotal > this.writeTotal) this.readTotal = this.writeTotal
    }

    const base = this.writeTotal
    for (let i = 0; i < samples.length; i++) {
      this.buffer[(base + i) % this.capacity] = samples[i]
    }
    this.writeTotal = base + samples.length
  }

  /** 尚未被读指针消费的源样本数。 */
  available() {
    return this.writeTotal - Math.floor(this.readTotal)
  }

  process(_inputs, outputs) {
    const output = outputs[0]
    if (!output || output.length === 0) return true

    const channelCount = output.length
    const frames = output[0].length
    const ratio = this.inputRate / sampleRate

    for (let i = 0; i < frames; i++) {
      let sample = 0
      let produced = false

      if (this.pendingSilence > 0) {
        // 预滚静音：不读缓冲、不推进读指针（欠载时同样「不跳读」）。
        this.pendingSilence--
      } else if (this.available() >= 2) {
        // 线性插值需要当前样本与**下一个**样本；只剩一个时宁可等下一包，
        // 也不要用零填充出来的假邻居（会在每包边界留一个台阶）。
        const index = Math.floor(this.readTotal)
        const fraction = this.readTotal - index
        const current = this.buffer[index % this.capacity]
        const next = this.buffer[(index + 1) % this.capacity]
        sample = current + (next - current) * fraction
        this.readTotal += ratio
        produced = true
      }

      if (produced) {
        if (this.silent) {
          this.silent = false
          this.fadeRemaining = this.fadeLength
        }
        if (this.fadeRemaining > 0) {
          sample *= 1 - this.fadeRemaining / this.fadeLength
          this.fadeRemaining--
        }
      } else {
        // 欠载 / 预滚：输出静音，并把状态复位成「下一次出声要淡入」。
        // 时间轴同时作废：下一包的 startDueFrame 重新对齐，而不是接着一条
        // 已经追不上墙钟的旧时间轴继续算。
        sample = 0
        this.silent = true
        this.fadeRemaining = 0
        if (this.pendingSilence === 0) this.streaming = false
      }

      // 单声道源复制到所有声道（播放器固定申请双声道输出）。
      for (let channel = 0; channel < channelCount; channel++) {
        output[channel][i] = sample
      }
    }

    // 常驻：只要播放器没 close()，处理器就必须继续被调度。
    return true
  }
}

registerProcessor(PROCESSOR_NAME, LiveAudioProcessor)
