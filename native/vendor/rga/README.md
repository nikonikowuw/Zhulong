# Vendored Rockchip RGA (librga) prefix

RKNN 视觉前处理后端（任务 `10-13-algo-rga-vision-backend`）的**离线**供应商前缀。
内容从目标板 RK3568 直接取得，未做任何修改（`im2d_expand.h` 除外，见下）。

## 来源与版本

| 项 | 值 |
| --- | --- |
| 目标板 | RK3568 / Debian 11 (bullseye) arm64 / 内核 5.10.226 |
| 头文件来源 | `rk3568:/usr/include/rga/` |
| 库来源 | `rk3568:/usr/lib/aarch64-linux-gnu/librga.so.2.1.0` |
| 库版本字符串 | `rga_api version 1.10.1_[10]` |
| 头文件宏 `RGA_API_VERSION` | `1.10.1_[10]`（与库一致） |
| RGA 内核驱动 | v1.3.4 |
| `byte_stride_align`（真机实测） | **4** |

## SHA-256

```
5ee06891775451c56e6264f5d53db0a4e948e1830287f3a5db4f75c30ac0ff11  include/rga/im2d.h
f50214c9196cfdb1a66301d71d53cd335ed4bc8b8cdf5a56fdcce5965db2c6b9  include/rga/rga.h
ba8744397f126c87dc2d637ddea35cca562aa8f323631bb4b9883816b3dcb714  lib/librga.so.2.1.0
```

前两项由 `native/scripts/build.py` 的 `rga_prefix()` 强制校验（不匹配即构建失败）。

## 为什么只保留部分头文件

`im2d.h` 是伞头文件，实际引用链只有 9 个文件：

```txt
im2d.h → im2d_version.h, im2d_type.h, im2d_common.h, im2d_buffer.h,
         im2d_single.h, im2d_task.h, im2d_mpi.h, rga.h
```

其余头（`drmrga.h`、`GrallocOps.h`、`RgaApi.h`、`RockchipRga.h`、`RgaMutex.h`、
`RgaSingleton.h`、`RgaUtils.h`、`im2d.hpp`）不在链上，已删除以缩小 vendor 体积。

⚠️ `im2d_expand.h` **已删除**：它 `#include <ui/GraphicBuffer.h>` 与
`<android/hardware_buffer.h>`（Android 专用），在 Linux 交叉工具链下无法编译。
`im2d.h` 不引用它，删除不影响任何被使用的 API。

## 符号链

```
lib/librga.so     -> librga.so.2
lib/librga.so.2   -> librga.so.2.1.0
lib/librga.so.2.1.0
```

⚠️ 板端 `librga.so` 的 `SONAME` 是 **`librga.so.2`**（不是 `librga.so`），
因此链接后产物的 `NEEDED` 显示 `librga.so.2`。板端 `/usr/lib/aarch64-linux-gnu/`
已有完整符号链，运行期按 SONAME 解析即可。

## 使用约束

- 仅 `ZHULONG_VISION_BACKEND=rga` 的构建使用；**宿主 `Zhulong` 与 `ZhulongEngine` 绝不链接**。
- `rga/im2d.h` 只允许在 `native/src/backends/rga/` 内被引用。
- 头文件需要调用方**先**包含 `<cstddef>`（其内部用了 `NULL` 但未包含该头）。
- 运行期需 `/dev/rga` 可访问（`crw-rw---- root video` → 进程须属 **`video` 组**）。
- 本任务只用 `wrapbuffer_virtualaddr`（不接管内存所有权）；不引入 `importbuffer_fd`（DMA-BUF 属后续任务）。
