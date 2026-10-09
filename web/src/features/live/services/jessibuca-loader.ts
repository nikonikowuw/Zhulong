/**
 * Jessibuca 离线静态资源异步加载器
 */

export interface JessibucaConfig {
  container: HTMLElement
  videoBuffer?: number
  isResize?: boolean
  useWCS?: boolean
  useMSE?: boolean
  autoWasm?: boolean
  wasmUrl?: string
  decoder?: string
  demuxUseWorker?: boolean
  operateBtns?: {
    fullscreen?: boolean
    screenshot?: boolean
    play?: boolean
    audio?: boolean
    record?: boolean
  }
  forceNoOffscreen?: boolean
  isNotMute?: boolean
  timeout?: number
}

export interface JessibucaInstance {
  play: (url: string) => Promise<void>
  pause: () => Promise<void>
  destroy: () => Promise<void>
  mute: () => void
  cancelMute: () => void
  screenshot: (
    filename?: string,
    format?: 'png' | 'jpeg',
    quality?: number
  ) => void
  resize: () => void
  on: (event: string, callback: (...args: unknown[]) => void) => void
  hasLoaded: () => boolean
}

export interface JessibucaConstructor {
  new (config: JessibucaConfig): JessibucaInstance
}

declare global {
  interface Window {
    Jessibuca?: JessibucaConstructor
  }
}

let loaderPromise: Promise<JessibucaConstructor> | null = null

export function loadJessibuca(): Promise<JessibucaConstructor> {
  if (typeof window === 'undefined') {
    return Promise.reject(
      new Error('Jessibuca can only be loaded in browser environment')
    )
  }

  if (window.Jessibuca) {
    return Promise.resolve(window.Jessibuca)
  }

  if (loaderPromise) {
    return loaderPromise
  }

  loaderPromise = new Promise<JessibucaConstructor>((resolve, reject) => {
    // 检查是否已存在 script 标签
    const existingScript = document.querySelector('script[src*="jessibuca.js"]')
    if (existingScript) {
      existingScript.addEventListener('load', () => {
        if (window.Jessibuca) resolve(window.Jessibuca)
        else
          reject(
            new Error(
              'Jessibuca script loaded but window.Jessibuca is undefined'
            )
          )
      })
      existingScript.addEventListener('error', () => {
        reject(new Error('Failed to load existing Jessibuca script'))
      })
      return
    }

    const script = document.createElement('script')
    script.src = '/vendor/jessibuca/jessibuca.js'
    script.async = true

    script.onload = () => {
      if (window.Jessibuca) {
        resolve(window.Jessibuca)
      } else {
        reject(new Error('window.Jessibuca not found after script load'))
      }
    }

    script.onerror = () => {
      reject(new Error('Failed to load /vendor/jessibuca/jessibuca.js'))
    }

    document.head.appendChild(script)
  })

  return loaderPromise
}
