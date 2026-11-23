export interface TerrainPalette {
  valley: string
  mid: string
  crest: string
  signal: string
  mote: string
  lineOpacity: number
  particleOpacity: number
  moteOpacity: number
}

interface TerrainPalettes {
  light: TerrainPalette
  dark: TerrainPalette
}

/**
 * 与 `src/styles/theme.css` 中的 OKLCH 语义色对齐的双模色板：
 * - 暗色模式：深海玄青底上浮现冷银山脊，点缀 `--brand-signal` 激光赤曜红脉动
 * - 浅色模式：宣纸白底上的青灰等高线版画，保留克制的激光赤红高程亮点
 */
export const TERRAIN_PALETTES: TerrainPalettes = {
  dark: {
    valley: '#162544',
    mid: '#3f6496',
    crest: '#dbeafe',
    signal: '#FF2A54',
    mote: '#cbd5e1',
    lineOpacity: 0.42,
    particleOpacity: 0.86,
    moteOpacity: 0.52,
  },
  light: {
    valley: '#cbd5e1',
    mid: '#64748b',
    crest: '#1e293b',
    signal: '#E11D48',
    mote: '#94a3b8',
    lineOpacity: 0.28,
    particleOpacity: 0.66,
    moteOpacity: 0.42,
  },
}

const TERRAIN_DIMENSIONS = {
  width: 48,
  depth: 27,
  zMin: -20,
  zMax: 7,
  ridgeRows: 72,
  ridgeCols: 150,
  particleRows: 72,
  particleCols: 120,
  moteCount: 160,
} as const

export const STATIC_POSE_TIME = 3.2

export function clampPixelRatio(dpr: number): number {
  if (!Number.isFinite(dpr) || dpr <= 0) {
    return 1
  }
  return Math.min(Math.max(dpr, 1), 2)
}

export function resolveIsDarkMode(root?: HTMLElement | null): boolean {
  const target =
    root ??
    (typeof document !== 'undefined' ? document.documentElement : undefined)

  if (target) {
    if (target.classList.contains('dark')) {
      return true
    }
    if (target.classList.contains('light')) {
      return false
    }
  }

  if (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function'
  ) {
    return window.matchMedia('(prefers-color-scheme: dark)').matches
  }

  return false
}

function createSeededRandom(seed: number): () => number {
  let state = seed >>> 0
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0
    return state / 0x100000000
  }
}

interface RidgeGeometryData {
  positions: Float32Array
  uvs: Float32Array
}

/**
 * 构建沿纬线延展的平行等高线线段数据：只生成一次，每帧波形起伏在顶点着色器内完成。
 */
export function buildRidgeLineData(
  cols: number = TERRAIN_DIMENSIONS.ridgeCols,
  rows: number = TERRAIN_DIMENSIONS.ridgeRows,
  width: number = TERRAIN_DIMENSIONS.width,
  zMin: number = TERRAIN_DIMENSIONS.zMin,
  zMax: number = TERRAIN_DIMENSIONS.zMax
): RidgeGeometryData {
  const segmentCount = rows * cols
  const vertexCount = segmentCount * 2
  const positions = new Float32Array(vertexCount * 3)
  const uvs = new Float32Array(vertexCount * 2)

  let posOffset = 0
  let uvOffset = 0

  for (let row = 0; row < rows; row++) {
    const v = rows > 1 ? row / (rows - 1) : 0
    const z = zMin + v * (zMax - zMin)

    for (let col = 0; col < cols; col++) {
      const u0 = col / cols
      const u1 = (col + 1) / cols
      const x0 = (u0 - 0.5) * width
      const x1 = (u1 - 0.5) * width

      positions[posOffset++] = x0
      positions[posOffset++] = 0
      positions[posOffset++] = z

      positions[posOffset++] = x1
      positions[posOffset++] = 0
      positions[posOffset++] = z

      uvs[uvOffset++] = u0
      uvs[uvOffset++] = v

      uvs[uvOffset++] = u1
      uvs[uvOffset++] = v
    }
  }

  return { positions, uvs }
}

interface ParticleGeometryData {
  positions: Float32Array
  uvs: Float32Array
  seeds: Float32Array
}

/**
 * 构建地形高程粒子场数据：在规则晶格上加入确定性微扰，既保留山脊韵律又消除机械网格感。
 */
