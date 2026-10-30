/**
 * 记录域的媒体 URL 构造入口。
 *
 * 实现已上移到 `@/lib/media-url`：底图、轨迹快照等跨特性消费方需要同一套规则，
 * 不应穿透导入本目录。这里保留同名导出，既有调用方与共置测试无需改动。
 */
export { getGalleryImageUrl, getMediaUrl } from '@/lib/media-url'
