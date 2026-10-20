/**
 * 跨浏览器安全复制文本到剪贴板，带回退方案。
 *
 * 为什么必须走这个函数而不是直接 `navigator.clipboard.writeText`：
 * `navigator.clipboard` **只在安全上下文**（HTTPS 或 localhost）中存在。
 * 边缘设备把服务绑到 `0.0.0.0` 后通过 `http://192.168.x.x:8080` 访问时它是
 * `undefined`，裸调 `navigator.clipboard.writeText(...)` 会在同步求值阶段抛
 * TypeError。本函数先探测可用性，再降级到 `execCommand('copy')`。
 *
 * @param text 待复制文本；空串直接返回 `false`，不做任何 DOM 操作。
 * @returns 是否复制成功。调用方应据此给出成功/失败反馈，不要假定成功。
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false

  if (navigator?.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 权限被拒或非聚焦文档：降级回退
    }
  }

  // 回退分支必须保证临时节点被移除：execCommand 可能抛错，且此时不能把
  // textarea 留在 DOM 里（它会吃掉焦点并干扰后续渲染）。
  let textArea: HTMLTextAreaElement | null = null
  try {
    textArea = document.createElement('textarea')
    textArea.value = text
    textArea.style.position = 'fixed'
    textArea.style.opacity = '0'
    textArea.style.left = '-9999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    textArea?.remove()
  }
}
