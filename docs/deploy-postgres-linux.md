# Linux 服务器部署 PostgreSQL

本文说明如何在 Linux 上安装 PostgreSQL 16，并让本机运行的 Chat Agent 通过 `DATABASE_URL` 远程连接。应用启动时会自动执行迁移并写入引导用户，**不必**在服务器上手动建 `users` / `sessions` 表。

以下以 Ubuntu 22.04 / 24.04 为例。命令需在服务器上以 `root` 或具有 `sudo` 的用户执行。

## 1. 安装 PostgreSQL 16

```bash
sudo apt-get update
sudo apt-get install -y postgresql postgresql-contrib
sudo systemctl enable --now postgresql
sudo systemctl status postgresql --no-pager
```

确认服务为 `active (running)`。

## 2. 创建数据库与业务用户

不要用 `postgres` 超级用户给应用连库。将下面的密码换成足够长的随机值，不要使用文档中的占位符。

```bash
sudo -u postgres psql <<'SQL'
CREATE USER chat_agent WITH PASSWORD 'replace-with-strong-password';
CREATE DATABASE chat_agent OWNER chat_agent;
GRANT ALL PRIVILEGES ON DATABASE chat_agent TO chat_agent;
SQL
```

PostgreSQL 15+ 还需要允许该用户在 `public` schema 建表（应用启动时会跑迁移）：

```bash
sudo -u postgres psql -d chat_agent -c 'GRANT ALL ON SCHEMA public TO chat_agent;'
```

## 3. 允许远程连接

编辑 `postgresql.conf`（路径按实际版本调整，可用 `sudo -u postgres psql -c 'SHOW config_file;'` 查看）：

```bash
sudo sed -i "s/^#listen_addresses = 'localhost'/listen_addresses = '*'/" /etc/postgresql/16/main/postgresql.conf
```

若文件里本来就没有注释掉的那一行，改为手动设置：

```text
listen_addresses = '*'
```

编辑 `pg_hba.conf`，只允许应用所在机器访问。将 `APP_HOST_IP` 换成你运行 `go run` / 部署 Chat Agent 的那台电脑或服务器公网 IP：

```text
# TYPE  DATABASE     USER        ADDRESS            METHOD
host    chat_agent   chat_agent  APP_HOST_IP/32     scram-sha-256
```

不要写成 `0.0.0.0/0`，除非只是在隔离网络里做临时联调。

重启：

```bash
sudo systemctl restart postgresql
```

## 4. 防火墙

UFW 示例（同样只放行应用机器 IP）：

```bash
sudo ufw allow from APP_HOST_IP to any port 5432 proto tcp
sudo ufw status
```

云厂商安全组也要放行 **入站 TCP 5432**，来源限制为应用 IP，不要对全网开放。

## 5. 从本机验证

在运行 Chat Agent 的机器上：

```bash
psql "postgres://chat_agent:replace-with-strong-password@POSTGRES_HOST:5432/chat_agent?sslmode=disable"
```

能进入 `psql` 即表示网络、账号和 `pg_hba.conf` 已通。当前默认未强制 SSL；若服务器已配置证书，将 `sslmode` 改为 `require`。

## 6. 配置项目连接串

复制 `.env.example` 为 `.env` 后，把 `DATABASE_URL` 改成远程地址：

```dotenv
DATABASE_URL=postgres://chat_agent:replace-with-strong-password@POSTGRES_HOST:5432/chat_agent?sslmode=disable
```

`POSTGRES_HOST` 填 Linux 服务器公网 IP 或域名。密码中若含 `@`、`#`、`%` 等字符，需要做 URL 编码。

然后启动应用：

```powershell
go run -buildvcs=false ./cmd/server
```

启动成功后，服务器上应能看到迁移写入的表：

```bash
sudo -u postgres psql -d chat_agent -c '\dt'
```

预期包含 `schema_migrations`、`users`、`sessions`。

## 7. 常见问题

| 现象 | 处理 |
| --- | --- |
| `connection refused` | 检查 `listen_addresses`、进程是否监听 `0.0.0.0:5432`、安全组 / UFW |
| `no pg_hba.conf entry` | 应用出口 IP 与 `pg_hba.conf` 中的 `APP_HOST_IP` 不一致（注意 NAT） |
| `password authentication failed` | 用户密码与 `DATABASE_URL` 不一致，或仍在用 `postgres` 用户 |
| `permission denied for schema public` | 补做第 2 步的 `GRANT ALL ON SCHEMA public` |
| 本机能 SSH 但连不上 5432 | SSH 通不代表数据库端口通，需要单独放行 5432 |

查看监听：

```bash
ss -lntp | grep 5432
```

## 安全建议

- 业务密码使用随机强密码，不要提交到 Git。
- `pg_hba.conf` 按 IP 收紧，不要对 `0.0.0.0/0` 开放。
- 生产环境优先启用 SSL，并将 `sslmode` 设为 `require`。
- 引导登录账号仍通过 `AUTH_USERNAME` / `AUTH_PASSWORD_HASH` 配置，不要把登录密码明文写入数据库或文档。
