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
  if (!url || !url.toLowerCase().startsWith('rtsp://')) return '-'
  try {
    const parsed = new URL(url)
    return parsed.hostname.replace(/^\[|\]$/g, '') || '-'
  } catch {
    const matchWithAuth = url.match(/@(?:\[([a-fA-F0-9:]+)\]|([^:/]+))/)
    if (matchWithAuth) return matchWithAuth[1] || matchWithAuth[2] || '-'
    const matchWithoutAuth = url.match(
      /^rtsp:\/\/(?:\[([a-fA-F0-9:]+)\]|([^:/]+))/i
    )
    if (matchWithoutAuth)
      return matchWithoutAuth[1] || matchWithoutAuth[2] || '-'
    return '-'
  }
}
