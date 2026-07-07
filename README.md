# 物流雷达

本项目是一个本地运行的物流监控工具，用 Go 提供后端服务，将前端页面、接口服务和本地 SQLite 数据库整合到一个 Windows 可执行文件中。

## 快速运行

源码运行：

```bash
go run .
```

启动后打开：

```text
http://localhost:3456
```

如果已经编译好 exe，直接运行：

```bash
.\物流查询.exe
```

## 主要功能

- 批量导入运单号，支持手动粘贴、TXT、CSV、XLSX。
- 自动识别常见快递公司，也可以手动指定快递编码。
- 查询并保存物流轨迹、状态、当前城市、最新轨迹和更新时间。
- 支持批量同步、筛选同步、失败记录重试、失败单号复制。
- 支持状态统计、标签、备注、导出、操作日志和仪表盘。
- 数据保存在本地 SQLite，不依赖外部数据库。

## 配置

页面顶部可以配置运行参数，配置会写入本地数据库。

| 配置项 | 默认值 | 说明 |
|---|---:|---|
| `appKey` | `12574478` | 菜鸟接口使用的应用 key |
| `proxyApi` | 空 | 代理提取 API，留空则直连 |
| `timeout` | `3` | 单次请求超时时间，单位秒 |
| `concurrency` | `5` | 批量查询并发数 |
| `monitorLimit` | `500` | 默认监控数量上限 |
| `port` | `3456` | 本地服务端口 |

## 数据文件

运行后会在项目目录下创建：

```text
data/logistics.db
data/logistics.db-shm
data/logistics.db-wal
```

这些是本地运行数据，不提交到 Git。需要清空数据时，可以在页面里批量删除记录，或停止程序后直接处理 `data/logistics.db`。

## 编译 exe

普通编译：

```bash
go build -ldflags="-s -w" -o "物流查询.exe" .
```

如果本机安装了 UPX，可以进一步压缩体积：

```bash
upx --best --lzma "物流查询.exe"
```

`rsrc.syso` 是 Windows 资源文件，Go 编译时会自动链接进去，主要用于给 exe 带上图标等资源。它不是可执行程序，交付时只需要 `物流查询.exe`。

## 目录结构

```text
.
├── public/          # 前端静态页面，编译时嵌入 Go 程序
├── data/            # 本地 SQLite 数据库，运行时生成
├── scripts/         # 辅助脚本
├── server.go        # HTTP 服务、路由和中间件
├── handlers.go      # API handler
├── sse.go           # 批量导入/同步的 SSE 进度流
├── db.go            # SQLite schema、查询和写入逻辑
├── query.go         # 菜鸟物流接口请求和结果解析
├── carrier.go       # 快递公司自动识别规则
├── rsrc.syso        # Windows 资源文件
└── 物流查询.exe      # 编译产物，本地生成
```

## API 概览

| 路径 | 方法 | 说明 |
|---|---|---|
| `/api/settings` | `GET` / `PUT` | 读取或更新配置 |
| `/api/query` | `POST` | 查询单个运单号 |
| `/api/import` | `POST` | 批量导入并通过 SSE 返回进度 |
| `/api/records` | `GET` / `DELETE` | 查询记录或批量删除 |
| `/api/records/{id}` | `GET` | 查询单条记录 |
| `/api/records/{id}/remarks` | `PUT` | 更新备注 |
| `/api/sync` | `POST` | 同步指定记录 |
| `/api/sync/filter` | `POST` | 按筛选条件同步 |
| `/api/sync/monitoring` | `POST` | 同步全部监控记录 |
| `/api/export` | `GET` | 导出当前筛选结果 |
| `/api/stats` | `GET` | 状态统计 |
| `/api/logs` | `GET` | 操作日志 |

## 批量导入一致性

批量导入的成功数以“查询成功并且写入数据库成功”为准。SQLite 使用单连接写入，避免并发导入时出现 `database is locked` 后仍被前端统计为成功。

如果导入结果显示失败，页面会列出失败单号和错误信息，可以复制失败项后重试。

## 常见问题

### 端口被占用

如果启动时报：

```text
listen tcp 127.0.0.1:3456: bind: Only one usage of each socket address...
```

说明 `3456` 已被占用。关闭已有实例，或在页面配置里修改端口后重启。

### exe 为什么比普通脚本大

`物流查询.exe` 包含 Go 后端、嵌入的前端静态文件、SQLite 驱动和 Windows 资源。使用 `-ldflags="-s -w"` 和 UPX 可以显著减小体积。

### Chart.js 加载失败

仪表盘依赖页面中的 CDN：

```text
https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js
```

如果离线环境无法访问 CDN，普通查询和导入功能仍可用，但仪表盘图表可能无法显示。

## 开发验证

修改代码后先运行测试：

```bash
go test ./...
```

再启动服务做冒烟验证：

```bash
go run .
```
