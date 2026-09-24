# ---- 构建阶段 ----
FROM golang:1.27-alpine AS builder

WORKDIR /build

# 先拷贝依赖清单，利用层缓存
COPY go.mod go.sum ./
RUN go mod download

# 再拷贝源码并编译（CGO 关闭，纯静态）
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dapi-go ./cmd/dapi-go

# ---- 运行阶段 ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S app -G app

COPY --from=builder /out/dapi-go /usr/local/bin/dapi-go

# 非 root 运行；token 文件挂卷持久化
WORKDIR /data
RUN chown -R app:app /data
USER app

ENV TZ=Asia/Shanghai

VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=15s \
  CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["dapi-go"]
CMD ["serve"]
