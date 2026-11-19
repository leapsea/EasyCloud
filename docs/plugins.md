# 插件开发

EasyCloud 有三类插件，共用一种包格式：zip 根目录放 `manifest.json`，可附 `signature.json`（ed25519）。

| 类型 | 运行方式 | 适用 |
| --- | --- | --- |
| `viewer` | 浏览器中 `sandbox="allow-scripts"` 的 iframe 里动态 `import()` 你的 ES Module | 文件预览/查看器 |
| `backend` | EasyCloud 启动的子进程，gRPC（hashicorp/go-plugin，JSON 编码）通讯 | 缩略图、文件事件处理、协议网关 |
| `connector` | 只有配置项，由 EasyCloud 内置逻辑使用 | OnlyOffice 等外部服务 |

## manifest.json

```json
{
  "id": "my-plugin",                       // ^[a-z0-9][a-z0-9-]{1,47}$
  "name": "显示名称",
  "version": "1.2.0",                      // semver
  "type": "viewer | backend | connector",
  "description": "…", "author": "…",
  "engines": { "easycloud": ">=0.3.0 <1.0.0" },  // 支持 >= > <= < = ^
  "permissions": ["fs:read"],              // 安装时展示给管理员
  "viewer":  { "entry": "index.js", "mimes": ["text/markdown"], "extensions": [".md"], "priority": 10, "label": "…" },
  "backend": { "executable": { "linux/amd64": "bin/linux-amd64/x", "linux/arm64": "bin/linux-arm64/x" },
               "thumbnail": ["video/*"], "events": false, "accounts": false },
  "connector": { "kind": "onlyoffice" },
  "config": [ { "key": "url", "label": "地址", "type": "string|secret|number|bool|url", "required": true, "default": "…", "help": "…" } ]
}
```

`secret` 类型的配置在管理界面中以 `********` 显示，提交未修改的掩码不会覆盖原值。

## 查看器插件

入口模块默认导出 `mount(el, ctx)`：

```js
export default {
  async mount(el, ctx) {
    // ctx.file: { name, mime, size, path, url }  url 是该文件的限时令牌地址（支持 Range）
    // ctx.theme: 'light' | 'dark'
    // ctx.fetchText() / ctx.fetchBlob()  ctx.download()  ctx.close()
    const text = await ctx.fetchText()
    el.textContent = text
  },
}
```

运行环境：不透明源（没有 Cookie、无法访问应用 API），CSP 只允许加载你插件目录下的脚本/样式以及当前文件的令牌地址。资源以 `/plugins/<id>/<version>/…` 提供，带长缓存。示例：[plugins/markdown-viewer](../plugins/markdown-viewer)。

## 后端插件

```go
package main

import (
	"context"
	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

type ext struct{ pluginsdk.Base } // Base 提供各方法的默认实现

func (ext) Thumbnail(ctx context.Context, req *pluginsdk.ThumbnailRequest) (*pluginsdk.ThumbnailResponse, error) {
	// req.Path 是已经过授权校验的绝对路径；只读取它。
	return &pluginsdk.ThumbnailResponse{Image: jpegBytes, Mime: "image/jpeg"}, nil
}

func main() {
	var cfg struct{ Option string `json:"option"` }
	_ = pluginsdk.Config(&cfg) // 管理界面中的配置
	pluginsdk.Serve(ext{})
}
```

可实现的方法：

| 方法 | 何时调用 |
| --- | --- |
| `Thumbnail` | manifest `backend.thumbnail` 中的 MIME 需要缩略图时；返回 `pluginsdk.ErrUnsupported` 让宿主放弃 |
| `OnEvent` | `backend.events: true` 时，文件变更（含宿主机侧改动）异步推送 |
| `Sync` | `backend.accounts: true` 时，推送账户（应用密码的 NT hash）与按授权计算的共享列表（完整快照） |

运行约束：子进程只继承 `PATH/HOME/LANG/TZ/TMPDIR` 与 `EASYCLOUD_PLUGIN_CONFIG`，不会拿到数据库密码；崩溃后自动指数退避重启；工作目录是插件版本目录。

## 打包与签名

```bash
go build -o .dev/ecplugin ./cmd/ecplugin
.dev/ecplugin keygen -out mykey                         # mykey.key（私钥，妥善保管）+ mykey.pub
.dev/ecplugin pack -dir path/to/plugin -key mykey.key   # → <id>-<version>.zip
.dev/ecplugin verify -pub mykey.pub my-plugin-1.2.0.zip
.dev/ecplugin index -base https://example.com/plugins/ *.zip > index.json   # 私有仓库索引
```

签名覆盖包内除 `signature.json` 外所有文件的 SHA-256 清单，任何文件被改动都会验签失败。EasyCloud 只安装由官方公钥（内置于 `internal/plugin/official_keys.txt`）或 `EASYCLOUD_PLUGINS_TRUSTED_KEYS` 中公钥签名的包（除非显式设置 `EASYCLOUD_PLUGINS_ALLOW_UNSIGNED=true`）。解压时每个条目都经过路径校验（防 Zip Slip），禁止符号链接。

`scripts/build-plugins.sh` 交叉编译、打包并签名 `plugins/` 下的插件（`ONLY="<id>"` 只构建指定插件）。官方插件的发布流程与插件市场索引见 [release.md](release.md)。

## 插件市场（仓库索引）

「插件 → 插件仓库」读取 `EASYCLOUD_PLUGINS_REPOSITORY` 指向的 `index.json`（默认为官方索引），列出与当前 EasyCloud 版本兼容的最新版本，可安装与更新；下载后先校验 SHA-256，再走与手动上传相同的验签与安装流程。格式：

```json
{ "plugins": [ { "id": "ffmpeg-thumbnail", "name": "…", "type": "backend", "description": "…",
    "versions": [ { "version": "1.0.1", "url": "https://…/ffmpeg-thumbnail-1.0.1.zip", "sha256": "…", "engine": ">=0.4.0" } ] } ] }
```

自建仓库（内网、镜像或第三方插件）：把 zip 放到任意静态地址，用 `ecplugin index -base <地址前缀> *.zip > index.json` 生成索引，再把 `EASYCLOUD_PLUGINS_REPOSITORY` 指向它，并把签名公钥加入 `EASYCLOUD_PLUGINS_TRUSTED_KEYS`。
