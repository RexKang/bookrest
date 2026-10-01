# 📚 Bookrest

> Your books, back on the shelf. 把散落的书，摆回书架上。

本地优先的数字书架：**不导入、不搬动、不接管你的文件** —— 只读取你已有的目录，把散落在硬盘各处的电子书与漫画，渲染成一面可以自由摆放的书架墙。

![Status](https://img.shields.io/badge/Status-设计阶段-orange)
![Platform](https://img.shields.io/badge/Platform-Desktop-blue)
![License](https://img.shields.io/badge/License-MIT-green)

## 它是什么

- **零导入**：直接扫描你已有的目录，书原地不动，一个字节都不写
- **只读优先**：摆放、标签等用户数据写在库内隐藏目录与本机镜像中，绝不修改你的书
- **书架视图**：书脊（封面裁条）与封面墙两种视图，书架与顺序可自由拖拽
- **离线可用**：库离线（移动硬盘拔掉、NAS 未挂载）时仍可浏览书架，自动降级为只读
- **本地优先**：无账号、无云服务、无遥测

## 它不是什么

- 不是阅读器：双击用系统默认程序打开文件
- 不是电子书服务器：不提供 OPDS，不做多用户
- 不是元数据编辑器：不写回你的文件

## 运行（Windows）

拾书同时提供两种访问方式，**同一份内核与前端**：

| 模式 | 启动方式 | 说明 |
|---|---|---|
| C/S（默认） | `bookrest.exe` | 原生桌面窗口（Wails v2 + WebView2） |
| B/S | `bookrest.exe --serve [--addr 127.0.0.1:8788]` | 起本地服务，浏览器访问同一套界面 |

桌面窗口里也可以一键切换：**设置（右上齿轮）→ 在浏览器中打开**。

### 访问安全（本地软件优先）

| 约束 | 行为 |
|---|---|
| 默认绑定 | 只绑 `127.0.0.1`（本机回环），局域网/公网设备无法连接 |
| 对外暴露 | 绑定非本机地址必须显式加 `--allow-remote`，否则**拒绝启动**；启动后打印风险提示 |
| 访问令牌 | 每次启动随机生成（128 位），打印在访问地址里；首次进入转存为 `HttpOnly` + `SameSite=Strict` Cookie，未带令牌一律 401 |
| Host 校验 | 只接受本机 Host，阻断 DNS rebinding |
| 跨站防护 | 不发送任何 CORS 头；`SameSite=Strict` 阻断 CSRF |
| 无 TLS | 服务本身不加密，仅适合本机/可信网络，用完请关闭 |

```bash
# 方式一：直接运行已构建的桌面应用
cmd/bookrest/build/bin/bookrest.exe

# 方式二：自己构建（需要 Go 1.25+ 与 Wails v2 CLI）
go install github.com/wailsapp/wails/v2/cmd/wails@latest
cd cmd/bookrest && wails build        # 产物：build/bin/bookrest.exe

# 方式三：开发模式——无桌面壳，浏览器里跑同一套前端（HTTP 传输）
go run ./cmd/bookrest-cli serve --root <你的书库目录> --addr 127.0.0.1:8899
```

命令行工具（`cmd/bookrest-cli`，开发与排障用）：

```bash
go run ./cmd/bookrest-cli scan   --root <库根>   # 扫描并建立本机镜像
go run ./cmd/bookrest-cli shelf  --root <库根>   # 终端书架视图（ANSI 书脊）
go run ./cmd/bookrest-cli report --root <库根>   # 只读体检报告（缺卷/重复/损坏/无封面/命名）
```

## 状态

**v0.1.0-m2**：内核与界面完整可用。

- 已实现：零导入扫描（目录剪枝 + 指纹身份 + 移动认领 + 精确重复）、CBZ/EPUB 只读解析、
  本机镜像与库内快照（原子写 + 滚动备份 + 冲突不静默）、书架视图（架层 + 书脊 + 拖拽）、
  封面墙、详情页（标签/评分/在架定位/系统打开）、体检报告（含 CSV 导出）、设置、离线只读、
  **全盘找书**（选盘符 / 屏蔽常见目录 / 选文件类型 → 候选库列表一键添加）、**B/S 模式**（`--serve`）、
  **封面与信息补全**（详情页里按需：粘贴图片 / 选本地图片 / 手动改字段 / 联网搜索候选并挑选）。
- 未做：CBR(rar) 解析、自由 2D 画布摆放、在线元数据补全、非 Windows 打包。

## License

MIT — 见 [LICENSE](LICENSE)。
