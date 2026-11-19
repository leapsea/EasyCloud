# 运维手册

## 目录布局

| 位置 | 内容 | 备份 |
| --- | --- | --- |
| 数据根（`EASYCLOUD_DATA_ROOT`，容器 `/data`） | 你的文件，结构与宿主机一致 | 按你的常规文件备份策略 |
| 数据根下 `.easycloud/` | 回收站、上传分片、`instance.json`（实例 ID） | 随数据根一起备份；不要单独删除 `instance.json` |
| 应用数据（`EASYCLOUD_APP_DATA`，容器 `/config`） | `easycloud.db`（SQLite）、`metrics/`（Pebble）、`thumbs/`（可重建）、`playback/`（视频播放修正缓存，可重建）、`plugins/` | 备份 `easycloud.db`（或 MariaDB）与 `plugins/`；`thumbs/`、`playback/` 可不备份 |
| 日志（`EASYCLOUD_LOG_DIR`，容器 `/logs`，可选） | `easycloud.log` 及 `.1`…`.5`（每个 20 MB） | 不需要 |

`.easycloud` 必须与数据根位于同一文件系统（回收站与上传完成都依赖原子 rename），启动时会校验。

## 退出码

| 退出码 | 含义 | systemd / Docker 行为 |
| --- | --- | --- |
| 0 | 正常退出 | — |
| 69 | 暂时不可用（数据库地址不可达、超时） | 继续重启，等待数据库恢复 |
| 78 | 配置错误（认证失败、库不存在、数据库版本过低、库结构比程序新、数据库与数据根不匹配、端口被占用） | systemd 单元设置了 `RestartPreventExitStatus=78`，不再重启，避免刷屏 |

每条致命错误日志都写明原因、目标地址（不含密码）与修复方法：`journalctl -u easycloud -n 50` 或 `docker logs easycloud`。

## 数据库

- **默认 SQLite**：WAL 模式、单写连接、多读连接。
- **MariaDB 10.11+**：在 `.env` 中配置 `EASYCLOUD_DB_HOST` 等即启用。账号需要建表权限（`GRANT ALL ON easycloud.* TO 'easycloud'@'%'`）。所有表名带 `easycloud_` 前缀，可与其他应用共存，但建议独立库。
- **运行期不回退**：服务启动时连不上配置的数据库，会在 `EASYCLOUD_DB_CONNECT_TIMEOUT` 秒内退避重试，然后以 69/78 退出。改用 SQLite 只能在 `easycloud install` 中显式选择。

### 首次启动与升级时的兼容检查

| 情况 | 处理 |
| --- | --- |
| 空库 | 执行全部迁移 |
| 旧版本数据 | SQLite 自动 `VACUUM INTO easycloud.db.v<N>.<时间>.bak` 后迁移；MariaDB 在日志中提示先 `mysqldump` |
| 库结构比程序新（降级） | 拒绝启动（78），请升级程序 |
| 有 `easycloud_` 表但版本表缺失 | 拒绝启动（78），需人工检查 |
| 库属于另一个数据根 | 拒绝启动（78）；确认无误后运行 `easycloud rebind` |
| SQLite 文件损坏 | 拒绝启动，原文件不动 |

## 常用命令

```bash
easycloud serve                    # 启动（systemd 单元使用）
easycloud install --env /etc/easycloud/.env   # 数据库检查（install.sh 调用；-y 为非交互）
easycloud rebind                   # 接受当前数据根，用于迁移数据或更换数据库
easycloud reset-password admin     # 忘记密码（服务运行时也可执行；会注销该用户所有会话）
easycloud version
```

二进制安装时，命令需要读取 `.env`：`sudo -u easycloud EASYCLOUD_ENV_FILE=/etc/easycloud/.env easycloud reset-password admin`。Docker：`docker exec -it easycloud easycloud reset-password admin`。

## 升级

