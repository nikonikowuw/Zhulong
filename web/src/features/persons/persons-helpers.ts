import { type Person } from './data/schema'

/**
 * 单张人脸图片上限，与后端 `MaxFaceImageBytes`（10 MiB）对齐。
 *
 * 三个上传入口（新建档案的首张底库、抽屉里的批量上传）共用一个常量：曾经
 * 两处各写一份 `10 * 1024 * 1024`，后端一改就只剩一处是对的。
 */
const MAX_FACE_IMAGE_BYTES = 10 * 1024 * 1024

/** 后端允许的图片 MIME。文件选择框的 `accept` 与拖拽校验共用这一份。 */
const ALLOWED_FACE_IMAGE_TYPES = [
  'image/jpeg',
  'image/png',
  'image/webp',
] as const

/** `accept` 属性值。 */
export const FACE_IMAGE_ACCEPT = ALLOWED_FACE_IMAGE_TYPES.join(',')

/** 校验单张人脸图片；通过返回 `null`，否则返回稳定的错误类别。 */
export function validateFaceImageFile(
  file: File
): 'invalidType' | 'tooLarge' | null {
  if (!(ALLOWED_FACE_IMAGE_TYPES as readonly string[]).includes(file.type)) {
    return 'invalidType'
  }
  if (file.size > MAX_FACE_IMAGE_BYTES) {
    return 'tooLarge'
  }
  return null
}

/** 主人脸 ID：显式 `primaryFaceId` 优先，其次第一张，都没有则 `undefined`。 */
export function primaryFaceIdOf(person: Person): string | undefined {
  return person.primaryFaceId || person.faces?.[0]?.id || undefined
}

/** 头像兜底字符：取姓名首字符大写，空名回落 `?`。 */
export function initialOf(name: string | null | undefined): string {
  return name ? name.trim().charAt(0).toUpperCase() || '?' : '?'
}
