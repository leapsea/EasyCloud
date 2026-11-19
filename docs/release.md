# 版本发布

主程序与插件在同一仓库，但版本号、标签、构建与发布流水线完全独立：

| | 主程序 | 插件 |
| --- | --- | --- |
| 版本号 | 标签本身 | `plugins/<id>/manifest.json` 的 `version` |
| 标签 | `v0.5.0` | `plugins/ffmpeg-thumbnail/v1.0.1` |
| 流水线 | `.github/workflows/release.yml` | `.github/workflows/plugins.yml` |
| 产物 | GitHub Release（二进制、install.sh，**标为 latest**）+ 镜像 `ghcr.io/leapsea/easycloud` | 独立 GitHub Release（插件包 + 索引条目，**不标为 latest**）+ 插件市场索引 |
| 构建范围 | 只构建 `cmd/easycloud`（Docker 构建上下文排除 `plugins/`） | 只构建、测试被打标签的那一个插件 |

两者唯一的联系是插件 manifest 中的 `engines.easycloud`（如 `">=0.4.0"`）：插件市场只向满足范围的主程序提供该版本，旧版本始终保留在索引中，所以老版本主程序仍能装到兼容的插件。后端插件的 RPC 不兼容变更还要提升 `pkg/pluginsdk.ProtocolVersion`。

## 一次性设置

1. **镜像设为公开**：第一次发布后，GHCR 包默认是私有的，`docker pull ghcr.io/leapsea/easycloud:latest` 会要求登录。到 GitHub → leapsea → Packages → easycloud → Package settings → Change visibility → Public。发布流水线最后一步会匿名拉取镜像，包仍是私有时会失败并提示这一步；改为公开后重跑该 job 即可。
2. **插件签名密钥**：

   ```bash
   go run ./cmd/ecplugin keygen -out official
   ```

   - `official.pub` 的内容追加到 `internal/plugin/official_keys.txt` 并提交——它会编进主程序，市场里的官方插件无需额外配置即可通过验签（需随下一个主程序版本发布才生效）。
   - `official.key` 的内容保存为仓库 Secret `PLUGIN_SIGNING_KEY`（Settings → Secrets and variables → Actions），然后删除本地文件。
   - 轮换密钥时，旧公钥在 `official_keys.txt` 中保留一段时间，已发布的旧版本插件才能继续安装。
3. 无需开启 GitHub Pages：插件索引通过 `raw.githubusercontent.com` 提供。

## 发布主程序

```bash
git tag v0.5.0 && git push origin v0.5.0
```

`release.yml` 先跑完整测试（复用 `test.yml`），再并行：

- **binaries**：linux amd64/arm64 二进制、`install.sh`、`checksums.txt` 发布到 Release，并标为 latest（`install.sh` 从 `releases/latest` 下载）。
- **image**：多架构镜像，标签 `0.5.0`、`0.5`、`latest`；最后匿名拉取并运行 `easycloud version` 验证。

预发布标签（`v0.5.0-rc.1`）发布为 pre-release，镜像只打 `0.5.0-rc.1`，不会移动 `latest`。

## 分支镜像（main）

推送到 `main` 时，`ci.yml` 先跑 `test.yml`，测试通过且本次推送改动了程序（不只是 `plugins/`、`docs/`、`*.md`、`plugins.yml`）时，调用 `image.yml` 构建并推送 `ghcr.io/leapsea/easycloud:main` 与 `:sha-<短SHA>`，程序版本为 `dev-<短SHA>`（插件兼容检查视为开发版）。Pull Request 只跑测试，不推镜像。

| 文件 | 作用 |
| --- | --- |
| `test.yml` | 完整测试（可复用） |
| `image.yml` | 多架构镜像构建、推送与匿名拉取验证（可复用） |
| `ci.yml` | main 推送 / PR 的入口 |
| `release.yml` | `v*` tag：测试 → 二进制 Release + 版本镜像 |
| `plugins.yml` | 插件发布与市场索引 |

## 发布插件

1. 修改 `plugins/<id>/`，并提升其 `manifest.json` 的 `version`；用到新版主程序能力时同时提高 `engines.easycloud`。CI 检查：已发布插件的目录有改动但版本号未变时失败（`scripts/check-plugin-versions.sh`）。
2. 合并后打标签：

   ```bash
   git tag plugins/ffmpeg-thumbnail/v1.0.1 && git push origin plugins/ffmpeg-thumbnail/v1.0.1
   ```

3. `plugins.yml` 自动完成：
   - 校验标签版本与 manifest 一致，测试并只构建这一个插件，用 `PLUGIN_SIGNING_KEY` 签名；
   - 创建 Release「<插件名> 1.0.1」，附 `ffmpeg-thumbnail-1.0.1.zip` 和索引条目 `ffmpeg-thumbnail-1.0.1.json`（含下载地址、SHA-256、兼容范围），说明为该插件目录自上个版本以来的提交；
   - 重建插件市场索引并推送到 `plugin-index` 分支。
4. 几分钟内（raw 缓存约 5 分钟）各实例的「插件 → 插件仓库」显示可更新。

## 插件市场索引

```
https://raw.githubusercontent.com/leapsea/EasyCloud/plugin-index/index.json
```

- **来源**：索引不是手工维护的文件，而是每次从**所有**插件 Release 的索引条目重新生成。Release 是唯一事实来源，索引可以随时重建，不会和实际发布物不一致。
- **撤回某个版本**：删除对应的 Release（及标签）。删除 Release 会自动触发重建；也可以在 Actions → plugins → Run workflow 手动重建。
- **一致性**：同一插件同一版本若出现两个内容不同的包，重建会失败，避免已安装的副本无法区分。
- **并发**：索引推送串行执行；排队中的重建可能被更新的一次取代，因为每次重建都读取全部 Release，结果相同。
- **镜像 / 内网**：GitHub 不可达时，可把 zip 与 `ecplugin index -base <镜像地址前缀> *.zip` 生成的索引放到任意静态服务器，并设置 `EASYCLOUD_PLUGINS_REPOSITORY`；或在「插件」页直接上传从 Release 下载的 zip。

## GitLab CI（已停用）

`.gitlab-ci.yml` 与 `scripts/ci-docker-build.sh` 是此前为自建 GitLab 编写的镜像构建流程，现已改用 GitHub Actions；`.gitlab-ci.yml` 已被 `.gitignore` 忽略，不会提交。
