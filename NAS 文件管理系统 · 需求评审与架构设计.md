# EasyCloud · 需求评审与架构设计

Oct 23, 2026 · @名字

## 0. 方案摘要

- **后端选 Go**：NAS 场景是 IO 密集，Go 在交叉编译单二进制、并发上传、子进程插件管理、迭代速度上更合适。
- **不做虚拟路径映射**：界面与 API 中的路径就是数据根（容器内 `/data` ⇄ 宿主 `/mnt/ssd/nasData`）下的真实相对路径。管理员看到的结构与宿主完全一致，普通用户只看到被授权的真实分支。
- **路径安全集中在 PathGuard**：只解码一次、分段校验、按分段边界授权、`os.Root` 文件系统层兜底，四层纵深防御（第 7.5 节）。
- **数据库默认 SQLite**：配置 MariaDB 后启用 MariaDB；命令行安装时连接失败提供「重试 / 改用 SQLite / 退出」三选一；服务运行期（含 docker）不回退，失败只写日志。首启兼容库中已有数据。
- **通信按场景选择 HTTP 或 WebSocket**：按场景选择，推送与高频交互用 WebSocket，文件传输与外部集成用 HTTP（第 3.5 节）。
- **监控历史用内嵌 LSM 键值引擎 Pebble**：封装在接口后，保留替换余地（第 3.7 节）。
- **上传改为分片续传（tus）**：摆脱反向代理请求体限制，总上限三级配置。

## 1. 需求评审：需调整项与替代方案

原需求有 10 处存在逻辑漏洞、过晚决策或更优替代，按影响程度排序：

| # | 原需求 | 问题 | 调整建议 |
| --- | --- | --- | --- |
| 1 | V1 不需要登录 | 文件管理服务一旦暴露到局域网/公网即可被任意读写删除，属安全缺陷 | V1 内置单管理员口令（env 设定，首启生成并打印）+ 会话 Cookie；V2 再迁移为完整用户体系 |
| 2 | V1 不做用户，V2 接入「用户目录 + 共享子目录」 | 若 V1 的文件 API 直接使用物理路径，V2 需重写全部文件接口 | V1 即引入 PathGuard：API 只接受相对数据根的真实路径，统一校验与授权，不做虚拟路径映射；V1 只有一条管理员全量授权 |
| 3 | Docker 打包放在 V5 | 数据目录、配置注入、ffmpeg 依赖、权限 UID 等都受部署形态影响，晚做会返工 | V1 即提供 Dockerfile + GitHub Actions 多架构构建；V5 只做打磨（安装脚本、升级迁移、文档） |
| 4 | 支持 SMB/NFS 供 Jellyfin 使用 | Go 内自研 SMB 服务端工程量巨大且协议兼容风险高；且 SMB 账户与系统账户是两套体系 | Jellyfin 与本系统挂载同一宿主目录（推荐）；远程需求用「Samba 网关插件」托管 Samba 容器并同步账户；内置 WebDAV 作为轻量通用协议 |
| 5 | 插件可单独升级（onlyoffice、图片查看、视频播放、ffmpeg） | 四者形态完全不同：OnlyOffice 是独立服务、ffmpeg 是二进制、查看器是前端组件。Go 原生 plugin 包要求版本完全一致，无法单独升级 | 定义三类插件：前端查看器、后端子进程扩展、外部服务连接器；统一 manifest + 签名包 + 版本兼容声明（见第 3 节） |
| 6 | 目录结构与本地一致 | 正确方向，但缩略图、分享、回收站等元数据若以路径为键，宿主机上直接改名/移动会导致悬挂引用 | 数据库建「文件索引」表，记录路径 + inode/设备号；fsnotify 实时监听 + 定时对账扫描修复 |
| 7 | 回收站 | 若统一移动到一个回收站目录，跨磁盘/跨挂载会变成复制+删除，大文件极慢且非原子 | 数据根下保留隐藏目录 `.easycloud/trash/<uid>/`（同文件系统 rename 原子完成），列表中过滤且禁止通过 API 访问；数据库记录原路径 |
| 8 | 上传大小可配置 | 仅改配置无法绕过用户自建反向代理的 body 限制 | 分片上传（默认 16 MB/片，可配置）；总大小上限三级配置；超限错误返回可跳转的设置链接 |
| 9 | 瀑布流布局 | 大目录（万级图片）直接渲染会卡死；图片未知宽高会导致布局跳动 | 虚拟滚动瀑布流；后端生成缩略图时把宽高写入索引，前端预先占位 |
| 10 | 「符合当前项目名称」 | 需求中未给出项目名称 | 已确认：项目名为 `easycloud` |

另有两项需确认：

- **数据库**：默认 SQLite，配置 MariaDB 后切换，详见第 3.6 节。
- **监控历史**：使用内嵌时序引擎，详见第 3.7 节。
- **已确认：保留目录 `.easycloud`**。它会出现在宿主数据目录中（`ls -a` 可见），是「结构与宿主一致」原则下唯一的例外。它承载回收站与上传分片，必须与用户文件在同一文件系统内，才能保证 rename 原子完成。数据库、缩略图缓存、监控数据放在应用数据目录（`/config`），不进入数据根。