export function buildParticleFieldData(
  cols: number = TERRAIN_DIMENSIONS.particleCols,
  rows: number = TERRAIN_DIMENSIONS.particleRows,
  width: number = TERRAIN_DIMENSIONS.width,
  zMin: number = TERRAIN_DIMENSIONS.zMin,
  zMax: number = TERRAIN_DIMENSIONS.zMax
): ParticleGeometryData {
  const count = cols * rows
  const positions = new Float32Array(count * 3)
  const uvs = new Float32Array(count * 2)
  const seeds = new Float32Array(count * 3)
  const rand = createSeededRandom(0x5a68756c)

  const stepX = width / Math.max(cols - 1, 1)
  const stepZ = (zMax - zMin) / Math.max(rows - 1, 1)

  let posOffset = 0
  let uvOffset = 0
  let seedOffset = 0

  for (let row = 0; row < rows; row++) {
    const v = rows > 1 ? row / (rows - 1) : 0
    const baseZ = zMin + v * (zMax - zMin)

    for (let col = 0; col < cols; col++) {
      const u = cols > 1 ? col / (cols - 1) : 0
      const baseX = (u - 0.5) * width

      const jitterX = (rand() - 0.5) * stepX * 0.65
      const jitterZ = (rand() - 0.5) * stepZ * 0.65

      positions[posOffset++] = baseX + jitterX
      positions[posOffset++] = 0
      positions[posOffset++] = baseZ + jitterZ

      uvs[uvOffset++] = u
      uvs[uvOffset++] = v

      seeds[seedOffset++] = rand()
      seeds[seedOffset++] = rand()
      seeds[seedOffset++] = rand()
    }
  }

  return { positions, uvs, seeds }
}

interface MoteGeometryData {
  positions: Float32Array
  seeds: Float32Array
}

/**
 * 构建空中漂浮微尘粒子数据，提供背景景深与低频光点呼吸感。
 */
export function buildAtmosphereMoteData(
  count: number = TERRAIN_DIMENSIONS.moteCount
): MoteGeometryData {
  const positions = new Float32Array(count * 3)
  const seeds = new Float32Array(count * 3)
  const rand = createSeededRandom(0x4c6f6e67)

  for (let i = 0; i < count; i++) {
    const posIdx = i * 3
    positions[posIdx] = (rand() - 0.5) * 34
    positions[posIdx + 1] = 0.8 + rand() * 7.2
    positions[posIdx + 2] = -15 + rand() * 20

    seeds[posIdx] = rand()
    seeds[posIdx + 1] = rand()
    seeds[posIdx + 2] = rand()
  }

  return { positions, seeds }
}

const TERRAIN_EVALUATION_GLSL = /* glsl */ `
float hash21(vec2 p) {
  p = fract(p * vec2(123.34, 456.21));
  p += dot(p, p + 45.32);
  return fract(p.x * p.y);
}

float valueNoise(vec2 p) {
  vec2 i = floor(p);
  vec2 f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  float a = hash21(i);
  float b = hash21(i + vec2(1.0, 0.0));
  float c = hash21(i + vec2(0.0, 1.0));
  float d = hash21(i + vec2(1.0, 1.0));
  return mix(mix(a, b, u.x), mix(c, d, u.x), u.y);
}

float fbm(vec2 p) {
  float value = 0.0;
  float amp = 0.55;
  mat2 rot = mat2(0.80, 0.60, -0.60, 0.80);
  for (int i = 0; i < 4; i++) {
    value += amp * (valueNoise(p) * 2.0 - 1.0);
    p = rot * p * 2.03 + vec2(17.1, 31.7);
    amp *= 0.48;
  }
  return value;
}

struct TerrainSample {
  float height;
  float ridge;
  float signal;
  float edgeFade;
};

TerrainSample evaluateTerrain(vec2 xz, vec2 uvCoord, float time, vec2 pointer) {
  vec2 flow = vec2(xz.x * 0.13 + time * 0.075, xz.y * 0.16 - time * 0.11);
  float n1 = fbm(flow);
  float n2 = fbm(flow * 0.55 + vec2(8.3, -4.7) + time * 0.03);

  float ridge = 1.0 - abs(n1);
  ridge = pow(clamp(ridge, 0.0, 1.0), 1.65);

  float swell =
    sin(xz.x * 0.17 + time * 0.21) * cos(xz.y * 0.15 - time * 0.15) * 0.52;

  // 居中卡片两侧山脊略高、正中留出视觉呼吸谷地
  float wingScale = mix(0.62, 1.42, smoothstep(2.6, 13.5, abs(xz.x)));

  vec2 focusXZ = vec2(pointer.x * 9.0, -3.0 - pointer.y * 5.5);
  float focusDist = length(xz - focusXZ);
  float pointerRipple = exp(-focusDist * focusDist * 0.042) * 0.46;

  float edgeX = smoothstep(0.0, 0.15, uvCoord.x) * (1.0 - smoothstep(0.85, 1.0, uvCoord.x));
  float edgeZ = smoothstep(0.0, 0.18, uvCoord.y) * (1.0 - smoothstep(0.86, 1.0, uvCoord.y));
  float edgeFade = edgeX * edgeZ;

  float rawHeight = (ridge * 2.85 + n2 * 0.72 + swell) * wingScale + pointerRipple;
  float height = (rawHeight - 0.55) * mix(0.35, 1.0, edgeZ);

  float energyWave =
    0.5 + 0.5 * sin(xz.x * 0.24 - xz.y * 0.18 - time * 0.62 + n1 * 2.6);
  float crestSignal = smoothstep(0.56, 0.96, ridge) * pow(energyWave, 2.2);
  float focusSignal = exp(-focusDist * focusDist * 0.055) * 0.55;
  float signal = clamp(crestSignal + focusSignal, 0.0, 1.0);

  return TerrainSample(height, ridge, signal, edgeFade);
}
`