- **Docker**：`docker compose pull && docker compose up -d`（compose 中应用服务名为 `app`，数据库为 `db`）。启动时自动迁移（SQLite 先自动备份）。
- **二进制**：重新运行 install.sh。它会停服务 → 替换二进制（保留 `.prev`）→ `easycloud install` 检查 → 启动；检查失败或新版本起不来会自动回滚到旧二进制。
- 迁移只做向前兼容的增量变更（先加列、跨版本再删列），回滚一个版本的二进制可以读取新数据。

## 反向代理

- 上传走 tus 分片：反向代理的请求体限制只需大于分片大小（默认 16 MB，可在「系统设置」调小）。
- 需要转发 WebSocket（`/api/ws`）与 `/dav/` 下的 WebDAV 方法（PROPFIND、MKCOL、MOVE、COPY、LOCK、UNLOCK）。
- 设置 `X-Forwarded-Proto`（HTTPS 下 Cookie 带 `Secure`）与 `X-Forwarded-Host`（同源校验）。

Nginx 示例：

```nginx
location / {
    proxy_pass http://127.0.0.1:2030;   # compose 映射的端口；二进制安装默认 8080
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-Host $host;
    client_max_body_size 32m;      # > 分片大小
    proxy_request_buffering off;   # 上传直接透传
    proxy_buffering off;           # 下载与视频拖动
}
```

## 文件索引与宿主机改动

索引用 fsnotify 实时跟踪宿主机上的改动，每 6 小时全量对账一次。目录数超过 inotify 上限时会自动退化为每 10 分钟扫描，并在日志中提示调大：

```bash
echo fs.inotify.max_user_watches=1048576 | sudo tee /etc/sysctl.d/90-easycloud.conf && sudo sysctl --system
```

在宿主机上改名或移动目录时，授权与分享会依据 inode 自动跟随新路径。

## 视频播放修正

网页播放器打开 MP4/MOV 前会先分析文件，遇到以下情况时改为播放「修正后的虚拟文件」：新生成的文件头（ftyp + moov）加上源文件中原样的音视频数据。**源文件不会被修改或复制，也不转码**，拖动进度正常（标准 Range 请求）。

| 问题 | 现象 | 修正 |
| --- | --- | --- |
| H.264 含 B 帧但缺少 ctts（帧显示时间偏移） | VLC 正常，浏览器、QuickTime 卡顿/丢帧 | 从每帧 slice 头的 POC 计算显示顺序，补上 ctts |
| moov（索引）在文件末尾 | 大文件起播、拖动慢 | 把 moov 放到开头 |

- 播放器标题栏显示「已修正播放」，悬停可见原因；修正流出错时自动退回直接播放原文件。
- 分析需要顺序读一遍文件（只解析帧头）：3 小时 1080p、7.9 GB 的文件在 SSD 上约 1.5 秒，机械硬盘约受顺序读速度限制（约 40–50 秒）。结果按「路径 + 大小 + 修改时间」缓存在 `playback/`，文件变化后自动重新分析，90 天未使用的缓存会被清理。
- 浏览器无法解码的编码（如 HEVC、MKV 容器）不在此范围，仍需下载后用本地播放器打开。

## 协议访问

- **WebDAV**：由管理员在「WebDAV」页面创建配置。每个配置有独立的地址 `https://<host>/dav/<地址标识>/` 和独立账号（用户名 + 密码，密码只在创建或重置时显示一次），根目录只列出配置中选择的目录（名称冲突时需设置别名），每个目录可设为只读或读写，越界访问一律 404。删除进入配置创建者的回收站；目录所在个人空间的配额、系统目录保护照常生效；在文件管理中重命名或移动目录时，配置会自动跟随。停用或删除配置立即生效。建议使用 HTTPS。
- **SMB**：安装并启用「Samba 网关」插件。每个用户在「我的账号」生成应用密码后才会出现在 SMB 账户中（使用最新的应用密码）。授权、部门、用户变化会在 2 秒内同步到 Samba 配置。插件可只生成 `smb.conf`/`smbpasswd` 供你自己的 Samba 使用，或通过 Docker socket 管理 Samba 容器（`force user` 与 EasyCloud 的 PUID 一致，文件属主统一）。
- **Jellyfin 等**：直接把同一宿主目录只读挂给它们，无需任何网络协议。
