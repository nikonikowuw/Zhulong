import {
  BufferAttribute,
  BufferGeometry,
  Color,
  LineSegments,
  NormalBlending,
  NoToneMapping,
  PerspectiveCamera,
  Points,
  Scene,
  ShaderMaterial,
  SRGBColorSpace,
  Vector2,
  WebGLRenderer,
} from 'three'
import {
  buildAtmosphereMoteData,
  buildParticleFieldData,
  buildRidgeLineData,
  clampPixelRatio,
  MOTE_FRAGMENT_SHADER,
  MOTE_VERTEX_SHADER,
  PARTICLE_FRAGMENT_SHADER,
  PARTICLE_VERTEX_SHADER,
  resolveIsDarkMode,
  RIDGE_FRAGMENT_SHADER,
  RIDGE_VERTEX_SHADER,
  STATIC_POSE_TIME,
  TERRAIN_PALETTES,
  type TerrainPalette,
} from './terrain-config'

interface TerrainSceneOptions {
  onReady?: () => void
  onUnavailable?: () => void
}

interface TerrainSceneHandle {
  dispose: () => void
}

const CAMERA_BASE_X = 0
const CAMERA_BASE_Y = 4.8
const CAMERA_BASE_Z = 14.5
const CAMERA_TARGET_Y = 0.7
const CAMERA_TARGET_Z = -5.0

interface PaletteTargets {
  valley: Color
  mid: Color
  crest: Color
  signal: Color
  mote: Color
  lineOpacity: number
  particleOpacity: number
  moteOpacity: number
}

function createPaletteTargets(palette: TerrainPalette): PaletteTargets {
  return {
    valley: new Color(palette.valley),
    mid: new Color(palette.mid),
    crest: new Color(palette.crest),
    signal: new Color(palette.signal),
    mote: new Color(palette.mote),
    lineOpacity: palette.lineOpacity,
    particleOpacity: palette.particleOpacity,
    moteOpacity: palette.moteOpacity,
  }
}

function createRendererSafely(): WebGLRenderer | null {
  try {
    return new WebGLRenderer({
      antialias: true,
      alpha: true,
      powerPreference: 'low-power',
    })
  } catch {
    return null
  }
}

/**
 * 在指定容器内挂载原生 Three.js 流动地形场景。
 * 负责 WebGL 上下文兜底、深浅主题平滑过渡、reduced-motion 降级、后台节流与完整显存回收。
 */
