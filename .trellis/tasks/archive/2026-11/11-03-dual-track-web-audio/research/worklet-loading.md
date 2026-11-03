# 研究：AudioWorklet 在 Vite 8 + 真实无头 Chromium 下的可加载形态

> 起因：Phase C 第一次实现尝试在 `AudioWorklet` 的资源加载上反复探测，30 分钟预算耗尽。
> 本文把**实测结论**固化下来，后续实现直接照抄，不要重新试探。
> 环境：Vite 8.0.8 + `@vitest/browser-playwright` 4.1.4 + Chromium（headless，Playwright 1.59.1）。

---

## 1. 结论速览

| 方案 | 生产构建 | `addModule()` | 判定 |
| :--- | :--- | :--- | :--- |
| `new URL('./x-worklet.ts', import.meta.url)` | 把 **.ts 文件原样复制**到 `dist/assets/`（名字带哈希，后缀仍是 `.ts`） | ❌ `AbortError: Unable to load a worklet's module.` | **不可用** |
| `?worker&inline`（Vite 内联 worker） | 产出 `data:video/mp2t;base64,...` | ❌ 同 `AbortError`（MIME 不对，且内联的是 IIFE 包裹体） | **不可用** |
| 显式 `?url` 指向 `.js` 源文件 | 复制 `.js` 到 assets，MIME 由 Go 静态服务按扩展名给出 | ⚠️ 理论可用，但要求 worklet 源是 `.js` 且不经 TS 编译，多一份重复类型定义 | 不选 |
| **`?raw` 导入源文本 + `Blob` + `createObjectURL`** | 源字符串**内联进主 bundle**，无需额外资源请求 | ✅ 通过 | **采用** |

## 2. 实测证据（均已在真实无头 Chromium 跑通）

```
✓ new AudioContext() 同步可用，state === 'suspended'
✓ ctx.audioWorklet.addModule(blobUrl) 在 suspended 状态下即可成功（无需用户手势）
✓ new AudioWorkletNode(ctx, name) 可构造
✓ node.port 双向 postMessage 往返正常（worklet 内 onmessage → postMessage）
✓ /raw 导入的独立 worklet 源 → Blob → addModule → 往返，全链路通过
✓ AudioDecoder 存在，isConfigSupported({codec:'mp4a.40.2', ...}) 可用
```

### 2.1 一个必须知道的行为：无手势时 `resume()` 的 Promise **永不落地**

无用户手势时：

- `AudioContext.state === 'suspended'`（构造同步成功）；
- `ctx.resume()` 返回的 Promise **既不 resolve 也不 reject**，`await` 它会永久挂起；
- 测试里如果 `await ctx.resume()` 会直接被 `testTimeout`（15 s）杀掉。

⇒ 播放器**不得** `await resume()`。正确写法：调用后立即返回，用 `ctx.state` / `onstatechange` 观察；把「真正出声」交给环形缓冲首次被读取。

## 3. 采纳方案的具体形状

### 3.1 worklet 源文件

`web/src/features/live/services/live-audio-worklet.js`

- **必须是 `.js`**（不是 `.ts`）：它被 `?raw` 当作文本读入，再由 `Blob` 以 `text/javascript` 交给 `addModule`，中间没有任何编译步骤。写 TS 会在运行时直接语法错误。
- 不参与 `tsc -b`（`.js` 不在 `include: ["src"]` 的 TS 编译范围内，除非 allowJs）。因此也**不要**给它写 `.d.ts`，用 `?raw` 的既有类型声明即可。
- 需要 `AudioWorkletProcessor` / `registerProcessor` / `sampleRate` / `currentFrame` 这些全局——在 `.js` 里天然不需要类型声明。
- `?raw` 的类型由 `vite/client` 自带：`declare module '*?raw' { const src: string; export default src }`（`node_modules/vite/client.d.ts:246`），`src/vite-env.d.ts` 已 triple-slash 引用。

### 3.2 装配顺序

```ts
import workletSource from './live-audio-worklet.js?raw'

const url = URL.createObjectURL(
  new Blob([workletSource], { type: 'text/javascript' })
)
try {
  await ctx.audioWorklet.addModule(url)
} finally {
  URL.revokeObjectURL(url) // addModule 已在解析阶段读取完，可以立刻回收
}
const node = new AudioWorkletNode(ctx, PROCESSOR_NAME, { outputChannelCount: [2] })
```

- `addModule` 是异步的 ⇒ 首帧投递必须等它就绪（用一个 `ready` Promise 或 `pendingPush` 队列）。
- `createObjectURL` 的 URL 在 `addModule` resolve 后可立刻回收：模块源码已进 worklet 全局作用域。

### 3.3 ESLint 注意

`web/eslint.config.js` 里 `no-console: 'error'`。worklet 源是 `.js` 且 `files: ['**/*.{ts,tsx}']` 只匹配 TS，因此 worklet 内的日志不受该规则约束——但仍建议不写 `console`（worklet 的 `console` 输出在部分浏览器不进 DevTools 主面板）。

## 4. 与既有任务制品的偏差

`11-03-dual-track-web-audio/design.md` 写的是：

> Worklet 用 `new URL('./xxx-worklet.ts', import.meta.url)`；`addModule()` 异步，首帧投递等它就绪。

**该写法在 Vite 8 下不可用**（见 §1）。以本文为准，`design.md` 已同步更正。

## 5. 复现实验（如需重跑）

在 `web/` 下建一个临时 `*.test.ts`：

```ts
import workletSource from './live-audio-worklet.js?raw'
// ctx.audioWorklet.addModule(URL.createObjectURL(new Blob([workletSource], {type:'text/javascript'})))
```

然后 `./node_modules/.bin/vitest run --browser.headless <file>`。
注意：不要 `await ctx.resume()`（见 §2.1）；跑完删除临时文件。
