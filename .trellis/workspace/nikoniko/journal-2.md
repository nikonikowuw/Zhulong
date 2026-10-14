# Journal - nikoniko (Part 2)

> Continuation from `journal-1.md` (archived at ~2000 lines)
> Started: 2026-10-15

---



## Session 39: 支持人员主人脸设置、相册画廊与全屏预览 (10-14-person-primary-face)
<!-- trellis-session: v=2 fp=bd730f31af4d9256 -->

**Date**: 2026-10-15
**Task**: 支持人员主人脸设置、相册画廊与全屏预览 (10-14-person-primary-face)
**Package**: backend
**Branch**: `dev`

### Summary

完成人员主人脸端到端功能支持：新增数据库迁移、自动与手动设主、画廊卡片重构、全屏沉浸式照片预览、复选批量操作及国际化

### Main Changes

- 数据库与后端：新增 000008 迁移脚本，Person 增加 primary_face_id 显式字段，提供 PUT /persons/:id/primary-face 端点及首图自动设主/删除替补逻辑
- 相册画廊：重构 PersonsFacesDialog 为响应式正方形相册网格（Gallery Grid），主人脸高亮角标与悬浮工具栏，内嵌上传卡片与大图预览
- 全屏照片查看：新增 PersonsImagePreviewDialog 沉浸式 Lightbox 查看器，支持原生全屏切换、滚轮缩放与多级放大
- 多选与批量操作：增加独立人脸图列与复选框列，集成 DataTableBulkActions 与 PersonsMultiDeleteDialog 高危批量删除弹窗

### Git Commits

| Hash | Message |
|------|---------|
| `166be4a` | feat(person): support primary face management, gallery grid, and full-screen preview |

### Testing

- [OK] 运行 vitest 无头浏览器测试：28 个测试 100% 通过
- [OK] 运行 make go-check：gofmt / go vet / go test -race / native-engine bridge tests 100% 通过

### Status

[OK] **Completed**

### Next Steps

- 推进 10-14-face-recognition-e2e 剩余人脸特征提取、推理结果出口与前端叠加子任务