export const RIDGE_VERTEX_SHADER = /* glsl */ `
uniform float uTime;
uniform vec2 uPointer;

attribute vec2 aUv;

varying float vRidge;
varying float vSignal;
varying float vEdgeFade;
varying float vDepthFade;

${TERRAIN_EVALUATION_GLSL}

void main() {
  TerrainSample sampleData = evaluateTerrain(position.xz, aUv, uTime, uPointer);
  vec3 displaced = vec3(position.x, sampleData.height, position.z);
  vec4 mvPosition = modelViewMatrix * vec4(displaced, 1.0);

  vRidge = sampleData.ridge;
  vSignal = sampleData.signal;
  vEdgeFade = sampleData.edgeFade;
  vDepthFade = smoothstep(38.0, 12.0, -mvPosition.z);

  gl_Position = projectionMatrix * mvPosition;
}
`

export const RIDGE_FRAGMENT_SHADER = /* glsl */ `
uniform vec3 uValleyColor;
uniform vec3 uMidColor;
uniform vec3 uCrestColor;
uniform vec3 uSignalColor;
uniform float uOpacity;

varying float vRidge;
varying float vSignal;
varying float vEdgeFade;
varying float vDepthFade;

void main() {
  vec3 baseColor = mix(uValleyColor, uMidColor, smoothstep(0.12, 0.62, vRidge));
  baseColor = mix(baseColor, uCrestColor, smoothstep(0.55, 0.98, vRidge));
  vec3 finalColor = mix(baseColor, uSignalColor, vSignal * 0.78);

  float elevationAlpha = mix(0.24, 1.0, smoothstep(0.1, 0.9, vRidge));
  float alpha = uOpacity * elevationAlpha * vEdgeFade * vDepthFade;
  if (alpha <= 0.004) {
    discard;
  }

  gl_FragColor = vec4(finalColor, alpha);
  #include <colorspace_fragment>
}
`

export const PARTICLE_VERTEX_SHADER = /* glsl */ `
uniform float uTime;
uniform float uPixelRatio;
uniform vec2 uPointer;

attribute vec2 aUv;
attribute vec3 aSeed;

varying float vRidge;
varying float vSignal;
varying float vEdgeFade;
varying float vDepthFade;
varying float vTwinkle;

${TERRAIN_EVALUATION_GLSL}

void main() {
  TerrainSample sampleData = evaluateTerrain(position.xz, aUv, uTime, uPointer);
  float hover = sin(uTime * 0.85 + aSeed.x * 6.28318) * 0.085;
  vec3 displaced = vec3(position.x, sampleData.height + hover + 0.04, position.z);

  vec4 mvPosition = modelViewMatrix * vec4(displaced, 1.0);

  vRidge = sampleData.ridge;
  vSignal = sampleData.signal;
  vEdgeFade = sampleData.edgeFade;
  vDepthFade = smoothstep(38.0, 11.5, -mvPosition.z);
  vTwinkle = 0.65 + 0.35 * sin(uTime * (0.9 + aSeed.y * 1.4) + aSeed.z * 6.28318);

  float baseSize = mix(18.0, 40.0, smoothstep(0.25, 0.95, sampleData.ridge));
  baseSize += sampleData.signal * 14.0;
  float pointSize = baseSize * uPixelRatio / max(-mvPosition.z, 2.5);
  gl_PointSize = clamp(pointSize, 1.25, 9.5 * uPixelRatio);

  gl_Position = projectionMatrix * mvPosition;
}
`

