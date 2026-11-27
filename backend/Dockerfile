# 后端容器化：multi-stage，alpine builder + alpine runner
# go-sqlite3 是 CGO，distroless static 跑不动，所以走 alpine + musl 静态链接路线
#
# 构建上下文 = repo root（不再是 backend/）
# 原因：entrypoint 要起 indexrepo 扫整个 repo 做 RAG 全仓预索引
# deploy.yml 实际 `flyctl deploy . --config backend/fly.toml --dockerfile backend/Dockerfile`

# ============ builder ============
FROM golang:1.25-alpine AS builder

# CGO 工具链 + sqlite 头文件
RUN apk add --no-cache build-base sqlite-dev

WORKDIR /src/backend

# 利用 docker cache：先复制 mod 文件单独下载依赖
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# 两个 binary：server 是主进程；indexrepo 在 entrypoint 启动时跑一次预索引
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags '-s -w -linkmode external -extldflags "-static"' \
    -o /out/server ./cmd/server
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags '-s -w -linkmode external -extldflags "-static"' \
    -o /out/indexrepo ./cmd/indexrepo

# ============ runner ============
FROM alpine:3.24

# CA certs 给 https 用；tzdata 给 logging 用本地时区
RUN apk add --no-cache ca-certificates tzdata

# 非 root 用户
RUN addgroup -g 1000 app && adduser -u 1000 -G app -D -h /app app

WORKDIR /app

# 二进制
COPY --from=builder /out/server /app/server
COPY --from=builder /out/indexrepo /app/indexrepo

# 全仓源码给 indexrepo 启动时扫
# .dockerignore（repo root）已排除 node_modules / .git / .claude / dist 等
# 仅 COPY 评审有价值的内容；运行时 server 主流程不读 /app/src
COPY --chown=app:app backend /app/src/backend
COPY --chown=app:app frontend /app/src/frontend
COPY --chown=app:app docs /app/src/docs

# entrypoint：后台 indexrepo + exec server
COPY --chown=app:app backend/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

USER app

# Volume：SQLite 评审历史 + RAG 索引都挂这里（重启不丢）
VOLUME ["/data"]

# Defaults：可被 fly.toml [env] 覆盖
ENV SQLITE_PATH=/data/reviews.db
ENV RAG_DB_PATH=/data/rag.db
ENV PORT=8080

EXPOSE 8080

# 健康检查走 /api/health；indexrepo 是后台跑，不阻塞 server，所以 start-period 不用拉长
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/api/health || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
