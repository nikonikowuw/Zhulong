import { type RecordsTab } from '../data/schema'
import { type ExportOptions } from './export-zip-pipeline'

export interface ExportTaskDraft {
  taskId: string
  mode: 'selection' | 'batch'
  activeTab: RecordsTab
  totalCount: number
  completedKeys: string[]
  options: Partial<ExportOptions>
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

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === 'undefined') {
      reject(new Error('IndexedDB is not supported in this environment'))
      return
    }

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

    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

export const exportDb = {
  /**
   * 保存或更新导出任务草稿
   */
  async saveTaskDraft(draft: ExportTaskDraft): Promise<void> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_TASKS, 'readwrite')
      const store = tx.objectStore(STORE_TASKS)
      store.put({
        ...draft,
        updatedAt: Date.now(),
      })
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    })
  },

  /**
   * 获取指定 ID 的任务草稿
   */
  async getTaskDraft(taskId: string): Promise<ExportTaskDraft | null> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_TASKS, 'readonly')
      const store = tx.objectStore(STORE_TASKS)
      const request = store.get(taskId)
      request.onsuccess = () => resolve(request.result || null)
      request.onerror = () => reject(request.error)
    })
  },

  /**
   * 获取最近一个未完成的导出任务草稿
   */
  async getLatestPendingTask(): Promise<ExportTaskDraft | null> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_TASKS, 'readonly')
      const store = tx.objectStore(STORE_TASKS)
      const request = store.getAll()
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
  },

  /**
   * 保存已完成处理的单张图片产物
   */
  async saveArtifact(
    taskId: string,
    artifactKey: string,
    data: Uint8Array,
    mimeType = 'image/jpeg'
  ): Promise<void> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_ARTIFACTS, 'readwrite')
      const store = tx.objectStore(STORE_ARTIFACTS)
      const record: ExportArtifactRecord = {
        key: `${taskId}:${artifactKey}`,
        taskId,
        artifactKey,
        data,
        mimeType,
        createdAt: Date.now(),
      }
      store.put(record)
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    })
  },

  /**
   * 获取单张已缓存的图片产物
   */
  async getArtifact(
    taskId: string,
    artifactKey: string
  ): Promise<Uint8Array | null> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_ARTIFACTS, 'readonly')
      const store = tx.objectStore(STORE_ARTIFACTS)
      const request = store.get(`${taskId}:${artifactKey}`)
      request.onsuccess = () => {
        const record = request.result as ExportArtifactRecord | undefined
        resolve(record ? record.data : null)
      }
      request.onerror = () => reject(request.error)
    })
  },

  /**
   * 获取某个任务的所有已缓存产物
   */
  async getAllArtifacts(
    taskId: string
  ): Promise<{ artifactKey: string; data: Uint8Array }[]> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction(STORE_ARTIFACTS, 'readonly')
      const store = tx.objectStore(STORE_ARTIFACTS)
      const index = store.index('taskId')
      const request = index.getAll(taskId)
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
  },

  /**
   * 清除指定任务的草稿和全部产物（释放浏览器存储）
   */
  async clearTaskDraft(taskId: string): Promise<void> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction([STORE_TASKS, STORE_ARTIFACTS], 'readwrite')

      // 删除任务记录
      const taskStore = tx.objectStore(STORE_TASKS)
      taskStore.delete(taskId)

      // 删除该任务下的所有产物
      const artifactStore = tx.objectStore(STORE_ARTIFACTS)
      const index = artifactStore.index('taskId')
      const request = index.getAllKeys(taskId)

      request.onsuccess = () => {
        const keys = request.result
        for (const k of keys) {
          artifactStore.delete(k)
        }
      }

      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    })
  },

  /**
   * 清空所有任务缓存
   */
  async clearAllDrafts(): Promise<void> {
    const db = await openDatabase()
    return new Promise((resolve, reject) => {
      const tx = db.transaction([STORE_TASKS, STORE_ARTIFACTS], 'readwrite')
      tx.objectStore(STORE_TASKS).clear()
      tx.objectStore(STORE_ARTIFACTS).clear()
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    })
  },
}
