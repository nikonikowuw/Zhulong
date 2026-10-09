/**
 * 对 RTSP URL 中的密码凭据进行掩码处理
 * 例如: rtsp://admin:123456@192.168.1.100:554/live -> rtsp://admin:••••••@192.168.1.100:554/live
 */
export function maskRtspUrl(url: string): string {
  if (!url) return ''
  // 匹配 rtsp://username:password@host
  return url.replace(
    /^(rtsp:\/\/[^:]+:)([^@]+)(@.+)$/i,
    (_match, prefix, pass, suffix) => {
      const dots = '•'.repeat(Math.min(pass.length, 8))
      return `${prefix}${dots}${suffix}`
    }
  )
}

/**
 * 从 RTSP URL 中提取目标主机 IP 或域名
 * 例如: rtsp://admin:123456@192.168.1.100:554/live -> 192.168.1.100
 */
export function extractIpFromRtspUrl(url?: string): string {
  if (!url) return '-'
  const matchWithAuth = url.match(/@([^:/]+)/)
  if (matchWithAuth) return matchWithAuth[1]
  const matchWithoutAuth = url.match(/^rtsp:\/\/([^:/]+)/i)
  if (matchWithoutAuth) return matchWithoutAuth[1]
  return '-'
}

/**
 * 跨浏览器安全复制文本到剪贴板，带回退方案
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!text) return false
  if (navigator?.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 降级回退
    }
  }

  try {
    const textArea = document.createElement('textarea')
    textArea.value = text
    textArea.style.position = 'fixed'
    textArea.style.opacity = '0'
    textArea.style.left = '-9999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()
    const success = document.execCommand('copy')
    document.body.removeChild(textArea)
    return success
  } catch {
    return false
  }
}
