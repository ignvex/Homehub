# HomeHub 多阶段构建 Dockerfile
# 构建: docker build -t homehub .
# 运行: docker run -d -p 8080:8080 -v $(pwd)/config.json:/app/config.json -v $(pwd)/uploads:/app/web/static/uploads homehub

# ============================================================
# 阶段 1: 构建二进制
# ============================================================
FROM golang:1.26-alpine AS builder

WORKDIR /build

# 安装 CA 证书（用于 HTTPS 请求）
RUN apk add --no-cache ca-certificates tzdata

# 复制依赖文件并下载
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o homehub .

# ============================================================
# 阶段 2: 运行镜像
# ============================================================
FROM alpine:latest

WORKDIR /app

# 从构建阶段复制必要文件
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /build/homehub .

# 复制 Web 静态资源
COPY web/ ./web/

# 创建上传目录
RUN mkdir -p /app/web/static/uploads

EXPOSE 8080

# 默认加载 /app/config.json，可通过环境变量覆盖
ENV HOMEHUB_CONFIG=/app/config.json

ENTRYPOINT ["./homehub"]