## 2. 需求解构与约束

系统划分为 6 个业务域，其中「存储域」是全系统核心，其余域都通过它访问文件。

| 业务域 | 关键实体 | 引入版本 |
| --- | --- | --- |
| 存储域（VFS） | Volume（卷）、MountPoint（挂载点）、Scope（作用域）、FileNode（索引）、TrashItem | V1 |
| 传输域 | UploadSession（分片会话）、Task（复制/移动/解压等后台任务） | V1 |
| 身份域 | User、Group（部门）、Session、Grant（目录授权） | V2 |
| 监控域 | MetricSample、StorageUsage | V2 |
| 插件域 | Plugin、PluginManifest、PluginInstance | V3 |
| 分享域 | Share（链接、口令、过期、次数）、ProtocolGateway（WebDAV/Samba） | V4 |

**路径与权限模型（V2）**：不做虚拟路径映射。所有界面与 API 中的路径都是数据根下的真实相对路径。管理员看到的结构与宿主目录完全一致；用户 A 只看到被授权目录及其祖先目录，祖先目录中只列出通往授权目录的分支。

```
宿主 /mnt/ssd/nasData  ⇄  容器 /data  ⇄  Dashboard 根 /

用户 A 在 Dashboard 中看到：
/                    仅导航
├── user/            仅导航（看不到 B）
│   └── A/           rw  个人空间，创建用户时自动授予
└── share/           仅导航（看不到 BB 等其他目录）
    └── AA/          rw  管理员手动授予
```

授权主体可以是用户或部门。匹配按路径分段边界进行：授予 `/share/AA` 不会命中 `/share/AAB`。多条授权取并集。

**非功能约束（均为假定，需确认）**：

- 并发：假定 ≤ 50 在线用户、≤ 20 并发上传，单节点部署。
- 数据规模：假定单卷 ≤ 200 万文件，单目录 ≤ 5 万项。
- 延迟：假定目录列表 P95 < 200 ms（索引命中），缩略图首屏 < 1 s。
- 文件大小：假定单文件最大 1 TB（受文件系统限制，非系统限制）。
- 安全：路径穿越与符号链接逃逸必须在 VFS 层统一拦截；分享链接使用不可枚举的随机 ID（≥ 128 bit）。
- 硬件：假定需支持 amd64 + arm64（覆盖 x86 NAS 与树莓派/ARM NAS）。

## 3. 架构与选型

### 3.1 Go 还是 Rust：选 Go

| 维度 | Go | Rust | 对本项目的影响 |
| --- | --- | --- | --- |
| 性能瓶颈 | 磁盘/网络 IO，GC 影响可忽略 | 更低内存、无 GC | NAS 瓶颈在磁盘，Rust 优势无法兑现 |
| 单二进制分发 | `CGO_ENABLED=0` 交叉编译，一行命令出 amd64/arm64 | 可行，但 musl 交叉编译与依赖配置成本更高 | 直接影响 V5 的安装脚本复杂度 |
| 生态 | tusd（分片上传）、x/net/webdav、gopsutil、fsnotify 均成熟 | tus 与 WebDAV 服务端生态较弱 | Go 可直接复用，Rust 需自研 |
| 插件子进程 | hashicorp/go-plugin（gRPC）成熟 | 需自建 | V3 插件体系 |
| 开发效率 | 编译快、上手快 | 生命周期与 async 学习成本高 | 五个版本的迭代节奏 |

### 3.2 分层：Clean Architecture 的模块化单体

依赖方向只向内：`transport → app（用例） → domain`，`infra` 实现 `domain` 中声明的接口。选择单体而非微服务的原因：NAS 是单机部署，微服务只增加运维负担；按领域拆分 module 后，未来若需要拆出（如缩略图服务）只需替换接口实现。

### 3.3 技术选型

| 类别 | 选型 | 依据 |
| --- | --- | --- |
| HTTP 路由 | chi | 兼容标准 `net/http`，流式下载/Range 请求无封装损耗 |
| 实时推送 | WebSocket（coder/websocket），单连接多主题 | 见 3.5 节 |
| 数据库 | SQLite（默认，modernc.org/sqlite 纯 Go）/ MariaDB 10.11+（配置后启用） | 零依赖开箱；已有实例或规模大时切换。见 3.6 节 |
| 数据访问 | sqlc（sqlite、mysql 两套查询）+ goose（两套迁移） | SQL 显式可审计、编译期类型安全；迁移 embed 进二进制，启动自动执行 |
| 监控时序 | Pebble（内嵌 LSM 键值引擎） | 见 3.7 节 |
| 路径安全 | 自研 pathguard + 标准库 `os.Root` | 见 7.5 节 |
| 分片上传 | tusd v2 | 标准协议、断点续传、前端有 tus-js-client；钩子用于配额与权限校验 |
| 配置 | koanf | 分层合并：默认值 < yaml < .env/环境变量 < 数据库（Dashboard） |
| 日志 | log/slog（JSON） | 标准库，零依赖 |
| 系统监控采集 | gopsutil | 跨平台 CPU/内存/磁盘采集 |
| 文件监听 | fsnotify + 定时对账扫描 | 捕获宿主机直接修改 |
| 缩略图 | disintegration/imaging（纯 Go）；HEIC/RAW/视频帧交给 ffmpeg 插件 | 保持核心二进制无 CGO |
| 消息队列 | 不引入；进程内任务队列（状态持久化到数据库） | 单机场景，MQ 只增加部署组件 |
| 缓存 | 不引入 Redis；进程内 LRU（ristretto） | 同上 |
| 前端 | Vue 3 + Vite + TypeScript + Tailwind CSS + Pinia + Vue Router | 需求指定 |
| 前端组件 | Reka UI（无样式无障碍组件）+ 自定义 Tailwind 设计令牌 | 避免组件库默认外观，保证风格统一 |
| 动画 | Vue Transition + @vueuse/motion | 页面切换、列表增删、上传进度 |
| 图表 | ECharts（按需引入） | 仪表盘时序与占比图 |
| 虚拟滚动/瀑布流 | @tanstack/vue-virtual + 自研列分配 | 万级条目流畅；依赖后端返回宽高 |
| 前端交付 | `embed.FS` 打包进二进制 | 二进制部署无需额外静态服务 |

