# EasyCloud

面向 NAS 的文件管理系统：网页文件管理、多用户与部门授权、分享、WebDAV/SMB、监控仪表盘与插件扩展。单个 Go 二进制（纯 Go、无 CGO，amd64/arm64），前端内嵌。

设计文档：[NAS 文件管理系统 · 需求评审与架构设计.md](NAS%20文件管理系统%20·%20需求评审与架构设计.md)。运维说明见 [docs/operations.md](docs/operations.md)，插件开发见 [docs/plugins.md](docs/plugins.md)，版本发布见 [docs/release.md](docs/release.md)。

## 功能

| 版本 | 能力 |
| --- | --- |
| V1 | 文件浏览（虚拟滚动）、上传（tus 分片续传、拖拽、暂停/继续）、下载（Range）、新建/重命名/移动/复制（冲突策略）、回收站、登录、实时刷新（WebSocket）、可在仪表盘覆盖的设置、带跳转按钮的错误提示 |
| V2 | 用户、部门树、目录授权（按路径分段匹配、部门授权对子部门生效、导航目录只显示授权分支）、个人空间配额、文件索引（inode 跟踪宿主机改名）、全局搜索、审计日志、监控（CPU/内存/磁盘/网络，Pebble 存储历史，降采样） |
| V3 | 插件：前端查看器（沙箱 iframe 中的 ES Module）、后端扩展（子进程 + gRPC）、外部服务连接器；ed25519 签名与版本兼容校验；插件仓库；缩略图与瀑布流；图片灯箱；OnlyOffice 在线编辑 |
| V4 | 分享链接（提取码、有效期、下载次数、文件夹打包、相册）、WebDAV 配置（管理员创建，每个配置独立地址与账号、只暴露所选目录）、Samba 网关插件、应用密码（SMB）、打包下载 |
| V5 | 交互式安装（MariaDB 连接失败三选一）、install.sh + systemd、升级与回滚、Docker 多架构镜像、发布流水线 |

内置插件（`plugins/`）：`ffmpeg-thumbnail`（视频/HEIC/RAW 缩略图）、`markdown-viewer`、`onlyoffice`（连接器）、`samba-gateway`。

## 部署

### Docker（推荐）

```bash
cd deploy/compose
cp .env.example .env                          # 设置数据库密码等
mkdir -p data config logs && sudo chown 1000:2000 data config logs
docker compose up -d                          # EasyCloud + MariaDB，访问 http://<host>:2030
docker compose -f compose.sqlite.yml up -d    # 或：内置 SQLite，无需数据库容器
```

| 挂载 | 内容 |
| --- | --- |
| `./data:/data` | 你的文件，结构与宿主机完全一致（可改为 NAS 目录，如 `/mnt/ssd/nasData:/data`） |
| `./config:/config` | SQLite 数据库、缩略图、监控数据、插件 |
| `./logs:/logs` | 可选；挂载后额外写入 `easycloud.log`（20 MB 轮转，保留 5 份），不挂载则只输出到 `docker logs` |

容器以 compose 中 `user: 1000:2000` 的身份运行（不经过 root），创建的文件属主就是它，因此挂载目录需先 `chown` 给该 uid:gid；不可写时容器会打印修复命令并退出。也可去掉 `user:`，改用 `PUID`/`PGID` 环境变量（以 root 启动后降权）。容器内监听 80 端口（`EASYCLOUD_HTTP_LISTEN` 可改；`network_mode: host` 时请改为 1024 以上端口）。首次启动未设置 `EASYCLOUD_ADMIN_PASSWORD` 时，`admin` 的随机密码会打印在 `docker logs EasyCloud` 中。

### 二进制 + systemd

```bash
curl -fsSL https://github.com/leapsea/easycloud/releases/latest/download/install.sh | sudo sh
```

安装脚本会检测架构、校验下载、创建 `easycloud` 系统用户、生成 `/etc/easycloud/.env`、运行 `easycloud install` 检查数据库（MariaDB 连接失败时提示「重试 / 改用 SQLite / 退出」），通过后才安装 systemd 单元。重复运行即升级：先检查数据库与迁移，新版本启动失败自动回滚。`--uninstall` 卸载（保留数据与配置）。

### 本地开发

```bash
make dev                    # 后端 :8080，数据目录 .dev/data
cd web && npm install && npm run dev   # 前端热更新，代理 /api 到 :8080
make test                   # go vet + race 测试
EASYCLOUD_TEST_MARIADB="127.0.0.1:3306:user:pass:easycloud_test" go test -p 1 ./...   # 同时跑 MariaDB
scripts/build-plugins.sh    # 构建并签名 plugins/ 下的插件，生成仓库 index.json
```

## 配置

