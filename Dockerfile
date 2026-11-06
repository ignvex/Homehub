# HomeHub Dockerfile（单阶段打包）
#
# 设计前提：编译在宿主机完成，镜像只负责运行。
# 升级流程因此可以很轻：替换 homehub 二进制后重新 build，
# 不需要在镜像里安装 Go 或 Node.js。
#
# 构建前准备：
#   1. 二进制：scripts/build.sh（先构建前端，再 go build 把 web/dist 嵌入二进制）
#   2. 镜像：docker build -t homehub .
#
# 配置文件不打包进镜像（避免凭据泄露），运行时通过挂载提供：
#   docker run -d -p 8080:8080 \
#     -v "$(pwd)/config.json:/app/config.json" \
#     -v /宿主机/uploads:/app/web/static/uploads homehub
#
# config.json 需可写：首次创建用户后会清除 auth.password，Web 端配置更新也会写回该文件。

FROM debian:trixie-slim

# 换清华镜像源（国内下载快 100 倍）。trixie 使用 deb822 格式（debian.sources），
# bookworm 及更早使用 sources.list，循环同时覆盖两种，文件不存在则跳过。
# 若部署在海外，删掉 sed 那一行即可回退到官方源。
#
# 安装的运行时依赖：
#   ca-certificates  Go 程序访问 HTTPS（Gravatar / LLM API / RSS 源）
#   tzdata           配合 TZ 环境变量提供正确时区
#   curl             手工诊断用（健康检查由二进制自己完成，见下方 HEALTHCHECK）
RUN set -eux; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        if [ -f "$f" ]; then \
            sed -i -E 's|https?://(deb|security)\.debian\.org|http://mirrors.tuna.tsinghua.edu.cn|g' "$f"; \
        fi; \
    done; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates tzdata curl; \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app

# 复制预编译的 Go 二进制（本地交叉编译好了，不用在 Docker 里装 Go）
COPY homehub .

# 复制静态资源目录
COPY web/static ./web/static

# 创建上传目录（运行时通过 volume 挂载覆盖）
RUN mkdir -p /app/web/static/uploads

EXPOSE 8080

ENV HOMEHUB_CONFIG=/app/config.json \
    TZ=Asia/Shanghai

# 健康检查走二进制自身的 -healthcheck：它从 config.json 读 server.host /
# server.port，改端口时这里无需同步，也不会探测错地址。endpoint 在数据库
# 不可达时仍返回 200，因此该检查只反映进程存活，不反映数据库连通性。
# start-period 覆盖启动时等待数据库的上限（60s）再加启动开销。
HEALTHCHECK --interval=30s --timeout=10s --start-period=75s --retries=3 \
    CMD ["/app/homehub", "-healthcheck"]

ENTRYPOINT ["/app/homehub"]