export function mountTerrainScene(
  container: HTMLElement,
  options: TerrainSceneOptions = {}
): TerrainSceneHandle | null {
  const renderer = createRendererSafely()
  if (!renderer) {
    options.onUnavailable?.()
    return null
  }

  let disposed = false
  let readyNotified = false
  let rafId = 0
  let elapsed = STATIC_POSE_TIME
  let lastFrameMs = 0

  const reducedMotionQuery =
    typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-reduced-motion: reduce)')
      : null
  const colorSchemeQuery =
    typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-color-scheme: dark)')
      : null

  let isReducedMotion = reducedMotionQuery?.matches ?? false
  let isDarkMode = resolveIsDarkMode()

  const targetPointer = new Vector2(0, 0)
  const currentPointer = new Vector2(0, 0)

  const palettes = {
    light: createPaletteTargets(TERRAIN_PALETTES.light),
    dark: createPaletteTargets(TERRAIN_PALETTES.dark),
  }

  const activePalette = isDarkMode ? palettes.dark : palettes.light
  const uniformColors = {
    valley: activePalette.valley.clone(),
    mid: activePalette.mid.clone(),
    crest: activePalette.crest.clone(),
    signal: activePalette.signal.clone(),
    mote: activePalette.mote.clone(),
  }

  const canvas = renderer.domElement
  canvas.setAttribute('aria-hidden', 'true')
  canvas.tabIndex = -1
  canvas.className = 'pointer-events-none block h-full w-full'

  renderer.outputColorSpace = SRGBColorSpace
  renderer.toneMapping = NoToneMapping
  renderer.setClearColor(0x000000, 0)

  const scene = new Scene()
  const initialWidth = Math.max(container.clientWidth, 1)
  const initialHeight = Math.max(container.clientHeight, 1)
  const camera = new PerspectiveCamera(
    44,
    initialWidth / initialHeight,
    0.1,
    100
  )
  camera.position.set(CAMERA_BASE_X, CAMERA_BASE_Y, CAMERA_BASE_Z)
  camera.lookAt(0, CAMERA_TARGET_Y, CAMERA_TARGET_Z)

  const ridgeData = buildRidgeLineData()
  const ridgeGeometry = new BufferGeometry()
  ridgeGeometry.setAttribute(
    'position',
    new BufferAttribute(ridgeData.positions, 3)
  )
  ridgeGeometry.setAttribute('aUv', new BufferAttribute(ridgeData.uvs, 2))

  const ridgeUniforms = {
    uTime: { value: elapsed },
    uPointer: { value: currentPointer },
    uValleyColor: { value: uniformColors.valley },
    uMidColor: { value: uniformColors.mid },
    uCrestColor: { value: uniformColors.crest },
    uSignalColor: { value: uniformColors.signal },
    uOpacity: { value: activePalette.lineOpacity },
  }

  const ridgeMaterial = new ShaderMaterial({
    vertexShader: RIDGE_VERTEX_SHADER,
    fragmentShader: RIDGE_FRAGMENT_SHADER,
    uniforms: ridgeUniforms,
    transparent: true,
    depthWrite: false,
    blending: NormalBlending,
  })
  const ridgeMesh = new LineSegments(ridgeGeometry, ridgeMaterial)
  ridgeMesh.frustumCulled = false
  scene.add(ridgeMesh)

  const particleData = buildParticleFieldData()
  const particleGeometry = new BufferGeometry()
  particleGeometry.setAttribute(
    'position',
    new BufferAttribute(particleData.positions, 3)
  )
  particleGeometry.setAttribute('aUv', new BufferAttribute(particleData.uvs, 2))
  particleGeometry.setAttribute(
    'aSeed',
    new BufferAttribute(particleData.seeds, 3)
  )

  const initialDpr = clampPixelRatio(window.devicePixelRatio || 1)
  const particleUniforms = {
    uTime: { value: elapsed },
    uPixelRatio: { value: initialDpr },
    uPointer: { value: currentPointer },
    uValleyColor: { value: uniformColors.valley },
    uMidColor: { value: uniformColors.mid },
    uCrestColor: { value: uniformColors.crest },
    uSignalColor: { value: uniformColors.signal },
    uOpacity: { value: activePalette.particleOpacity },
  }

  const particleMaterial = new ShaderMaterial({
    vertexShader: PARTICLE_VERTEX_SHADER,
    fragmentShader: PARTICLE_FRAGMENT_SHADER,
    uniforms: particleUniforms,
    transparent: true,
    depthWrite: false,
    blending: NormalBlending,
  })
  const particlePoints = new Points(particleGeometry, particleMaterial)
  particlePoints.frustumCulled = false
  scene.add(particlePoints)

  const moteData = buildAtmosphereMoteData()
  const moteGeometry = new BufferGeometry()
  moteGeometry.setAttribute(
    'position',
    new BufferAttribute(moteData.positions, 3)
  )
  moteGeometry.setAttribute('aSeed', new BufferAttribute(moteData.seeds, 3))

  const moteUniforms = {
    uTime: { value: elapsed },
    uPixelRatio: { value: initialDpr },
    uPointer: { value: currentPointer },
    uMoteColor: { value: uniformColors.mote },
    uSignalColor: { value: uniformColors.signal },
    uOpacity: { value: activePalette.moteOpacity },
  }

  const moteMaterial = new ShaderMaterial({
    vertexShader: MOTE_VERTEX_SHADER,
    fragmentShader: MOTE_FRAGMENT_SHADER,
    uniforms: moteUniforms,
    transparent: true,
    depthWrite: false,
    blending: NormalBlending,
  })
  const motePoints = new Points(moteGeometry, moteMaterial)
  motePoints.frustumCulled = false
  scene.add(motePoints)

  const syncSize = () => {
    if (disposed) {
      return
    }
    const width = Math.max(container.clientWidth, 1)
    const height = Math.max(container.clientHeight, 1)
    const aspect = width / height
    const dpr = clampPixelRatio(window.devicePixelRatio || 1)

    camera.aspect = aspect
    camera.fov = aspect < 1 ? 44 + Math.min(1, (1 - aspect) / 0.55) * 10 : 44
    camera.updateProjectionMatrix()

    renderer.setPixelRatio(dpr)
    renderer.setSize(width, height, false)

    particleUniforms.uPixelRatio.value = dpr
    moteUniforms.uPixelRatio.value = dpr
  }

  const applyTargetPalette = (snap: boolean) => {
    const target = isDarkMode ? palettes.dark : palettes.light
    if (snap) {
      uniformColors.valley.copy(target.valley)
      uniformColors.mid.copy(target.mid)
      uniformColors.crest.copy(target.crest)
      uniformColors.signal.copy(target.signal)
      uniformColors.mote.copy(target.mote)
      ridgeUniforms.uOpacity.value = target.lineOpacity
      particleUniforms.uOpacity.value = target.particleOpacity
      moteUniforms.uOpacity.value = target.moteOpacity
    }
  }

  const drawFrame = () => {
    if (disposed) {
      return
    }
    camera.position.set(
      CAMERA_BASE_X + currentPointer.x * 1.1,
      CAMERA_BASE_Y - currentPointer.y * 0.42,
      CAMERA_BASE_Z
    )
    camera.lookAt(
      currentPointer.x * 0.32,
      CAMERA_TARGET_Y - currentPointer.y * 0.18,
      CAMERA_TARGET_Z
    )

    ridgeUniforms.uTime.value = elapsed
    particleUniforms.uTime.value = elapsed
    moteUniforms.uTime.value = elapsed

    renderer.render(scene, camera)

    if (!readyNotified) {
      readyNotified = true
      options.onReady?.()
    }
  }

  const stopLoop = () => {
    if (rafId !== 0) {
      cancelAnimationFrame(rafId)
      rafId = 0
    }
  }

  const stepAnimation = (nowMs: number) => {
    if (disposed || isReducedMotion || document.hidden) {
      rafId = 0
      return
    }

    const dt =
      lastFrameMs > 0 ? Math.min((nowMs - lastFrameMs) / 1000, 0.05) : 0.016
    lastFrameMs = nowMs
    elapsed += dt

    const pointerBlend = Math.min(1, dt * 4.5)
    currentPointer.lerp(targetPointer, pointerBlend)

    const colorBlend = Math.min(1, dt * 5.5)
    const target = isDarkMode ? palettes.dark : palettes.light
    uniformColors.valley.lerp(target.valley, colorBlend)
    uniformColors.mid.lerp(target.mid, colorBlend)
    uniformColors.crest.lerp(target.crest, colorBlend)
    uniformColors.signal.lerp(target.signal, colorBlend)
    uniformColors.mote.lerp(target.mote, colorBlend)
    ridgeUniforms.uOpacity.value +=
      (target.lineOpacity - ridgeUniforms.uOpacity.value) * colorBlend
    particleUniforms.uOpacity.value +=
      (target.particleOpacity - particleUniforms.uOpacity.value) * colorBlend
    moteUniforms.uOpacity.value +=
      (target.moteOpacity - moteUniforms.uOpacity.value) * colorBlend

    drawFrame()
    rafId = requestAnimationFrame(stepAnimation)
  }

  const startLoop = () => {
    if (disposed || isReducedMotion || document.hidden || rafId !== 0) {
      return
    }
    lastFrameMs = 0
    rafId = requestAnimationFrame(stepAnimation)
  }

  const onPointerMove = (event: PointerEvent) => {
    if (isReducedMotion) {
      return
    }
    const width = Math.max(window.innerWidth, 1)
    const height = Math.max(window.innerHeight, 1)
    targetPointer.set(
      (event.clientX / width) * 2 - 1,
      -(event.clientY / height) * 2 + 1
    )
  }

  const onPointerLeave = () => {
    targetPointer.set(0, 0)
  }

  const onThemeChange = () => {
    const nextDark = resolveIsDarkMode()
    if (nextDark === isDarkMode) {
      return
    }
    isDarkMode = nextDark
    if (isReducedMotion) {
      applyTargetPalette(true)
      drawFrame()
    }
  }

  const onReducedMotionChange = (event: MediaQueryListEvent) => {
    isReducedMotion = event.matches
    if (isReducedMotion) {
      stopLoop()
      currentPointer.set(0, 0)
      targetPointer.set(0, 0)
      applyTargetPalette(true)
      drawFrame()
    } else {
      startLoop()
    }
  }

  const onVisibilityChange = () => {
    if (document.hidden) {
      stopLoop()
    } else if (!isReducedMotion) {
      startLoop()
    }
  }

  const onContextLost = (event: Event) => {
    event.preventDefault()
    stopLoop()
    options.onUnavailable?.()
  }

  container.appendChild(canvas)
  syncSize()
  applyTargetPalette(true)

  let resizeObserver: ResizeObserver | null = null
  if (typeof ResizeObserver === 'function') {
    resizeObserver = new ResizeObserver(() => {
      syncSize()
      if (isReducedMotion) {
        drawFrame()
      }
    })
    resizeObserver.observe(container)
  }

  const onWindowResize = () => {
    syncSize()
    if (isReducedMotion) {
      drawFrame()
    }
  }

  const themeObserver =
    typeof MutationObserver === 'function'
      ? new MutationObserver(onThemeChange)
      : null
  themeObserver?.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class'],
  })

  window.addEventListener('resize', onWindowResize)
  window.addEventListener('pointermove', onPointerMove, { passive: true })
  document.documentElement.addEventListener('pointerleave', onPointerLeave)
  document.addEventListener('visibilitychange', onVisibilityChange)
  canvas.addEventListener('webglcontextlost', onContextLost)
  reducedMotionQuery?.addEventListener('change', onReducedMotionChange)
  colorSchemeQuery?.addEventListener('change', onThemeChange)

  // 首帧立即绘制，避免等待第一拍 rAF 造成的视觉空档
  drawFrame()
  if (!isReducedMotion) {
    startLoop()
  }

  return {
    dispose: () => {
      if (disposed) {
        return
      }
      disposed = true
      stopLoop()

      resizeObserver?.disconnect()
      themeObserver?.disconnect()
      window.removeEventListener('resize', onWindowResize)
      window.removeEventListener('pointermove', onPointerMove)
      document.documentElement.removeEventListener(
        'pointerleave',
        onPointerLeave
      )
      document.removeEventListener('visibilitychange', onVisibilityChange)
      canvas.removeEventListener('webglcontextlost', onContextLost)
      reducedMotionQuery?.removeEventListener('change', onReducedMotionChange)
      colorSchemeQuery?.removeEventListener('change', onThemeChange)

      ridgeGeometry.dispose()
      ridgeMaterial.dispose()
      particleGeometry.dispose()
      particleMaterial.dispose()
      moteGeometry.dispose()
      moteMaterial.dispose()
      renderer.dispose()

      if (canvas.parentElement === container) {
        container.removeChild(canvas)
      }
    },
  }
}