### 3.4 插件体系（V3，V1 预留扩展点）

| 插件类型 | 示例 | 运行形态 | 独立升级方式 |
| --- | --- | --- | --- |
| 前端查看器 | 图片查看器、视频播放器、Markdown 预览 | ES Module 包，按 MIME 注册，动态 `import()` 加载 | 替换 `plugins/<id>/<version>/` 下静态资源 |
| 后端扩展 | ffmpeg 转码/缩略图、Samba 网关 | 子进程，通过 Unix Socket 上的 gRPC 通讯（go-plugin） | 下载新版本包，热重启子进程 |
| 外部服务连接器 | OnlyOffice Document Server | 仅配置外部服务地址与 JWT 密钥，本系统提供回调接口 | 连接器随插件包升级；OnlyOffice 本体由用户自行升级 |

插件包统一结构：`manifest.json`（id、版本、类型、`engines.easycloud` 兼容范围、所需权限）+ 内容 + 签名。插件仓库默认指向 GitHub Releases，可配置私有源。

### 3.5 通信协议：按场景选择 HTTP 或 WebSocket

原则：请求-响应与文件传输用 HTTP；需要服务端主动推送，或高频双向交互的场景用 WebSocket。下表是逐场景结论，新增功能按同一原则判断。

| 场景 | 协议 | 原因 |
| --- | --- | --- |
| 文件下载、视频播放、图片显示 | HTTP | 浏览器 `<video>`/`<img>`/下载管理器只认 URL；拖动进度依赖 Range 请求 |
| 分享下载页 | HTTP | 外部访客、aria2/wget 等下载工具只支持 HTTP |
| 上传 | HTTP（tus） | 用 WebSocket 上传需自研分片确认、重传、续传，等于重造 tus |
| OnlyOffice 回调、WebDAV、Jellyfin | HTTP | 外部系统只使用 HTTP |
| 列表、重命名、移动等命令 | HTTP | 天然的请求-响应、状态码、浏览器缓存与反代日志；WebSocket 需自研请求关联、超时与错误码 |
| 上传/任务进度、目录变更、监控实时曲线、通知 | WebSocket | 服务端主动推送，多人在同一目录时实时刷新 |

WebSocket 设计要点：

- 每个标签页一条连接，按主题订阅：`dir:share/AA`、`task:<id>`、`metrics`。
- 握手时校验会话 Cookie 与 `Origin`，防跨站 WebSocket 劫持；订阅目录主题同样经过 PathGuard 授权。
- 断线指数退避重连，重连后前端主动拉取一次当前状态，避免漏掉断线期间的事件。

### 3.6 数据库：选择、连接失败与已有数据兼容

数据库选择只有一条规则：用配置指定的数据库；未配置任何 MariaDB 信息时用 SQLite。回退只发生在命令行安装阶段，服务运行期从不回退。

| 阶段 | 入口 | 连接失败时 |
| --- | --- | --- |
| 命令行安装 | `install.sh` 调用 `easycloud install`（交互式） | 三选一：重试 / 改用 SQLite / 退出 |
| 服务运行（systemd） | `easycloud serve` | 指数退避重试，直到 `EASYCLOUD_DB_CONNECT_TIMEOUT`（默认 60 秒），然后写日志退出 |
| 服务运行（docker） | `easycloud serve` | 同上；不回退，以 compose 中的配置为准 |

命令行安装流程：

1. 读取 `.env`；未配置 MariaDB → 使用 SQLite，直接进入第 4 步。
2. 已配置 MariaDB → 测试连接，同时检查账号权限与版本。
3. 失败 → 显示具体原因（地址不可达 / 认证失败 / 库不存在 / 版本过低），提示三选一：
   1. **重试**：用户修改 `.env` 后回车，重新读取再连接。
   2. **改用 SQLite**：二次确认后把 `EASYCLOUD_DB_DRIVER=sqlite` 写回 `.env`。
   3. **退出**：不安装 systemd 单元。
4. 成功 → 执行「已有数据兼容检查」（见下文），再安装 systemd 单元。

