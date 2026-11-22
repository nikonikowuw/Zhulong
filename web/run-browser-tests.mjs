import { spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = dirname(fileURLToPath(import.meta.url))
const vitestEntry = resolve(webDir, 'node_modules/vitest/vitest.mjs')

const rawArgs = process.argv.slice(2)
// `pnpm test -- <filter>` passes a literal `--`; strip it so Vitest receives file filters.
const cliArgs = rawArgs[0] === '--' ? rawArgs.slice(1) : rawArgs

const valueFlags = new Set([
  '-c',
  '--config',
  '-r',
  '--root',
  '--dir',
  '-t',
  '--testNamePattern',
  '--project',
  '--reporter',
  '--outputFile',
  '--shard',
  '--maxWorkers',
  '--max-workers',
  '--bail',
  '--testTimeout',
  '--hookTimeout',
  '--retry',
  '--sequence',
  '--environment',
  '--pool',
])

let hasShard = false
let hasMaxWorkers = false
let hasFileFilter = false

for (let i = 0; i < cliArgs.length; i++) {
  const arg = cliArgs[i]
  if (arg === '--') {
    if (i + 1 < cliArgs.length) {
      hasFileFilter = true
    }
    break
  }
  if (arg === '--shard' || arg.startsWith('--shard=')) {
    hasShard = true
  }
  if (
    arg === '--maxWorkers' ||
    arg.startsWith('--maxWorkers=') ||
    arg === '--max-workers' ||
    arg.startsWith('--max-workers=')
  ) {
    hasMaxWorkers = true
  }
  if (arg.startsWith('-')) {
    if (!arg.includes('=') && valueFlags.has(arg)) {
      i++
    }
    continue
  }
  hasFileFilter = true
}

function runVitest(extraArgs) {
  const result = spawnSync(
    process.execPath,
    [vitestEntry, 'run', '--browser.headless', ...extraArgs],
    {
      cwd: webDir,
      stdio: 'inherit',
      env: process.env,
    }
  )
  if (result.error) {
    throw result.error
  }
  if (result.signal) {
    process.kill(process.pid, result.signal)
    return 1
  }
  return result.status ?? 1
}

if (hasShard || hasFileFilter) {
  process.exit(runVitest(cliArgs))
}

const rawShards = (process.env.VITEST_SHARDS ?? '6').trim()
const totalShards = Number.parseInt(rawShards, 10)
if (
  !Number.isInteger(totalShards) ||
  totalShards < 1 ||
  String(totalShards) !== rawShards
) {
  console.error(
    `Invalid VITEST_SHARDS=${rawShards}; expected a positive integer.`
  )
  process.exit(2)
}

// Chromium hits net::ERR_INSUFFICIENT_RESOURCES once a single Vitest browser session
// accumulates too many isolated iframes; run the full suite across bounded single-worker shards.
const workerArgs = hasMaxWorkers ? [] : ['--maxWorkers=1']
for (let shard = 1; shard <= totalShards; shard++) {
  const code = runVitest([
    ...workerArgs,
    `--shard=${shard}/${totalShards}`,
    ...cliArgs,
  ])
  if (code !== 0) {
    process.exit(code)
  }
}
