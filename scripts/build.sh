#!/usr/bin/env bash
# 构建可部署的 homehub 单文件二进制（前端已嵌入）。
#
# 顺序不能反：go:embed 在编译时读取 web/dist，先 go build 会嵌入上一版前端
# （或者只有占位文件）。这个脚本就是为了把顺序固定下来。
#
# 用法：
#   scripts/build.sh                 # linux/amd64，输出 ./homehub
#   GOARCH=arm64 scripts/build.sh    # ARM NAS
#   OUT=dist/homehub scripts/build.sh
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="${OUT:-homehub}"

echo "==> 前端：pnpm install && pnpm build"
(cd web && pnpm install --frozen-lockfile && pnpm build)

if [ ! -f web/dist/index.html ]; then
  echo "web/dist/index.html 不存在，前端构建失败" >&2
  exit 1
fi

echo "==> 后端：go build（嵌入 web/dist）"
CGO_ENABLED=0 GOOS="${GOOS:-linux}" GOARCH="${GOARCH:-amd64}" \
  go build -trimpath -ldflags="-s -w" -o "$OUT" ./cmd/homehub

echo "==> 完成：$OUT（$(du -h "$OUT" | cut -f1)，提交 $(git rev-parse --short HEAD)$(git diff --quiet HEAD -- . ':!web/dist' || echo '，工作区有未提交改动')）"