**服务运行期的日志与退出码**：连接失败时 HTTP 服务尚未启动、后台无法访问，日志是唯一出口。因此每条失败日志都写明原因、目标地址（隐去密码）与修复方法。退出码分两类，供进程管理器决策：

- **69（服务不可用）**：地址不可达、超时。systemd 与 docker 的重启策略可继续重启，等待数据库恢复。
- **78（配置错误）**：认证失败、库不存在、版本不兼容。systemd 单元配置 `RestartPreventExitStatus=78`，不再重启，避免日志刷屏。
- compose 中给 MariaDB 加 `healthcheck`，easycloud 服务使用 `depends_on: condition: service_healthy`，避免数据库启动慢于应用导致首启失败。

**已有数据兼容**：首次启动与每次升级都执行同一套检查，原则是「能识别的就迁移，不能确定的就拒绝启动，绝不覆盖」。

| 情况 | 识别方式 | 处理 |
| --- | --- | --- |
| 空库或新 SQLite 文件 | 无版本表、无 `easycloud_` 前缀表 | 执行全部迁移 |
| 本系统旧版本的数据 | 版本表中的版本低于当前程序 | 迁移前备份（SQLite 自动复制文件；MariaDB 在日志中提示先用 mysqldump 备份），再向上迁移 |
| 本系统更新版本的数据（降级） | 版本高于当前程序 | 拒绝启动，退出码 78，提示升级程序；不做降级迁移 |
| 库中有其他系统的表 | 存在非 `easycloud_` 前缀的表 | 所有表名带 `easycloud_` 前缀，共存且绝不触碰；日志建议使用独立库 |
| 存在 `easycloud_` 表但版本表缺失或损坏 | 版本表不可读 | 拒绝启动，退出码 78，提示人工检查 |
| 数据库与数据根不匹配（如把库指向另一台 NAS 的数据目录） | `.easycloud/instance.json` 中的实例 ID 与库中记录不一致 | 拒绝启动；用户执行 `easycloud rebind` 确认后重建文件索引，保留用户、授权、分享 |
| SQLite 文件损坏或不是 SQLite | 打开或 `integrity_check` 失败 | 拒绝启动，不覆盖原文件 |
| MariaDB 版本过低 | `SELECT VERSION()` | 低于 10.11 拒绝启动 |

迁移编写约束：

- 只做向前兼容的增量变更：先加列、后删列，跨两个版本完成，保证旧数据在新程序下可读。
- MariaDB 的 DDL 会隐式提交、无法回滚，因此每个迁移只含一个 DDL，且可重复执行。
- 字符集与排序规则在列级显式指定，不依赖库默认值。路径列统一使用 `utf8mb4_bin`：Linux 文件名区分大小写，默认的不区分大小写排序规则会让 `A.txt` 与 `a.txt` 在唯一索引上冲突。
- SQLite 运行参数：WAL 模式、`busy_timeout`、单写连接 + 多读连接。

### 3.7 监控历史：内嵌 LSM 键值引擎

可行，推荐 Pebble。RocksDB 本身是 C++ 库，Go 只能通过 CGO 调用（如 grocksdb），会破坏纯 Go 单二进制与交叉编译，因此在 Go 生态中选同类 LSM 引擎：

| 引擎 | 结构 | 纯 Go | 维护情况 | 结论 |
| --- | --- | --- | --- | --- |
| Pebble | LSM，借鉴 RocksDB 设计 | 是 | CockroachDB 的存储引擎，持续维护 | 推荐 |
| BadgerDB | LSM + 值日志分离 | 是 | 维护中 | 可选；值日志 GC 需额外调度，小值场景无优势 |
| goleveldb | LSM，LevelDB 的 Go 移植 | 是 | 长期缺乏维护 | 不推荐 |
| RocksDB（grocksdb） | LSM | 否（CGO） | 活跃 | 排除：破坏单二进制 |
| bbolt | B+ 树 | 是 | etcd 维护 | 不推荐：随机写放大高，不适合高频追加 |

键值设计：

```
key   = 指标 ID（1 字节）| 粒度（1 字节：raw / 1m）| unix 秒（8 字节，大端）
value = float64（8 字节），或聚合结构 {min, max, avg, count}
```

大端时间戳让同一指标按时间有序存放，范围查询就是一次前缀扫描；过期数据用 `DeleteRange` 整段删除，由 compaction 回收空间。

- 封装 `MetricStore` 接口（追加、范围查询、过期清理），引擎可替换。
- 降采样自行实现：原始 5 秒粒度保留 7 天，1 分钟聚合保留 90 天（假定，可配置）。
- 规模估算：约 10 个指标 × 每 5 秒一点 × 7 天 ≈ 121 万点，每点约 18 字节，未压缩约 22 MB。量级很小，选型重点是可靠与可维护，而非吞吐。
- 同一个 Pebble 实例还可承载其他本地 KV 需求（如缩略图缓存索引），无需再引入一种存储。
- 数据目录位于 `/config/metrics`，不进入数据根。

## 4. 目录结构

单仓库（monorepo），后端按领域纵向切分，每个领域内部再按层横向切分；`domain` 不依赖任何外部包。

