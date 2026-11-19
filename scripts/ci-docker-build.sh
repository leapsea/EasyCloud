#!/bin/sh
# 构建并推送多架构镜像（GitLab CI 调用；本地设置 PUSH=0 可只构建验证）。
#
# 镜像标签规则：
#   tag v1.2.3      -> 1.2.3, 1.2, latest
#   tag v1.2.3-rc.1 -> 1.2.3-rc.1（预发布不移动 latest）
#   分支           -> <分支 slug>, sha-<短 SHA>（程序版本为 dev-<短 SHA>，插件兼容检查视为开发版）
#
# 输入（环境变量）：
#   IMAGE      目标仓库，必填，如 registry.example.com/nas/easycloud
#   PLATFORMS  目标架构，默认 linux/amd64,linux/arm64
#   PUSH       1 推送（默认）；0 只构建并加载到本地（此时只能单一架构）
#   CACHE_REF  可选，registry 构建缓存地址，如 $IMAGE:buildcache
#   NODE_IMAGE GO_IMAGE BASE_IMAGE NPM_REGISTRY GOPROXY  可选，透传给 Dockerfile 的镜像/源
#   CI_COMMIT_TAG CI_COMMIT_REF_SLUG CI_COMMIT_SHORT_SHA CI_COMMIT_SHA CI_PROJECT_URL  GitLab 预定义变量
set -eu

: "${IMAGE:?IMAGE is required}"
PLATFORMS=${PLATFORMS:-linux/amd64,linux/arm64}
PUSH=${PUSH:-1}

# 1) 由触发来源计算程序版本号与镜像标签
if [ -n "${CI_COMMIT_TAG:-}" ]; then
  version=${CI_COMMIT_TAG#v}
  tags=$version
  case $version in
    *-*) ;;                                  # 预发布：只打完整版本号
    *) tags="$tags ${version%.*} latest" ;;  # 1.2.3 -> 1.2
  esac
else
  sha=${CI_COMMIT_SHORT_SHA:-$(git rev-parse --short HEAD)}
  version=dev-$sha
  tags="${CI_COMMIT_REF_SLUG:-local} sha-$sha"
fi

# 2) 组装 buildx 参数；可选项为空时不传，保留 Dockerfile 中的默认值
set -- --file deploy/docker/Dockerfile --platform "$PLATFORMS" \
  --build-arg "VERSION=$version" \
  --label "org.opencontainers.image.version=$version" \
  --label "org.opencontainers.image.revision=${CI_COMMIT_SHA:-unknown}" \
  --label "org.opencontainers.image.source=${CI_PROJECT_URL:-}"
for name in NODE_IMAGE GO_IMAGE BASE_IMAGE NPM_REGISTRY GOPROXY; do
  eval "value=\${$name:-}"
  [ -n "$value" ] && set -- "$@" --build-arg "$name=$value"
done
for t in $tags; do set -- "$@" --tag "$IMAGE:$t"; done
if [ -n "${CACHE_REF:-}" ]; then
  # 缓存写入失败（如仓库不支持 OCI 缓存清单）不应导致发布失败
  set -- "$@" --cache-from "type=registry,ref=$CACHE_REF" \
    --cache-to "type=registry,ref=$CACHE_REF,mode=max,image-manifest=true,oci-mediatypes=true,ignore-error=true"
fi
if [ "$PUSH" = 1 ]; then set -- "$@" --push; else set -- "$@" --load; fi

echo "version=$version"
echo "tags=$tags"
docker buildx build "$@" .

# 3) 推送后核对清单，确认每个标签都包含目标架构
if [ "$PUSH" = 1 ]; then
  for t in $tags; do docker buildx imagetools inspect "$IMAGE:$t" | sed -n '1,/^$/p;/Platform:/p'; done
fi
