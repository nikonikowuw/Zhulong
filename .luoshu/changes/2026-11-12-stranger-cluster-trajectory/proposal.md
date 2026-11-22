# 陌生人疑似人员档案与跨摄像头轨迹

来源：`.trellis/tasks/11-12-stranger-cluster-trajectory/task.json` 与其 `prd.md`，原文见 [source-prd.md](./source-prd.md)。原状态 `planning`，优先级 `P2`，创建日期 `2026-11-12`，创建人及经办人 `nikoniko`。本次仅登记规划，未启动实现。

## 背景与目标

将反复出现且质量达标的未匹配识别记录关联为可人工纠错的疑似档案，提供轨迹和证据。簇编号不是身份。

## In Scope

v1 的数据、在线与周期归档、轨迹查询、纠错及人工转正、最小 UI；详见原 PRD R1–R11。

## Out of Scope

不做自动确认身份，不纳入原文 v2 的自适应阈值、图搜簇、人体 ReID 或 ANN。

## 影响范围

记录、SQLite、空间轨迹、人员底库、Web 与 API 文档白名单。
