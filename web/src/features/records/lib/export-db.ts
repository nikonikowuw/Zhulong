import { type RecordsTab } from '../data/schema'

/**
 * 允许落盘的导出参数。
 *
 * ⚠️ 这里只能出现可结构化克隆的原始值。`executeRecordsExport` 的入参里还有
 * `signal`（AbortSignal）、`onProgress` / `resolveCameraName`（函数）——
 * IndexedDB 的 `put()` 遇到它们会**同步**抛 `DataCloneError`，整条图片导出
 * 流水线会在第一张图之前就崩掉。类型上禁止比注释里写「别传函数」可靠。
 */
export interface ExportDraftOptions {
  exportCsv: boolean
  exportImages: boolean
  burnOverlay: boolean
  imageKind: 'scene' | 'closeup' | 'both'
  maxImages?: number
}

export interface ExportTaskDraft {
  taskId: string
  mode: 'selection' | 'batch'
  activeTab: RecordsTab
  /** 本次任务涉及的维度（用于续传时重建报表清单） */
  tabs: RecordsTab[]
  /** 参与导出的记录条数（报表行数，不是图片数） */
  recordCount: number
  /** 图片任务总数（已按 maxImages 截断后） */
  totalCount: number
  completedKeys: string[]
  options: ExportDraftOptions
  status: 'in_progress' | 'paused' | 'completed'
  createdAt: number
  updatedAt: number
}

export interface ExportArtifactRecord {
  key: string // 格式: `${taskId}:${artifactKey}`
  taskId: string
  artifactKey: string
  data: Uint8Array
  mimeType: string
  createdAt: number
}

const DB_NAME = 'zhulong_export_db'
const DB_VERSION = 1
const STORE_TASKS = 'tasks'
const STORE_ARTIFACTS = 'artifacts'

/**
 * 未完成草稿的保留时长。
 *
 * 断点续传只对「上次那几十分钟内」有意义；再久远的数据集大概率已经被清理，
 * 留着只是白占配额，而且 `getLatestPendingTask` 只会展示最新的一条 ——
 * 更早的草稿会变成用户既看不见、也清不掉的垃圾。
 */
const DRAFT_TTL_MS = 7 * 24 * 60 * 60 * 1000

// 每个操作都新开连接时，一轮 200 张图的导出会累积上千个 IDBDatabase；
// 连接还会阻塞其他标签页的 versionchange 升级。这里复用一个惰性连接。
let dbPromise: Promise<IDBDatabase> | null = null

function openDatabase(): Promise<IDBDatabase> {
  if (typeof indexedDB === 'undefined') {
    return Promise.reject(
      new Error('IndexedDB is not supported in this environment')
    )
  }
  if (dbPromise) {
    return dbPromise
  }

  dbPromise = new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION)

    request.onupgradeneeded = (event) => {
      const db = (event.target as IDBOpenDBRequest).result

      if (!db.objectStoreNames.contains(STORE_TASKS)) {
        db.createObjectStore(STORE_TASKS, { keyPath: 'taskId' })
      }

      if (!db.objectStoreNames.contains(STORE_ARTIFACTS)) {
        const artifactStore = db.createObjectStore(STORE_ARTIFACTS, {
          keyPath: 'key',
        })
        artifactStore.createIndex('taskId', 'taskId', { unique: false })
      }
    }

    request.onsuccess = () => {
      const db = request.result
      // 让出连接，否则别的标签页/新版本永远等不到 upgrade
      db.onversionchange = () => {
        db.close()
        dbPromise = null
      }
      db.onclose = () => {
        dbPromise = null
      }
      resolve(db)
    }
    request.onerror = () => {
      // 别把一次失败的 open 缓存成永久的 rejected promise
      dbPromise = null
      reject(request.error)
    }
    request.onblocked = () => {
      dbPromise = null
    }
  })

  return dbPromise
}

/** 保存或更新导出任务草稿 */
async function saveTaskDraft(draft: ExportTaskDraft): Promise<void> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_TASKS, 'readwrite')
    tx.objectStore(STORE_TASKS).put({
      ...draft,
      updatedAt: Date.now(),
    })
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error)
  })
}

/** 获取指定 ID 的任务草稿 */
async function getTaskDraft(taskId: string): Promise<ExportTaskDraft | null> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_TASKS, 'readonly')
    const request = tx.objectStore(STORE_TASKS).get(taskId)
    request.onsuccess = () =>
      resolve((request.result as ExportTaskDraft | undefined) ?? null)
    request.onerror = () => reject(request.error)
  })
}