```
easycloud/
├── cmd/
│   └── easycloud/main.go            # 入口：serve 启动服务；install 交互式安装（数据库三选一只在这里）
├── internal/
│   ├── config/                  # 配置加载与分层合并（默认值/yaml/env/数据库覆盖）
│   ├── platform/                # 横切能力：日志、错误码、事件总线、HTTP 中间件
│   ├── pathguard/               # 路径解析校验、授权判定、os.Root 句柄（全系统唯一入口）
│   ├── database/                # SQLite/MariaDB 连接、已有数据兼容检查、两种方言的仓储实现
│   ├── realtime/                # WebSocket Hub：主题订阅、授权、推送
│   ├── storage/                 # 存储域（V1 核心）
│   │   ├── domain/              #   Entity + Repository 接口，零外部依赖
│   │   ├── app/                 #   用例：列表、上传完成、移动、删除到回收站、恢复
│   │   ├── infra/               #   本地文件操作、fsnotify 索引器
│   │   └── transport/           #   HTTP handler、DTO、路由注册
│   ├── transfer/                # 分片上传（tusd 适配）、后台任务队列
│   ├── identity/                # 用户、部门、会话、目录授权（V2）
│   ├── monitor/                 # 指标采集、MetricStore（Pebble 实现）、降采样（V2）
│   ├── plugin/                  # 插件注册表、包管理、子进程宿主（V3）
│   ├── share/                   # 分享链接、下载页、WebDAV/Samba 网关（V4）
│   └── settings/                # 可在 Dashboard 覆盖的运行时设置
├── migrations/                  # goose SQL 迁移（sqlite/、mysql/ 两套），embed 进二进制
├── queries/                     # sqlc 的 SQL 源文件（按方言分目录）
├── pkg/pluginsdk/               # 对外公开的插件 SDK（唯一允许外部依赖的 Go 包）
├── web/                         # 前端（Vue 3）
│   ├── src/
│   │   ├── api/                 #   接口客户端，统一处理错误中的跳转动作
│   │   ├── components/          #   通用组件（基于 Reka UI + Tailwind）
│   │   ├── features/            #   按领域：files/ trash/ upload/ admin/ dashboard/ share/
│   │   ├── plugins/             #   前端插件加载器与 MIME 注册表
│   │   ├── stores/              #   Pinia
│   │   └── styles/              #   设计令牌（颜色、圆角、动效时长）
│   └── vite.config.ts
├── deploy/
│   ├── docker/Dockerfile        # 多阶段构建：web → go → distroless/alpine
│   ├── compose/                 # compose.yml（SQLite）、compose.mariadb.yml
│   ├── install.sh               # 二进制安装：架构检测、systemd 单元、配置模板
│   └── easycloud.example.yaml
└── .github/workflows/           # CI：测试、多架构镜像、Release 二进制
```

**边界约定**：领域之间只通过 `app` 层暴露的接口调用（如 `share` 依赖 `storage/app.FileReader`），禁止跨领域引用 `infra`；所有文件访问必须经过 pathguard 返回的句柄，其他领域不得直接调用 `os` 包操作用户数据。

## 5. 领域模型与接口

核心设计是把「谁能访问哪条真实路径」（PathGuard）与「文件怎么读写」（`os.Root`）分开。路径始终是数据根下的真实相对路径，不存在任何映射。

### 5.1 核心实体

| 实体 | 关键字段 | 说明 |
| --- | --- | --- |
| DataRoot | path | 数据根，如容器内 `/data`；其下保留目录 `.easycloud` 承载回收站与上传分片 |
| RelPath | segments | 值对象：已校验的真实相对路径，只能由 `pathguard.Parse` 构造 |
| Grant | subject\_type, subject\_id, path, perm | 授权；创建用户时自动生成 `user/<name>` 的 rw 授权，共享目录默认无 |
| FileNode | id, rel\_path, inode, dev, size, mtime, mime, width, height | 文件索引；inode+dev 用于识别宿主机侧改名 |
| TrashItem | id, owner\_id, origin\_path, trash\_path, deleted\_at, expire\_at | 回收站记录，到期由后台任务清理 |
| UploadSession | id, owner\_id, target\_path, size, offset | tus 会话，分片位于 `.easycloud/uploads`，完成时 rename 到目标路径 |
| User / Group | id, name, quota / id, name, parent\_id | 部门支持树形（V2） |
| Share | id(随机 128 bit), path, password\_hash, expire\_at, max\_downloads | 分享链接（V4） |
| Setting | key, value, updated\_by | Dashboard 覆盖值 |

### 5.2 数据流

```
HTTP Handler ──(路径字符串，仅被标准库解码一次)──▶ pathguard.Parse → RelPath
   App Service ──▶ Authorizer.Check(user, RelPath, perm) → Handle（os.Root 打开在授权目录上）
   App Service ──▶ Handle.Root.Rename / Open / ...（文件系统层兜底防逃逸）
   App Service ──▶ Repository（所有查询都带 owner 或授权条件）
   App Service ──▶ EventBus.Publish(FileChanged) ──▶ WebSocket Hub / 缩略图 / 配额统计
```

**解耦方式**：

