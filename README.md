# dapi-go

dapi-go 是一个 Go 网关：登录配置的 ykt 上游、缓存并自动刷新 token，并向 `/ykt/api/*` 代理业务请求。上游的 `Token` 请求头由网关注入，调用方不需要持有上游账号或 token。

## 配置与运行

复制 `.env.example` 为 `.env`，填写 `YKT_BASE_URL`、`YKT_API_KEY`，以及登录时需要的 `YKT_ACCOUNT`、`YKT_PASSWORD`。上游地址没有内置默认值，必须是无用户名、密码、路径和查询参数的 `http(s)` 地址。已有有效缓存时，服务可以暂时不使用账号密码；需要重新登录时仍须提供。token 缓存含有效凭证，文件权限为 `0600`。

```sh
go run ./cmd/dapi-go -env .env serve
```

环境变量优先于 `-env` 指定的文件。`API_PROXY_LISTEN` 默认 `:8080`；`YKT_TOKEN_FILE` 默认 `ykt-token.json`，迁移旧缓存时可以显式指向原文件。`YKT_TIMEOUT`、`YKT_REFRESH_MARGIN` 单位为秒；`YKT_MAX_REQUEST_BYTES` 默认 16777216 字节。

CLI 还提供 `login ykt`（强制登录）和 `decode ykt`（只显示 token 元数据）。`decode` 只需缓存文件；`serve` 和 `login` 必须配置 `YKT_BASE_URL`。

## HTTP 接口与鉴权

所有 `/ykt/*` 请求都要带 `Authorization: Bearer <YKT_API_KEY>`。如果设置了可选的 `API_PROXY_KEY`，还要带 `X-Api-Key: <API_PROXY_KEY>`。不要向网关提交上游的 `Token` 头。

| 路径 | 用途 |
| --- | --- |
| `GET /` | 公开的服务名称、状态和健康检查入口 |
| `GET /health` | 不含凭证和上游详情的公开进程探针 |
| `GET /ykt/health` | 需鉴权的上游状态 |
| `GET /ykt/token` | 需鉴权的上游 token，响应禁止缓存 |
| `/ykt/api/*` | 需鉴权的业务接口代理，对应上游 `/api/*` |

```sh
curl -H "Authorization: Bearer $YKT_API_KEY" http://127.0.0.1:8080/ykt/health
```

启用 `API_PROXY_KEY` 时，再加 `-H "X-Api-Key: $API_PROXY_KEY"`。不要把密钥放入 URL、日志或公开的命令示例。公网访问请使用 HTTPS 反向代理。公开 GHCR 镜像、纯环境变量 Compose 和 1Panel 的用法见 [部署说明](docs/05-1panel-deployment.md)。

调用 `/ykt/api/basic/findDataAreaBoard` 时，网关使用 `/transactionDetail` 作为上游 `Referer`，并补齐与浏览器一致的请求元数据。调用方只需提供网关鉴权头；上游 `Token` 仍由网关注入。`Connection` 等连接级请求头由 Go HTTP 客户端管理。浏览器请求头不能保证复现浏览器的 TLS 特征或绕过上游的风控。

## 登录排查

若日志显示 `captcha endpoint returned 3xx`，表示验证码接口发生重定向。检查 `YKT_BASE_URL` 是否为上游实际使用的 HTTPS 根地址；不要把登录页或 `/api` 路径写入该变量。验证码步骤的日志只记录错误类别或 HTTP 状态码，不记录账号、验证码和响应内容。
