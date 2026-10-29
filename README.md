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

浏览器打开 `http://127.0.0.1:8080`，用 `.env` 里的用户名和密码登录。页面标题是 iCloud Mail。登录后有「概览」「账号」「邮箱池」「设置」。

### 添加 Apple 账号

在「账号」填写名称、Apple ID、密码。代理可空。要走代理时写成完整地址，例如：

```text
socks5://用户:密码@主机:端口
http://主机:端口
```

没有 `://` 的地址会被拒绝。代理字段全部留空表示直连。改 Apple ID 会清掉这个账号已经保存的登录态。已存密码留空再保存，会把密码清掉。账号列表能看到 Web、Apple、IMAP 是否已保存，以及代理是已设置还是直连。密码和代理明文只在点开的详情里。

### 登录并保存取码

点开账号，按实际要用的通道登录。验证码方式默认是受信任设备，也可以选短信。登录请求只提交登录密码和验证码方式，不会带代理。

- 用 iCloud 网页创建邮箱：点「Web 登录」。要验证码时填上，再点「提交 Web 验证码」。需要验证码时不要刷新页面，否则这次登录挑战会中断。
- 用 Apple Account 创建邮箱：点「Apple 登录」，需要时再点「提交 Apple 验证码」。设置里的 Apple API Key 留空不会清掉已有值；只有登录态里没有密钥时才需要另填。

验证码只能提交给刚才点开的那条登录。点错通道时页面不会发出请求，服务端也会拒绝。请求还没返回时，对应按钮不可再点。如果先点了一条登录、又改点另一条，新的提交按钮会打开，旧的提交按钮会禁用。旧的验证码返回时，不会清掉后开始的那条验证码。

取验证码默认走 IMAP。在同一张卡片里填写 iCloud 邮箱和 App 专用密码，点「保存 IMAP」。详情里的 IMAP 邮箱会显示已经保存的地址；没有可用的 IMAP 登录时，输入框才回退到 Apple ID。保存新的应用专用密码时，写入的是输入框里当前的地址。没保存 IMAP、又没在设置里打开「Web 取码兜底」时，接码会返回「未保存 IMAP 取码登录」。

### 准备可领取的邮箱

在「账号」的创建区选择已有账号，可以填标签和备注。通道保持「自动」时，已有 Apple Account 登录态就用它，否则用 iCloud Web。点「创建一个邮箱」，一次只创建一个。创建还没结束时，刷新或切换页面也不会让这个账号的另一个创建按钮重新打开；服务端会直接拒绝重叠的创建，不会排队再创建一个。也可以在「设置」里打开「自动创建」。自动创建默认关闭，间隔至少 30 秒，每轮最多 20 个。账号卡片里也可以按所选通道创建一个，不填标签和备注。

账号显示「待核对」时，先到 iCloud 看这个邮箱是否已经创建出来，再点「我已在 iCloud 核对，清除待核对」。不要删 Docker 卷来跳过这一步。

公开接口默认开启。接码密钥已经由环境变量写入；设置页把「新的接口密钥」留空保存，不会清空现有密钥。要换密钥时，在这里填新值。设置页不会显示已经保存的接口密钥和 Apple API Key。设置要等当前配置读取成功后才能保存。读取失败时保存按钮保持不可用，避免把空表单写进数据库。

「对外地址」可以填外部程序访问本服务的根地址，例如 `http://127.0.0.1:8080`。复制接口、导出文件和领取结果都用同一个地址：先用这里填的值，留空才用进程环境变量 `ICLOUD_MAIL_PUBLIC_BASE_URL`，两边都空时才是相对路径。复制不会把环境变量里的地址写回设置。

后台取信默认开启，大约 60 秒一轮。默认关键词是 `OpenAI`。租约默认 30 分钟。邮件默认保留 90 天。

「邮箱池」可以搜索，也可以按账号、状态、导出、接口开关和删除状态筛选。状态只有可领取和已占用；关闭接口后用「接口关」筛选，没有单独的已停用状态。复制邮箱只复制地址。复制接口不会标记已导出，也不会把密钥显示在表格里；复制出来的地址和导出、领取用的是同一个根地址。导出选中邮箱会下载带密钥的 `mailbox-apis.json`，并标记已导出。导出失败会在页面上提示，不会假装已经下载。这个文件不要提交，也不要发给不需要接码的人。一次最多导出 500 个。

删除邮箱默认会删远端。远端没确认成功时，本地会留在删除中。服务重启后，中断的删除会变成未知，再次删除必须在页面上明确确认。只清本地不会访问 Apple，也必须确认。远端已经删除成功时，页面上的「清理本地记录」只删本地，确认框也会说明不会再访问 Apple。创建、同步、核对和删除在请求返回前不能再点。已经写入但列表刷新失败时，页面会说明已经完成，这个账号的两个创建入口都保持不可点，直到下一次刷新成功。不要因为刷新失败再创建一次。同步如果已经导入一部分后又失败，页面会写出导入数量并刷新列表，不会把已导入的邮箱说成完全没写入。保存账号资料和代理是一次写入；代理写不进去时，Apple ID、密码和登录态也不会先被改掉。

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