- Handler 不拼接路径，只负责调用 `Parse`；拿不到 `RelPath` 就无法调用任何文件操作。
- Service 依赖 `Authorizer` 接口：V1 注入 `AdminAuthorizer`（数据根全量 rw），V2 注入 `GrantAuthorizer`，Service 代码不变。
- 缩略图、配额统计、WebSocket 推送、插件钩子都通过事件总线订阅文件变化，不侵入主流程。

## 6. 骨架代码

三个模块覆盖三个关键设计：VFS 作用域与路径安全、可覆盖配置与带跳转动作的错误、组装根中的依赖注入。

### 6.1 路径校验与授权（`internal/pathguard`）

```go
package pathguard

const ReservedDir = ".easycloud" // 回收站与上传分片所在目录，任何 API 都不可访问

// RelPath 只能由 Parse 构造：业务代码拿到的路径必然已校验，
// 从类型层面杜绝「某个接口忘记校验」这一类漏洞。
type RelPath struct{ segs []string }

// Parse 接收「已被 HTTP/JSON 标准库解码过恰好一次」的字符串，此后不再做任何解码。
// 因此 "%2e%2e" 在这里只是一个字面文件名，不会被二次解码成 ".."。
func Parse(raw string) (RelPath, error) {
	if !utf8.ValidString(raw) || strings.ContainsAny(raw, "\x00\\") {
		return RelPath{}, ErrInvalidPath
	}
	var segs []string
	for _, s := range strings.Split(raw, "/") {
		switch {
		case s == "":
			continue // 首尾或连续斜杠
		case s == "." || s == "..":
			return RelPath{}, ErrInvalidPath // 拒绝而非归一化：归一化会掩盖攻击且容易出错
		case len(s) > 255 || hasControlChar(s):
			return RelPath{}, ErrInvalidPath
		case len(segs) == 0 && s == ReservedDir:
			return RelPath{}, ErrInvalidPath
		}
		segs = append(segs, s)
	}
	return RelPath{segs: segs}, nil
}

// HasPrefix 按分段比较："share/AA" 不会命中 "share/AAB"。
func (p RelPath) HasPrefix(q RelPath) bool

type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
)

// Handle 是唯一能触达文件系统的对象。Root 打开在「授权目录」上而不是数据根上：
// 即使授权判断出现遗漏，os.Root 也保证 ".." 与符号链接无法越出该授权目录（纵深防御）。
type Handle struct {
	Root *os.Root
	Rel  string // 相对授权目录
	Perm Perm
}

// Authorizer：V1 注入 AdminAuthorizer（数据根全量 rw），V2 注入 GrantAuthorizer。
type Authorizer interface {
	// Check 未授权时返回 ErrNotFound 而非 ErrForbidden，不泄露其他目录是否存在。
	Check(ctx context.Context, sub Subject, p RelPath, need Perm) (Handle, error)
	// Visible 用于列表：授权目录的祖先只返回通往授权目录的分支。
	Visible(ctx context.Context, sub Subject, dir RelPath) (Visibility, error)
}
```

```go
package domain // internal/storage/domain

type FileIndexRepository interface {
	Upsert(ctx context.Context, n FileNode) error
	Move(ctx context.Context, from, to pathguard.RelPath) error // 目录移动批量更新前缀
	FindByInode(ctx context.Context, dev, ino uint64) (FileNode, error)
}

// 所有按 ID 查询的方法都要求 owner：从签名上防止平行越权。
type TrashRepository interface {
	Add(ctx context.Context, item TrashItem) error
	Get(ctx context.Context, owner UserID, id string) (TrashItem, error)
	Delete(ctx context.Context, owner UserID, id string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]TrashItem, error)
}
```

### 6.2 存储域用例：删除到回收站（`internal/storage/app`）

```go
package app

// FileService 只依赖接口；V1 到 V2 切换权限模型时本文件不改动。
type FileService struct {
	guard  pathguard.Authorizer
	index  FileIndexRepository
	trash  TrashRepository
	events EventPublisher
	clock  Clock
	policy TrashPolicyProvider // 保留天数来自可覆盖设置
}

func NewFileService(g pathguard.Authorizer, i FileIndexRepository, t TrashRepository,
	e EventPublisher, c Clock, p TrashPolicyProvider) *FileService

// MoveToTrash：物理上同卷 rename 到 .easycloud/trash/<owner>/<id>，再写记录。
// 顺序设计为「先 rename 后写库」：若写库失败，对账任务可从 .easycloud/trash 的目录名恢复记录；
// 反过来则会出现「记录存在但文件未移走」的不一致，用户可见性更差。
func (s *FileService) MoveToTrash(ctx context.Context, sub Subject, paths []string) ([]TrashItem, error)

func (s *FileService) Restore(ctx context.Context, sub Subject, trashID string, onConflict ConflictPolicy) error
func (s *FileService) List(ctx context.Context, sub Subject, dir string, q ListQuery) (Listing, error)
func (s *FileService) Move(ctx context.Context, sub Subject, from, to string, onConflict ConflictPolicy) (TaskID, error) // 跨卷移动转为后台任务
```

### 6.3 可覆盖配置与带引导动作的错误（`internal/settings`、`internal/platform/errs`）

