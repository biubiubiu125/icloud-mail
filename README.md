# icloud-mail

单管理员的 iCloud 隐私邮箱接码服务。管理员在本机打开管理页，维护 Apple 账号和隐私邮箱。外部程序只走接码接口，不登录管理页。

仓库：<https://github.com/biubiubiu125/icloud-mail>

镜像：`ghcr.io/biubiubiu125/icloud-mail`

## 先看这几条

- 只能跑一个进程。账号锁在进程内存里，不要开第二个容器，也不要 `--scale`。
- 数据库只用 PostgreSQL。表结构在启动时自动迁移。
- 每个 Apple 账号上的代理是这个账号的唯一出口。代理留空就是直连，不会用旧登录态里的代理补上。
- 代理只支持 `http`、`https`、`socks5`、`socks5h`，必须带主机，不能带路径、查询参数或片段。
- 管理登录只收用户名和密码。多传代理等字段会被拒绝，不是忽略。
- 不要设置 `IPM_DEBUG_APPLE_ACCOUNT`。设成 `1` 时，协议层会把供应商响应打到标准错误。
- 创建邮箱如果远端已经成功、本地却没落稳，这个账号会停止继续创建。停止记录在持久卷里，不要删卷假装没有发生。
- 默认只监听本机 `127.0.0.1:8080`。管理页和接码接口是同一个端口，不要直接公开到公网。

## 服务器搭建

下面按 Linux 服务器、root 用户来写。不写 Docker 安装，也不写 Nginx 和证书。Docker Engine 和 Compose 插件需要已经装好。

部署目录用 `/opt/icloud-mail`。数据库不映射到宿主机，只在本项目的容器网络里访问。

### 1. 检查

```bash
whoami
docker --version
docker compose version
ss -lntp | grep ':8080' || echo '8080 空闲'
```

`whoami` 应是 `root`。`8080` 已被占用时，先停掉占用的程序，不要改成本教程以外的端口后再对不上命令。

### 2. 取代码

```bash
git clone https://github.com/biubiubiu125/icloud-mail.git /opt/icloud-mail
cd /opt/icloud-mail
```

目录里已经有 `docker-compose.yml`。不要另写一份，避免和环境变量对不上。

### 3. 写环境变量

用户名至少 3 位，密码至少 8 位。数据库密码和接码密钥用十六进制，不要加 `@`、`:`、`/` 这类符号，否则数据库地址会被截断。

这两个值只在第一次写入空库时生效。以后改 `.env` 不会改已经建好的管理员密码，也不会轮换已经写入的接码密钥。

```bash
cd /opt/icloud-mail
umask 077
db_password="$(openssl rand -hex 24)"
api_key="$(openssl rand -hex 24)"
cat > .env <<EOF
POSTGRES_PASSWORD=${db_password}
ICLOUD_MAIL_ADMIN_USERNAME=admin
ICLOUD_MAIL_ADMIN_PASSWORD=$(openssl rand -hex 24)
ICLOUD_MAIL_API_KEY=${api_key}
ICLOUD_MAIL_PUBLIC_BASE_URL=
ICLOUD_MAIL_COOKIE_SECURE=false
EOF
chmod 600 .env
```

把管理员密码单独留下来。`.env` 不会进 Git，丢了就只能按文末的办法重置。

```bash
grep '^ICLOUD_MAIL_ADMIN_PASSWORD=' /opt/icloud-mail/.env
```

`ICLOUD_MAIL_PUBLIC_BASE_URL` 留空。它如果以 `https://` 开头，登录 Cookie 会带 Secure，本机 `http://127.0.0.1:8080` 就登不进去。对外地址以后到管理页的「设置」里填，那个值只影响接口返回的链接。

### 4. 启动

```bash
cd /opt/icloud-mail
docker compose pull
docker compose up -d
docker compose ps
```

`pull` 如果返回未授权，说明镜像包还不能匿名拉取。在同一目录本地构建，标签必须和 compose 里的镜像名一致：

```bash
cd /opt/icloud-mail
docker build -t ghcr.io/biubiubiu125/icloud-mail:latest .
docker compose up -d
docker compose ps
```

应用服务已经是 `restart: unless-stopped`。仓库里的 `postgres` 服务没有这项。希望服务器重启后数据库也起来，在 `docker-compose.yml` 的 `postgres:` 下加上：

```yaml
    restart: unless-stopped
```

然后重新执行 `docker compose up -d`。

第一次启动后，把停止记录目录交给容器用户，否则创建失败时的停止文件写不进去：

```bash
cd /opt/icloud-mail
docker compose exec -u 0 icloud-mail chown 65532:65532 /var/lib/icloud-mail
```

### 5. 确认服务起来了

