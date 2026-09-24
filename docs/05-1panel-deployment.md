# 1Panel 部署

把项目放在服务器的私有目录。复制 `.env.example` 为 `.env`，填写 `YKT_*` 配置，并将 `.env` 权限设为 `0600`。`YKT_BASE_URL` 没有内置上游地址。不要提交 `.env` 或 token 缓存。

在 1Panel 中从项目的 `docker-compose.yml` 创建编排。示例仅将宿主机端口绑定到 `127.0.0.1`，并用 `dapi-go-data` 命名卷保存 token。需要更换宿主机端口时，在 `.env` 中设置 `DAPI_PORT`。

启动后检查进程探针：

```sh
curl http://127.0.0.1:8080/health
```

查看上游状态时，访问 `/ykt/health` 并提供 `Authorization: Bearer <YKT_API_KEY>`。如果设置了 `API_PROXY_KEY`，还需提供 `X-Api-Key: <API_PROXY_KEY>`。不要把真实密钥写进命令历史或文档。

对外访问时，在 1Panel 配置 HTTPS 反向代理，连接到本地 Compose 端口；不要直接将容器端口暴露到公网。如果反向代理运行在另一容器中，请使用该部署环境中实际可达的 Docker 网络地址。

修改代码或配置后，可运行 `docker compose up -d --build` 重建。命名卷会保留 token 缓存；它和 `.env` 都应按凭证保护。