export const PARTICLE_FRAGMENT_SHADER = /* glsl */ `
uniform vec3 uValleyColor;
uniform vec3 uMidColor;
uniform vec3 uCrestColor;
uniform vec3 uSignalColor;
uniform float uOpacity;

varying float vRidge;
varying float vSignal;
varying float vEdgeFade;
varying float vDepthFade;
varying float vTwinkle;

void main() {
  vec2 centered = gl_PointCoord - vec2(0.5);
  float dist = length(centered);
  if (dist > 0.5) {
    discard;
  }

  float radial = smoothstep(0.5, 0.06, dist);
  vec3 baseColor = mix(uValleyColor, uMidColor, smoothstep(0.15, 0.64, vRidge));
  baseColor = mix(baseColor, uCrestColor, smoothstep(0.55, 0.96, vRidge));
  vec3 finalColor = mix(baseColor, uSignalColor, vSignal * 0.88);

  float elevationAlpha = mix(0.28, 1.0, smoothstep(0.08, 0.9, vRidge));
  float alpha =
    uOpacity * radial * elevationAlpha * vEdgeFade * vDepthFade * vTwinkle;
  if (alpha <= 0.004) {
    discard;
  }

  gl_FragColor = vec4(finalColor, alpha);
  #include <colorspace_fragment>
}
`

export const MOTE_VERTEX_SHADER = /* glsl */ `
uniform float uTime;
uniform float uPixelRatio;
uniform vec2 uPointer;

attribute vec3 aSeed;

varying float vSignalMix;
varying float vAlphaFade;

void main() {
  float driftX = sin(uTime * 0.16 + aSeed.x * 6.28318) * 0.9;
  float driftY = mod(position.y + uTime * (0.06 + aSeed.y * 0.08), 7.8) + 0.5;
  float driftZ = cos(uTime * 0.14 + aSeed.z * 6.28318) * 0.6;

  vec3 pos = vec3(
    position.x + driftX + uPointer.x * (0.45 + aSeed.z * 0.4),
    driftY - uPointer.y * 0.25,
    position.z + driftZ
  );

  vec4 mvPosition = modelViewMatrix * vec4(pos, 1.0);

  vSignalMix = smoothstep(0.62, 0.96, aSeed.z);
  float heightFade =
    smoothstep(0.5, 1.6, driftY) * (1.0 - smoothstep(6.6, 8.3, driftY));
  float depthFade = smoothstep(34.0, 9.0, -mvPosition.z);
  float pulse = 0.55 + 0.45 * sin(uTime * 1.1 + aSeed.x * 12.56);
  vAlphaFade = heightFade * depthFade * pulse;

  float size = mix(18.0, 32.0, aSeed.y) * uPixelRatio / max(-mvPosition.z, 3.0);
  gl_PointSize = clamp(size, 1.2, 7.5 * uPixelRatio);

  gl_Position = projectionMatrix * mvPosition;
}
`

export const MOTE_FRAGMENT_SHADER = /* glsl */ `
uniform vec3 uMoteColor;
uniform vec3 uSignalColor;
uniform float uOpacity;

varying float vSignalMix;
varying float vAlphaFade;

void main() {
  vec2 centered = gl_PointCoord - vec2(0.5);
  float dist = length(centered);
  if (dist > 0.5) {
    discard;
  }

  float radial = smoothstep(0.5, 0.05, dist);
  vec3 finalColor = mix(uMoteColor, uSignalColor, vSignalMix * 0.85);
  float alpha = uOpacity * radial * vAlphaFade;
  if (alpha <= 0.004) {
    discard;
  }

  gl_FragColor = vec4(finalColor, alpha);
  #include <colorspace_fragment>
}
`