```bash
curl -fsS http://127.0.0.1:8080/api/v1/health
docker compose logs --tail 50 icloud-mail
```

健康检查应返回：

```json
{"success":true}
```

日志里应有 `listening`。如果是 `seed failed`，多半是用户名或密码长度不够，改 `.env` 后执行 `docker compose up -d`。这时库里还没有管理员，改环境变量仍然有效。

## 打开管理页

管理页只给管理员用。在自己的电脑上做 SSH 转发，不要把 `8080` 映射到 `0.0.0.0`。

```bash
ssh -L 8080:127.0.0.1:8080 root@服务器IP
```

本机 `8080` 已被占用时，把左边改成别的端口，例如 `ssh -L 18080:127.0.0.1:8080 root@服务器IP`，浏览器打开 `http://127.0.0.1:18080`。

浏览器打开 `http://127.0.0.1:8080`，用 `.env` 里的用户名和密码登录。页面标题是 iCloud Mail。

### 添加 Apple 账号

在工作台填写名称、Apple ID、密码。代理可空。要走代理时写成完整地址，例如：

```text
socks5://用户:密码@主机:端口
http://主机:端口
```

没有 `://` 的地址会被拒绝。代理字段全部留空表示直连。改 Apple ID 会清掉这个账号已经保存的登录态。已存密码留空再保存，会把密码清掉。

### 登录并保存取码

点开账号，按实际要用的通道登录：

- 用 iCloud 网页创建邮箱：点「Web 登录」。要验证码时填上，再点「提交 Web 验证码」。
- 用 Apple Account 创建邮箱：点「Apple 登录」，需要时再提交验证码。设置里的 Apple API Key 留空不会清掉已有值；只有登录态里没有密钥时才需要另填。

取验证码默认走 IMAP。在同一张卡片里填写 iCloud 邮箱和 App 专用密码，点「保存 IMAP」。没保存 IMAP、又没在设置里打开「Web 取码兜底」时，接码会返回「未保存 IMAP 取码登录」。

### 准备可领取的邮箱

登录成功后，通道保持「自动」，点「创建一个邮箱」。也可以在「设置」里打开「自动创建」。自动创建默认关闭，间隔至少 30 秒，每轮最多 20 个。

账号显示「待核对」时，先到 iCloud 看这个邮箱是否已经创建出来，再点「我已在 iCloud 核对，清除待核对」。不要删 Docker 卷来跳过这一步。

公开接口默认开启。接码密钥已经由环境变量写入；设置页把「新的接口密钥」留空保存，不会清空现有密钥。要换密钥时，在这里填新值。

「对外地址」可以填外部程序访问本服务的根地址，例如 `http://127.0.0.1:8080`。它只用来拼领取结果里的 `api_url`。留空时 `api_url` 是相对路径，调用方自己补上服务地址。

后台取信默认开启，大约 60 秒一轮。默认关键词是 `OpenAI`。租约默认 30 分钟。邮件默认保留 90 天。

导出选中邮箱会下载带密钥的 `mailbox-apis.json`。这个文件不要提交，也不要发给不需要接码的人。

删除邮箱默认会删远端。远端没确认成功时，本地会留在删除中。服务重启后，中断的删除会变成未知，再次删除必须在页面上明确确认。

## 外部程序怎么接码

同一台服务器上的程序访问 `http://127.0.0.1:8080`。全局密钥放在 `X-API-Key`。也接受 `Authorization: Bearer`，或查询参数 `api_key`，优先用请求头。

接码接口不认管理员 Cookie。管理页那个 `X-Requested-With` 头，外部程序不用带。

下面先读出密钥，再调用。`.env` 的值不要有空格。

```bash
cd /opt/icloud-mail
set -a
. ./.env
set +a
```

领取一个未使用邮箱。`request_id` 每次新领取都要换。同一个 `project` 加上同一个 `request_id` 再请求，会回到上一次那一笔，不会再拿一个新邮箱。`ttl_minutes` 不传时用设置里的租约分钟，最长 10080。

```bash
curl -sS -X POST http://127.0.0.1:8080/api/v1/mailboxes/claim \
  -H "X-API-Key: ${ICLOUD_MAIL_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"project":"demo","purpose":"signup","request_id":"demo-001","ttl_minutes":30}'
```

成功时看 `email`、`api_token` 和 `lease.id`。池子空时 HTTP 状态仍是 200，正文是：

```json
{"success":false,"code":"no_available_mailbox","error":"没有可领取的邮箱"}
```

调用方必须看 `code`，不能只看 HTTP 状态。

取验证码用邮箱自己的密钥，不使用全局密钥。地址里的 `@` 写成 `%40`。`wait_ms` 最长 30000。不传 `keyword` 时用设置里的默认关键词。