优先级：仪表盘覆盖 > 环境变量（`.env`）> `easycloud.yaml` > 内置默认。影响启动的配置只能用环境变量或 yaml；策略类配置可在「系统设置」覆盖并一键恢复。

| 环境变量 | 默认 | 说明 |
| --- | --- | --- |
| `EASYCLOUD_DATA_ROOT` | `/data` | 数据根，界面路径就是它下面的真实相对路径 |
| `EASYCLOUD_APP_DATA` | `/var/lib/easycloud`（容器 `/config`） | 数据库、缩略图、监控、插件 |
| `EASYCLOUD_HTTP_LISTEN` | `:8080`（容器 `:80`） | 监听地址 |
| `EASYCLOUD_LOG_DIR` | —（容器 `/logs`） | 目录存在时额外写入轮转日志文件 `easycloud.log` |
| `EASYCLOUD_ADMIN_PASSWORD` | 随机 | 仅在没有任何用户时用于创建 `admin` |
| `EASYCLOUD_DB_HOST/PORT/USER/PASSWORD/NAME` | — | 配置 HOST 即启用 MariaDB 10.11+ |
| `EASYCLOUD_DB_DRIVER` | 自动 | `sqlite` 或 `mariadb` |
| `EASYCLOUD_DB_CONNECT_TIMEOUT` | `60` | 启动时连接重试的总秒数 |
| `EASYCLOUD_DB_LOG` | `slow` | 数据库查询日志：`slow` 记录慢查询（WARN），`all` 记录每条语句（INFO），`off` 关闭；只记 SQL 文本、耗时、行数和调用函数，不记参数 |
| `EASYCLOUD_DB_SLOW_MS` | `300` | 慢查询阈值（毫秒），耗时含读取全部结果 |
| `EASYCLOUD_UPLOAD_MAX_FILE_SIZE` | `0`（不限） | 单文件上限，字节（可在仪表盘覆盖） |
| `EASYCLOUD_UPLOAD_CHUNK_SIZE` | `16777216` | 分片大小；反向代理限制请求体时调小（可覆盖） |
| `EASYCLOUD_TRASH_RETENTION_DAYS` | `30` | 回收站保留天数（可覆盖） |
| `EASYCLOUD_METRICS_RAW_RETENTION_DAYS` / `..._MINUTE_...` | `7` / `90` | 监控数据保留（可覆盖） |
| `EASYCLOUD_PLUGINS_TRUSTED_KEYS` | — | 信任的插件签名公钥（逗号分隔，base64） |
| `EASYCLOUD_PLUGINS_ALLOW_UNSIGNED` | `false` | 允许安装未签名插件 |
| `EASYCLOUD_PLUGINS_REPOSITORY` | 官方索引（`plugin-index` 分支） | 插件市场 `index.json` 地址，可指向自建镜像 |

## 安全模型

- **路径**：所有路径只经标准库解码一次后交给 `pathguard.Parse`（拒绝 `.`/`..`、NUL、反斜杠、控制字符、非法 UTF-8、保留目录 `.easycloud`）；授权按路径分段匹配；文件访问一律通过以「授权目录」为根打开的 `os.Root`，符号链接无法越出授权目录。`Parse` 有模糊测试，HTTP 层有攻击载荷回归测试和用户 A/B 交叉矩阵测试。
- **资源 ID**：回收站、上传会话、分享、应用密码的查询都强制带属主；ID 为 128 位随机数；未授权一律返回 404。
- **会话**：HttpOnly Cookie、SameSite=Lax、非同源写请求拒绝、登录限速；WebSocket 校验 Origin，目录订阅经过授权。
- **插件**：ed25519 签名校验、Zip Slip 防护；查看器运行在 `sandbox="allow-scripts"` 的不透明源 iframe 中，只拿到当前文件的限时令牌；后端插件进程只继承无敏感信息的环境变量。
- **分享与令牌**：分享以属主的实时权限读取，属主被停用或失去权限即失效；文件令牌为 HMAC 签名并在每次使用时重新校验。
- **OnlyOffice**：编辑器配置 JWT 签名；保存回调校验 JWT，且只从所配置的 Document Server 主机下载（防 SSRF）。

已知限制：宿主机上创建的硬链接无法在应用层识别来源。

## 测试覆盖

`go test ./...`：PathGuard（含模糊测试）、两种数据库方言的迁移与兼容检查（新版本库拒绝、版本表缺失拒绝、外部表不触碰、实例绑定、损坏文件不覆盖）、存储用例、文件索引（fsnotify、inode 改名检测）、监控存储与降采样、插件签名/篡改/Zip Slip/真实子进程端到端（含崩溃自动重启）、缩略图、文件令牌、OnlyOffice 回调（JWT、SSRF）、分享、WebDAV、Samba 同步、安装流程。