```go
package settings

// Key 绑定类型与默认值；默认值来自 koanf 合并后的启动配置（内置默认 < yaml < env）。
type Key[T any] struct {
	Name     string // "upload.max_file_size"
	Fallback func(cfg *config.Config) T
}

var UploadMaxFileSize = Key[int64]{"upload.max_file_size", func(c *config.Config) int64 { return c.Upload.MaxFileSize }}
var UploadChunkSize   = Key[int64]{"upload.chunk_size", func(c *config.Config) int64 { return c.Upload.ChunkSize }}

// Store：数据库中的 Dashboard 覆盖值，带进程内缓存，修改后通过事件失效。
type Store interface {
	Get(ctx context.Context, name string) (raw string, ok bool, err error)
	Set(ctx context.Context, name, raw string, by UserID) error
}

// Resolve 实现最终优先级：Dashboard 覆盖 > env > yaml > 内置默认。
func Resolve[T any](ctx context.Context, s Store, cfg *config.Config, k Key[T]) (T, error)
```

```go
package errs

// Action 让错误自带「去哪里解决」，前端统一渲染为按钮，而不是文案描述路径。
type Action struct {
	Label string `json:"label"` // "调整上传限制"
	Route string `json:"route"` // "/admin/settings/upload"
	Focus string `json:"focus"` // "max_file_size"：跳转后高亮并聚焦该输入框
	Role  string `json:"role"`  // 仅对该角色展示按钮；普通用户看到「请联系管理员」
}

type AppError struct {
	Code    string  `json:"code"` // "UPLOAD_TOO_LARGE"，前端按 code 做 i18n
	Status  int     `json:"-"`
	Detail  any     `json:"detail,omitempty"` // {limit, actual}
	Action  *Action `json:"action,omitempty"`
	cause   error
}
```

### 6.4 组装根（`cmd/easycloud/main.go`）

```go
func main() {
	cfg := config.MustLoad() // 内置默认 → easycloud.yaml → .env / EASYCLOUD_* 环境变量
	log := platform.NewLogger(cfg.Log)

	// 用配置指定的数据库，运行期不回退；连接失败退避重试，超时后写日志并以 69/78 退出。
	ctx := context.Background()
	db := database.MustOpen(ctx, cfg.Database)
	db.MustCheckAndMigrate(ctx, cfg.DataRoot) // 版本检查、实例绑定、迁移前备份

	root := localfs.MustOpenDataRoot(cfg.DataRoot) // 校验 .easycloud 与数据根位于同一文件系统
	bus := eventbus.New()
	set := settings.NewStore(db.Settings(), bus)
	metrics := monitor.NewPebbleStore(cfg.Paths.Metrics) // 位于应用数据目录，不在数据根内

	// 手写构造注入；V2 只替换这一行 Authorizer。
	guard := pathguard.NewAdminAuthorizer(root)
	files := storageapp.NewFileService(guard, db.FileIndex(), db.Trash(), bus, clock.System{}, set)
	upload := transfer.NewUploadService(guard, set, cfg.Upload) // tusd 适配，钩子里校验大小与权限
	hub := realtime.NewHub(bus, guard)                          // 订阅目录主题同样经过授权

	router := httpx.NewRouter(log, httpx.ErrorMiddleware(log))
	storagehttp.Mount(router, files)
	transferhttp.Mount(router, upload)
	realtime.Mount(router, hub) // GET /ws：校验会话 Cookie 与 Origin
	web.MountSPA(router)        // embed.FS

	platform.Run(router, cfg.HTTP, files.StartIndexer, files.StartTrashReaper, metrics.StartCollector)
}
```

## 7. 工程落地

### 7.1 异常处理与日志

- 业务层只返回 `AppError` 或包装后的 `error`；`ErrorMiddleware` 统一映射为 HTTP 状态码与 JSON，未知错误一律 500 且不向前端泄露内部信息。
- 每个请求生成 `request_id`，写入 slog 上下文与响应头；前端错误提示可一键复制，便于排查。
- 文件操作写审计日志（谁、何时、对哪个虚拟路径做了什么），V2 起在 Dashboard 可查。
- 前端 API 层统一识别 `action` 字段：渲染为提示条中的按钮，跳转后目标设置项高亮并聚焦。

### 7.2 配置与环境隔离

| 配置项示例 | 内置默认 | yaml / .env | Dashboard 覆盖 |
| --- | --- | --- | --- |
| `upload.max_file_size` | 0（不限，仅受磁盘限制） | `EASYCLOUD_UPLOAD_MAX_FILE_SIZE` | 可 |
| `upload.chunk_size` | 16 MB | `EASYCLOUD_UPLOAD_CHUNK_SIZE` | 可（用户自建反代 body 限制较小时调小） |
| `trash.retention_days` | 30 | `EASYCLOUD_TRASH_RETENTION_DAYS` | 可 |
| `metrics.raw_retention` | 7 天 | `EASYCLOUD_METRICS_RAW_RETENTION` | 可 |
| `database.driver` | sqlite | `EASYCLOUD_DB_DRIVER` | 不可（启动依赖） |
| `database.dsn` | 无 | `EASYCLOUD_DB_HOST` / `PORT` / `USER` / `PASSWORD` / `NAME` | 不可 |
| `data_root` | `/data` | `EASYCLOUD_DATA_ROOT` | 不可（需重启） |
| `paths.app_data` | 二进制 `/var/lib/easycloud`，容器 `/config` | `EASYCLOUD_APP_DATA` | 不可 |
| `http.listen` | `:8080` | `EASYCLOUD_HTTP_LISTEN` | 不可 |

