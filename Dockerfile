# HomeHub Dockerfile（单阶段打包）
#
# 所有编译在本地完成，Docker 只负责打包：
#   1. 前端：cd web && pnpm install && pnpm build
#   2. 后端：GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o homehub ./cmd/homehub
#   3. Docker：docker build -t homehub .
#
# 配置文件不打包进镜像（避免凭据泄露），运行时通过挂载提供：
#   docker run -d -p 8080:8080 \
#     -v "$(pwd)/config.json:/app/config.json" \
#     -v /宿主机/uploads:/app/web/static/uploads homehub
#
# config.json 需可写：首次创建用户后会清除 auth.password，Web 端配置更新也会写回该文件。

FROM alpine:latest

WORKDIR /app

# 换清华镜像源（国内下载快 100 倍），安装基础依赖
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata

# 复制预编译的 Go 二进制（本地交叉编译好了，不用在 Docker 里装 Go）
COPY homehub .

# 复制前端构建产物
COPY web/dist ./web/dist

# 复制静态资源目录
COPY web/static ./web/static

# 创建上传目录（运行时通过 volume 挂载覆盖）
RUN mkdir -p /app/web/static/uploads

EXPOSE 8080

ENV HOMEHUB_CONFIG=/app/config.json

ENTRYPOINT ["./homehub"]
