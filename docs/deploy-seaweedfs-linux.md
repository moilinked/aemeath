# Linux 服务器部署 SeaweedFS

本文说明如何在单台 Linux 云主机上部署 SeaweedFS，用于存放图片和 Markdown 等小文件。不部署集群，不暴露管理端口到公网。

以下以 Ubuntu 22.04 / 24.04、x86_64 为例。命令需在服务器上以 `root` 或具有 `sudo` 的用户执行。

国内云主机访问 GitHub 经常中断。二进制请优先在能打开 GitHub 的电脑上下载，再传到服务器。

## 1. 适用场景与组件

单机使用 `weed server -filer -s3`，同一进程内启动：

| 组件   | 本机端口 | 用途                                          |
| ------ | -------- | --------------------------------------------- |
| Master | `9333`   | 集群元数据，仅本机访问                        |
| Volume | `8080`   | 实际文件块，仅本机访问                        |
| Filer  | `8888`   | 路径式文件，如 `/notes/a.md`、`/images/a.png` |
| S3     | `8333`   | 兼容 S3 API，便于 SDK / 预签名 URL            |

起步推荐走 **Filer HTTP**。以后要用 AWS SDK 时再开 S3。

单机默认副本为 `000`：磁盘损坏即丢数据。重要文件需另做备份。公网只开放 `22`、`80`、`443`。

建议配置：2 核 4 GB 内存、60～100 GB SSD。数据目录单独挂盘，不要写系统盘根分区。

## 2. 准备目录

```bash
sudo mkdir -p /opt/seaweedfs /data/seaweedfs /etc/seaweedfs
```

如果数据盘挂在 `/mnt/data`：

```bash
sudo mkdir -p /mnt/data/seaweedfs
sudo ln -sfn /mnt/data/seaweedfs /data/seaweedfs
```

## 3. 安装二进制

版本号按需替换。下面以 `4.46` 为例。

### 3.1 本机下载后上传（推荐）

在能访问 GitHub 的电脑上下载：

```text
https://github.com/seaweedfs/seaweedfs/releases/download/4.46/linux_amd64.tar.gz
```

传到服务器：

```powershell
scp .\linux_amd64.tar.gz deploy@SERVER_IP:/tmp/
```

服务器上安装：

```bash
cd /tmp
ls -lh linux_amd64.tar.gz
tar -tzf linux_amd64.tar.gz
sudo tar -xzf linux_amd64.tar.gz -C /opt/seaweedfs
sudo chmod +x /opt/seaweedfs/weed
/opt/seaweedfs/weed version
```

压缩包大约 40MB 以上。只有几 KB 多半是 HTML 错误页，不要解压。

### 3.2 服务器直连 GitHub（经常失败）

国内云主机直连 GitHub 可能出现 `HTTP/2 stream 1 was not closed cleanly: PROTOCOL_ERROR`。可先试 HTTP/1.1 断点续传：

```bash
cd /tmp
curl --http1.1 -L --retry 20 --retry-all-errors --retry-delay 3 \
  -C - -o linux_amd64.tar.gz \
  https://github.com/seaweedfs/seaweedfs/releases/download/4.46/linux_amd64.tar.gz
```

仍失败时再试镜像（地址会变，哪个通就用哪个）：

```bash
URL="https://github.com/seaweedfs/seaweedfs/releases/download/4.46/linux_amd64.tar.gz"
curl --http1.1 -L --retry 10 -C - -o linux_amd64.tar.gz "https://ghproxy.net/${URL}"
```

下完同样先 `tar -tzf` 再安装。

## 4. 配置 S3 鉴权

不配密钥时，S3 默认允许所有人访问。公网服务器必须配置身份。

将下面的密钥换成足够长的随机值，不要使用文档中的占位符：

```bash
sudo tee /etc/seaweedfs/s3.json >/dev/null <<'EOF'
{
  "identities": [
    {
      "name": "admin",
      "credentials": [
        {
          "accessKey": "replace-with-access-key",
          "secretKey": "replace-with-secret-key"
        }
      ],
      "actions": ["Admin", "Read", "Write", "List"]
    }
  ]
}
EOF
sudo chmod 600 /etc/seaweedfs/s3.json
```

密钥不要提交到 Git，也不要写进公开文档。

## 5. systemd 常驻

```bash
sudo tee /etc/systemd/system/seaweedfs.service >/dev/null <<'EOF'
[Unit]
Description=SeaweedFS
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/seaweedfs/weed server \
  -ip=127.0.0.1 \
  -ip.bind=127.0.0.1 \
  -dir=/data/seaweedfs \
  -master.volumeSizeLimitMB=1024 \
  -volume.max=0 \
  -filer \
  -s3 \
  -s3.port=8333 \
  -s3.config=/etc/seaweedfs/s3.json
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now seaweedfs
sudo systemctl status seaweedfs --no-pager
```

确认服务为 `active (running)`。