原则：影响进程启动的配置只在 env/yaml；运行时策略类配置可在 Dashboard 覆盖，且 Dashboard 显示「当前值来源」与「恢复为部署默认值」按钮。

### 7.3 部署形态

- **Docker**：GitHub Actions 构建 `linux/amd64`、`linux/arm64` 镜像，推送 GHCR。挂载示例：`/mnt/ssd/nasData:/data`（用户文件）、`./config:/config`（数据库、缩略图、监控、配置）。`compose.yml` 默认 SQLite，`compose.mariadb.yml` 附带 MariaDB 服务。容器以 `PUID/PGID` 运行，避免宿主文件属主变成 root。
- **二进制**：`install.sh` 检测架构 → 从 Release 下载 → 生成 `/etc/easycloud/.env`（MariaDB 配置项默认注释）→ 调用 `easycloud install` 完成数据库检测与兼容检查 → 创建 systemd 单元（配置错误退出码 78 时不自动重启）。默认 SQLite 库位于 `/var/lib/easycloud/easycloud.db`。升级复用同一脚本，启动时自动迁移。
- **Jellyfin**：同一 compose 中把 `/mnt/ssd/nasData/share/media` 以只读方式同时挂给 Jellyfin，无需任何网络协议。

### 7.4 扩展瓶颈与演进路线

| 瓶颈 | 触发条件（假定） | 演进方案 |
| --- | --- | --- |
| fsnotify 句柄耗尽 | 目录数 > 系统 inotify 上限（默认约 8 千～数十万） | 调大 `max_user_watches`；超大卷降级为定时增量扫描 |
| 缩略图生成抢占 CPU | 首次导入大量照片 | 任务队列限并发 + 低优先级；可拆出为独立 worker 进程 |
| 文件名搜索变慢 | 索引 > 数百万行 | MariaDB 全文索引 → 外置 Meilisearch 插件 |
| 单机进程内队列 | 需要多节点 | 任务队列接口替换为 NATS JetStream 实现，业务代码不变 |
| 大目录列表 | 单目录 > 5 万项 | 游标分页 + 前端虚拟滚动（已在 V1 设计中） |

### 7.5 安全：路径逃逸与平行越权

路径越权按多层纵深防御，任一层单独失效都不会导致越权。

| 层 | 措施 | 拦截的攻击 |
| --- | --- | --- |
| 传输 | 路径只放在 JSON body 或查询参数中，由标准库解码恰好一次；不放进 URL path | `%2e%2e`、`%252e%252e`（二次编码）、`..%2f`、反向代理路径归一化与改写 |
| 解析 | `pathguard.Parse`：拒绝 `.`/`..` 段、NUL、反斜杠、控制字符、非法 UTF-8、超长段、保留目录 `.easycloud` | 字面穿越、`..%5c`、`%00` 截断、`%c0%ae` 超长 UTF-8 编码 |
| 授权 | 按分段边界匹配授权；列表只返回授权分支；未授权统一返回 404 | 前缀碰撞（`share/AA` 命中 `share/AAB`）、目录存在性探测 |
| 文件系统 | `os.Root` 以授权目录为根打开，符号链接不得指向根外 | 宿主侧放置的逃逸符号链接、上层校验遗漏 |
| 资源 ID | 回收站、分享、上传会话、任务的仓储方法签名强制带 owner；ID 为 128 bit 随机数 | 修改 ID 访问他人资源（典型平行越权） |
| 解压/打包 | 归档内每个条目路径都经过 `Parse` 与 `Check` | Zip Slip |
| WebSocket | 握手校验 Cookie 与 Origin；订阅目录主题经过 `Check` | 跨站 WebSocket 劫持、订阅他人目录变更 |

验证方式：

- 对 `Parse` 做 `go test -fuzz` 模糊测试，断言「解析成功 ⇒ 拼接结果必在根内」。
- 维护攻击载荷回归表，覆盖全部 HTTP 接口与 WebSocket 订阅。
- 用户 A/B 交叉矩阵集成测试：A 携带 B 的每一类资源 ID 调用每个接口，期望全部 404。

已知限制：宿主机上创建的硬链接在应用层无法分辨来源，数据根内的硬链接由宿主管理员负责。

## 8. 调整后的版本路线

与原路线相比：V1 增加最小登录、VFS 与 Docker/CI；V5 从「实现打包」收缩为「安装与升级打磨」。

&#91;embedded content: 调整后的版本路线 · 5 个版本，4 个发布门槛\]

每个版本只在 V1 的扩展点上叠加实现：V2 替换 Authorizer，V3 订阅文件事件与 MIME 注册表，V4 复用 PathGuard 对外暴露，因此不出现跨版本重构。
