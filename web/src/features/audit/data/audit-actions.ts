/**
 * 审计动作清单（前端唯一来源）
 *
 * 后端 Go 侧用 `Action*` 常量决定写入什么 action 字符串，前端用这份清单决定怎么显示。
 * 两侧必须同步：新增后端动作时，在这里登记并补齐三份 locales 的 `actionNames` 文案。
 * 两道 CI 守卫拦住漏登记：
 * - `audit-actions.test.ts`：清单 ↔ 三语 locale 双向一致（不缺、不多、不空）；
 * - `internal/audit` 的 Go 测试：后端常量 ⊆ 三语 locale。
 *
 * 任何一环缺失都会让审计列表回退成裸 key（`t(..., { defaultValue: action })`）。
 */
export const AUDIT_ACTION_KEYS = [
  // 认证与会话
  'auth.init',
  'auth.login',
  'auth.logout',
  // 摄像机
  'camera.create',
  'camera.update',
  'camera.delete',
  'camera.toggle',
  // 分析任务
  'task.create',
  'task.update',
  'task.delete',
  // 算法包
  'algorithm.install',
  'algorithm.enable',
  'algorithm.disable',
  'algorithm.reset',
  'algorithm.delete',
  'algorithm.trial',
  'algorithm.task.create',
  'algorithm.task.delete',
  // 人员与人脸
  'person.create',
  'person.update',
  'person.delete',
  'person.face.add',
  'person.face.delete',
  'person.primary_face.set',
  'person.search_by_image',
  'person.import.preview',
  'person.import.commit',
  'person.face_import.create',
  // GB28181 接入
  'gb28181.config_update',
  'gb28181.device_upsert',
  'gb28181.device_delete',
  'gb28181.catalog_sync',
  'gb28181.channel_bind_camera',
  // 存储
  'storage.update_config',
  'storage.manual_cleanup',
  'storage.auto_cleanup',
  'storage.test_path',
  'storage.emergency_stop',
  'storage.emergency_resume',
  // 系统标识
  'system.identity.update',
  // 系统时间
  'time.update_config',
  'time.ntp_sync',
  'time.manual_set',
  'time.rtc_heal',
  'time.clock_step',
  'time.ntp_sync_failed',
  'time.ntp_sync_recovered',
  // 审计自身
  'audit.clear',
  // 机器间认证 (API Token)
  'api_token.create',
  'api_token.revoke',
  'api_token.auth_rejected',
  // 告警推送
  'alertpush.create',
  'alertpush.update',
  'alertpush.delete',
  'alertpush.enable',
  'alertpush.disable',
  'alertpush.test',
] as const

export type AuditActionKey = (typeof AUDIT_ACTION_KEYS)[number]