| 参数                             | 作用                                                      |
| -------------------------------- | --------------------------------------------------------- |
| `-ip.bind=127.0.0.1`             | 只监听本机，由 Nginx 对外                                 |
| `-dir`                           | 数据目录                                                  |
| `-master.volumeSizeLimitMB=1024` | 单 Volume 1 GB。默认约 30 GB，小磁盘容易装不下多个 Volume |
| `-volume.max=0`                  | 按剩余磁盘自动创建 Volume                                 |
| `-filer`                         | 启用路径式文件接口                                        |
| `-s3.config`                     | S3 身份配置                                               |

查看日志：

```bash
sudo journalctl -u seaweedfs -f
```

## 6. 本机验证

```bash
ss -lntp | grep -E '8888|8333|9333|8080'
curl -sS http://127.0.0.1:8888/
echo 'hello seaweed' > /tmp/hello.md
curl -F file=@/tmp/hello.md http://127.0.0.1:8888/notes/hello.md
curl -sS http://127.0.0.1:8888/notes/hello.md
```

预期能读回 `hello seaweed`。

## 7. Nginx 反代 HTTPS

Filer 默认偏开放。上公网后至少加一层 Basic Auth，或只允许应用服务器 IP 访问，由后端代传文件。

安装 Nginx 与证书（以 Certbot 为例，域名换成实际值）：

```bash
sudo apt-get update
sudo apt-get install -y nginx
sudo certbot --nginx -d files.example.com
```

`/etc/nginx/sites-available/seaweedfs`：

```nginx
server {
    listen 443 ssl;
    server_name files.example.com;

    client_max_body_size 32m;

    # 可选：限制来源 IP
    # allow APP_HOST_IP;
    # deny all;

    location / {
        proxy_pass http://127.0.0.1:8888;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

```bash
sudo ln -sfn /etc/nginx/sites-available/seaweedfs /etc/nginx/sites-enabled/seaweedfs
sudo nginx -t
sudo systemctl reload nginx
```

云厂商安全组只放行 **入站 TCP 80 / 443**。不要对全网开放 `8080`、`8333`、`8888`、`9333`。

大陆域名公开服务通常需要 ICP 备案。

## 8. 上传与下载

### Filer HTTP

```bash
curl -F file=@note.md http://127.0.0.1:8888/notes/note.md
curl -F file=@cover.png http://127.0.0.1:8888/images/cover.png
curl http://127.0.0.1:8888/notes/?pretty=y
```

对外 URL 示例：

```text
https://files.example.com/notes/note.md
https://files.example.com/images/cover.png
```

### S3 API

```bash
export AWS_ACCESS_KEY_ID=replace-with-access-key
export AWS_SECRET_ACCESS_KEY=replace-with-secret-key
export AWS_DEFAULT_REGION=us-east-1

aws --endpoint-url http://127.0.0.1:8333 s3 mb s3://assets
aws --endpoint-url http://127.0.0.1:8333 s3 cp note.md s3://assets/notes/note.md
aws --endpoint-url http://127.0.0.1:8333 s3 cp cover.png s3://assets/images/cover.png
```

应用里把 S3 endpoint 指到本机 `127.0.0.1:8333` 或内网地址即可。不要把 AccessKey / SecretKey 下发给浏览器。

## 9. 和 Chat Agent 对接建议

1. 浏览器不要直接访问 SeaweedFS。
2. 由 Chat Agent 提供上传接口，服务端再转发到 Filer 或 S3。
3. 对外只返回自己的 HTTPS URL。

密钥放在服务器环境变量或密钥管理服务中，不要写入 `.env.example`。

## 10. 备份与升级

备份数据目录：

```bash
sudo systemctl stop seaweedfs
sudo tar -C /data -czf /tmp/seaweedfs-$(date +%Y%m%d).tar.gz seaweedfs
sudo systemctl start seaweedfs
```

把归档拷到对象存储或另一块盘。单机没有副本，这是主要容灾手段。

升级只替换二进制：

```bash
sudo systemctl stop seaweedfs
sudo mv /opt/seaweedfs/weed /opt/seaweedfs/weed.bak
sudo tar -xzf linux_amd64.tar.gz -C /opt/seaweedfs
sudo chmod +x /opt/seaweedfs/weed
/opt/seaweedfs/weed version
sudo systemctl start seaweedfs
```

## 11. 常见问题

| 现象                                         | 处理                                         |
| -------------------------------------------- | -------------------------------------------- |
| `PROTOCOL_ERROR` / GitHub 下载到 90% 失败    | 本机下载后 `scp`，或 HTTP/1.1 + 镜像         |
| `tar: This does not look like a tar archive` | 下到的是错误页，检查文件大小后重下           |
| 服务启动后端口不在 `127.0.0.1`               | 确认 `-ip.bind=127.0.0.1`                    |
| 磁盘很快写满 / Volume 创建失败               | 减小 `-master.volumeSizeLimitMB`，例如 `512` |
| 公网能直接打开 `8888`                        | 安全组未收紧，或 systemd 未绑定本机          |
| S3 匿名可读写                                | 未配置 `-s3.config`，补密钥后重启            |

查看监听地址：

```bash
ss -lntp | grep weed
```

预期类似 `127.0.0.1:8888`，而不是 `0.0.0.0:8888`。
