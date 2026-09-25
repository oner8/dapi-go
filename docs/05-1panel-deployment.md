# Docker / 1Panel 部署

公开镜像 `ghcr.io/oner8/dapi-go:latest` 可直接拉取，无需克隆仓库或登录 GHCR。目前镜像支持 `linux/amd64`。服务器需安装 Docker 和 Docker Compose 插件。

在服务器的私有目录创建 `compose.yaml`：

```yaml
services:
  dapi-go:
    image: ghcr.io/oner8/dapi-go:latest
    restart: unless-stopped
    environment:
      YKT_BASE_URL: ${YKT_BASE_URL:?required}
      YKT_ACCOUNT: ${YKT_ACCOUNT:?required}
      YKT_PASSWORD: ${YKT_PASSWORD:?required}
      YKT_API_KEY: ${YKT_API_KEY:?required}
      API_PROXY_KEY: ${API_PROXY_KEY:-}
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - dapi-go-data:/data

volumes:
  dapi-go-data:
```

启动 Compose 的进程必须能读取 `YKT_BASE_URL`、`YKT_ACCOUNT`、`YKT_PASSWORD`、`YKT_API_KEY` 环境变量；在 1Panel 等面板中，应将它们配置为编排模板变量，供 Compose 解析 `${...}`。仅设置容器启动后的环境变量，无法完成 Compose 变量替换。`YKT_BASE_URL` 填上游的 `http(s)` 根地址，不带路径、账号或查询参数。`YKT_API_KEY` 是调用本服务时使用的 Bearer 密钥。可选的 `API_PROXY_KEY` 留空即关闭网关级额外鉴权；设置时至少 16 个字符。不要把这些值直接写入公开的 Compose 文件、命令历史或文档。

程序无需 `.env` 文件：镜像默认运行 `serve`，直接读取容器环境变量。其他可配置变量包括 `API_PROXY_LISTEN`、`YKT_TOKEN_FILE`、`YKT_REFRESH_MARGIN`、`YKT_TIMEOUT`、`YKT_MAX_REQUEST_BYTES`；未配置时使用程序默认值。若修改 `API_PROXY_LISTEN` 的端口，也要同步修改 Compose 的容器端口和健康检查。`/data` 命名卷保存含有效凭证的 token 缓存，备份时应按凭证保护。

启动和检查：

```sh
docker compose up -d
docker compose ps
curl http://127.0.0.1:8080/health
```

`/health` 返回 `{"status":"ok"}` 只说明服务进程正常。检查上游时，访问 `/ykt/health` 并提供 `Authorization: Bearer <YKT_API_KEY>`；若设置了 `API_PROXY_KEY`，还需提供 `X-Api-Key: <API_PROXY_KEY>`。不要把真实密钥写入共享的命令记录。

示例只将端口绑定到服务器本机。公网访问请配置 HTTPS 反向代理；若反向代理运行在另一容器中，应改用同一 Docker 网络中的服务地址，而非容器内的 `127.0.0.1`。

更新公开镜像：

```sh
docker compose pull
docker compose up -d
```

如需从源码自行构建，仓库根目录的 `docker-compose.yml` 仍提供本地构建方案，使用 `.env` 文件和 `docker compose up -d --build`。
