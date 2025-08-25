# 使用官方Go镜像作为构建阶段
FROM golang:1.21-alpine AS builder

# 设置工作目录
WORKDIR /app

# 复制go mod文件
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY src/ ./src/

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o pvc-timing-webhook src/main.go

# 使用alpine作为运行阶段
FROM alpine:latest

# 安装ca-certificates用于HTTPS
RUN apk --no-cache add ca-certificates

# 创建非root用户
RUN addgroup -g 1001 -S webhook && \
    adduser -u 1001 -S webhook -G webhook

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/pvc-timing-webhook .

# 创建证书目录
RUN mkdir -p /etc/webhook/certs && \
    chown -R webhook:webhook /app /etc/webhook

# 切换到非root用户
USER webhook

# 暴露端口
EXPOSE 8080

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider https://localhost:8080/healthz || exit 1

# 运行应用
CMD ["./pvc-timing-webhook"]
