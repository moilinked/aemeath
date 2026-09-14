# Linux 上用 Docker 跑 Chat Agent

PostgreSQL 已在宿主机 `5432` 运行，Compose **只**启动后端。容器监听 `9998`，默认只绑本机，由 Nginx 反代对外。

## 1. 准备 `.env`

仓库根目录复制 `.env.example` 为 `.env`，填入真实配置。不要提交 `.env`。

访问宿主机数据库时，把 `DATABASE_URL` 的主机名改成 `host.docker.internal`，不要用 `127.0.0.1`（那是容器自己）：

```text
DATABASE_URL=postgres://chat_agent:replace-with-db-password@host.docker.internal:5432/chat_agent?sslmode=disable
SERVER_PORT=9998
```

`pg_hba.conf` 需允许 Docker 网桥访问 `5432`（常见为 `172.16.0.0/12`）。

## 2. 启动

```bash
docker compose up -d --build
curl -sS http://127.0.0.1:9998/healthz
```

预期 `{"status":"ok"}`。

## 3. 日志地址

应用日志走 stdout，不要在容器里写文件。

| 来源 | 地址 |
| --- | --- |
| 容器日志 | `docker logs -f chat-agent` |
| Docker json-file | `docker inspect --format='{{.LogPath}}' chat-agent`（通常在 `/var/lib/docker/containers/<id>/<id>-json.log`） |
| Nginx access | `/var/log/nginx/chat-agent.access.log` |
| Nginx error | `/var/log/nginx/chat-agent.error.log` |

## 4. Nginx 反代

`/etc/nginx/sites-available/chat-agent`：

```nginx
server {
    listen 443 ssl;
    server_name chat.example.com;

    access_log /var/log/nginx/chat-agent.access.log;
    error_log  /var/log/nginx/chat-agent.error.log warn;

    location / {
        proxy_pass http://127.0.0.1:9998;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

```bash
sudo ln -sfn /etc/nginx/sites-available/chat-agent /etc/nginx/sites-enabled/chat-agent
sudo nginx -t
sudo systemctl reload nginx
```

安全组只放行 80/443。不要对公网开放 `9998` 或 `5432`。