```bash
curl -sS -G 'http://127.0.0.1:8080/api/v1/mailboxes/邮箱%40icloud.com/code' \
  --data-urlencode 'key=领取结果里的 api_token' \
  --data-urlencode 'wait_ms=15000'
```

还没有匹配邮件时返回 404，`code` 是 `code_not_found`。只要这次发送之后的邮件时，加 `after`，值必须是 RFC3339 时间。

用完后确认租约。确认后邮箱保持已使用，不会回到池子：

```bash
curl -sS -X POST "http://127.0.0.1:8080/api/v1/mailbox-leases/租约ID/commit" \
  -H "X-API-Key: ${ICLOUD_MAIL_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{}'
```

没用完就放开。持有中的租约放开后会回到池子：

```bash
curl -sS -X POST "http://127.0.0.1:8080/api/v1/mailbox-leases/租约ID/release" \
  -H "X-API-Key: ${ICLOUD_MAIL_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"return_to_pool":true}'
```

已经确认过的租约，只有 `return_to_pool` 为 true 才会回到池子。续期只对未过期的持有租约有效：

```bash
curl -sS -X POST "http://127.0.0.1:8080/api/v1/mailbox-leases/租约ID/renew" \
  -H "X-API-Key: ${ICLOUD_MAIL_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"ttl_minutes":30}'
```

按邮箱地址做确认、放开、续期时，路径是 `/api/v1/mailboxes/邮箱/commit|release|renew`。这条可以用全局密钥，也可以用邮箱密钥。按租约 ID 的那三条只认全局密钥。

按邮箱查询不返回邮箱密钥：

```bash
curl -sS -X POST http://127.0.0.1:8080/api/v1/mailboxes/lookup \
  -H "X-API-Key: ${ICLOUD_MAIL_API_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"emails":["name@icloud.com"]}'
```

一次查询 1 到 500 个地址。

## 更新、备份和停止

拉镜像部署的，更新用：

```bash
cd /opt/icloud-mail
git pull
docker compose pull
docker compose up -d
curl -fsS http://127.0.0.1:8080/api/v1/health
```

当初是本地构建的，不要只 pull。重新构建同一个标签后再启动：

```bash
cd /opt/icloud-mail
git pull
docker build -t ghcr.io/biubiubiu125/icloud-mail:latest .
docker compose up -d
```

备份放到仓库目录外面：

```bash
mkdir -p /root/icloud-mail-backup
docker compose -f /opt/icloud-mail/docker-compose.yml exec -T postgres \
  pg_dump -U icloud_mail -d icloud_mail \
  > "/root/icloud-mail-backup/icloud-mail-$(date +%F).sql"
```

停止但保留数据：

```bash
cd /opt/icloud-mail
docker compose stop
```

再启动用 `docker compose up -d`。不要执行 `docker compose down -v`，那会删掉数据库和创建停止记录。

看日志：

```bash
cd /opt/icloud-mail
docker compose logs --tail 100 icloud-mail
docker compose logs --tail 100 postgres
```

容器日志时间默认是 UTC。

## 忘记管理员密码

环境变量改不了已经写入的密码。确认 `.env` 里现在就是要使用的用户名和密码，再删掉管理员行。邮箱、账号和接码密钥不会被这句删掉。

```bash
cd /opt/icloud-mail
docker compose exec -T postgres \
  psql -U icloud_mail -d icloud_mail -c 'DELETE FROM admins;'
docker compose up -d
curl -fsS http://127.0.0.1:8080/api/v1/health
```

`up -d` 会按当前 `.env` 重新创建进程，并写入这一个管理员。只执行 `restart` 不会读到刚改的密码。旧登录会话会一起失效。

## 不用 Docker 时

复制 `config.example.json` 为 `config.json`，或只设环境变量。环境变量优先。数据库必须是 PostgreSQL。没有管理员、又没设用户名和密码时，只能从本机打开管理页创建唯一管理员；服务不在本机监听时，用环境变量创建。

```bash
export ICLOUD_MAIL_DATABASE_URL='postgres://icloud_mail:change-me@127.0.0.1:5432/icloud_mail?sslmode=disable'
export ICLOUD_MAIL_ADMIN_USERNAME='admin'
export ICLOUD_MAIL_ADMIN_PASSWORD='change-me-now'
export ICLOUD_MAIL_API_KEY='change-me-api-key'
export ICLOUD_MAIL_HTTP_ADDR=':8080'
go run ./cmd/icloud-mail
```

开发检查：

```bash
go test -count=1 ./...
```

没设置 `ICLOUD_MAIL_TEST_DSN` 时，数据库测试会跳过，不代表 PostgreSQL 已经验证过。