/** 获取最近一个未完成的导出任务草稿 */
async function getLatestPendingTask(): Promise<ExportTaskDraft | null> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_TASKS, 'readonly')
    const request = tx.objectStore(STORE_TASKS).getAll()
    request.onsuccess = () => {
      const tasks = (request.result as ExportTaskDraft[]) || []
      const pendingTasks = tasks.filter(
        (t) => t.status === 'in_progress' || t.status === 'paused'
      )
      if (pendingTasks.length === 0) {
        resolve(null)
        return
      }
      // 按更新时间降序取最新一个
      pendingTasks.sort((a, b) => b.updatedAt - a.updatedAt)
      resolve(pendingTasks[0])
    }
    request.onerror = () => reject(request.error)
  })
}

/** 保存已完成处理的单张图片产物 */
async function saveArtifact(
  taskId: string,
  artifactKey: string,
  data: Uint8Array,
  mimeType = 'image/jpeg'
): Promise<void> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_ARTIFACTS, 'readwrite')
    const record: ExportArtifactRecord = {
      key: `${taskId}:${artifactKey}`,
      taskId,
      artifactKey,
      data,
      mimeType,
      createdAt: Date.now(),
    }
    tx.objectStore(STORE_ARTIFACTS).put(record)
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error)
  })
}

/** 获取单张已缓存的产物 */
async function getArtifact(
  taskId: string,
  artifactKey: string
): Promise<Uint8Array | null> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_ARTIFACTS, 'readonly')
    const request = tx
      .objectStore(STORE_ARTIFACTS)
      .get(`${taskId}:${artifactKey}`)
    request.onsuccess = () => {
      const record = request.result as ExportArtifactRecord | undefined
      resolve(record ? record.data : null)
    }
    request.onerror = () => reject(request.error)
  })
}

/** 获取某个任务的所有已缓存产物 */
async function getAllArtifacts(
  taskId: string
): Promise<{ artifactKey: string; data: Uint8Array }[]> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_ARTIFACTS, 'readonly')
    const request = tx
      .objectStore(STORE_ARTIFACTS)
      .index('taskId')
      .getAll(taskId)
    request.onsuccess = () => {
      const records = (request.result as ExportArtifactRecord[]) || []
      resolve(
        records.map((r) => ({
          artifactKey: r.artifactKey,
          data: r.data,
        }))
      )
    }
    request.onerror = () => reject(request.error)
  })
}

/** 清除指定任务的草稿和全部产物（释放浏览器存储） */
async function clearTaskDraft(taskId: string): Promise<void> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction([STORE_TASKS, STORE_ARTIFACTS], 'readwrite')

    tx.objectStore(STORE_TASKS).delete(taskId)

    const artifactStore = tx.objectStore(STORE_ARTIFACTS)
    const request = artifactStore.index('taskId').getAllKeys(taskId)
    request.onsuccess = () => {
      for (const k of request.result) {
        artifactStore.delete(k)
      }
    }

    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error)
  })
}

/**
 * 清掉超期的草稿与产物，返回清理数量。
 *
 * 在导出弹窗打开时调用即可 —— 不需要给 app 层加启动钩子，也不会在
 * 用户没进记录页时白白打开 IndexedDB。
 */
async function pruneExpiredTasks(maxAgeMs = DRAFT_TTL_MS): Promise<number> {
  const db = await openDatabase()
  const tasks = await new Promise<ExportTaskDraft[]>((resolve, reject) => {
    const tx = db.transaction(STORE_TASKS, 'readonly')
    const request = tx.objectStore(STORE_TASKS).getAll()
    request.onsuccess = () =>
      resolve((request.result as ExportTaskDraft[]) || [])
    request.onerror = () => reject(request.error)
  })

  const cutoff = Date.now() - maxAgeMs
  // 用 <= 而不是 <：maxAgeMs 为 0 时 cutoff 可能与 updatedAt 落在同一毫秒，
  // 严格小于会让「刚写入的也算过期」这个语义变得依赖时钟分辨率。
  // 已完成任务的产物保留 10s 宽限期，之后随过期清理一起回收。
  const completedCutoff = Date.now() - Math.min(maxAgeMs, 10_000)
  const stale = tasks.filter(
    (t) =>
      t.updatedAt <= cutoff ||
      (t.status === 'completed' && t.updatedAt <= completedCutoff)
  )
  await Promise.all(stale.map((task) => clearTaskDraft(task.taskId)))
  return stale.length
}

/** 清空所有任务缓存（测试与「重置」入口使用） */
async function clearAllDrafts(): Promise<void> {
  const db = await openDatabase()
  return new Promise((resolve, reject) => {
    const tx = db.transaction([STORE_TASKS, STORE_ARTIFACTS], 'readwrite')
    tx.objectStore(STORE_TASKS).clear()
    tx.objectStore(STORE_ARTIFACTS).clear()
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error)
  })
}

export const exportDb = {
  saveTaskDraft,
  getTaskDraft,
  getLatestPendingTask,
  saveArtifact,
  getArtifact,
  getAllArtifacts,
  clearTaskDraft,
  pruneExpiredTasks,
  clearAllDrafts,
}
