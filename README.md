# icloud-mail

单管理员的 iCloud 隐私邮箱接码服务。外部程序只走接码接口，不登录管理页。

协议来自本机 `C:\Users\Administrator\codex-1\iCloud-Privacy-Mail`。租约和后台任务只借鉴了 [xiuxiu56/iCloud-Privacy-Mail-v2](https://github.com/xiuxiu56/iCloud-Privacy-Mail-v2) 的思路，没有复制它的源码。公开对照仓库是 [q1953258942/iCloud-Privacy-Mail](https://github.com/q1953258942/iCloud-Privacy-Mail)。

总方案名是“icloud-mail 单管理员接码服务总方案”。完整方案只有一份，文件是 `docs/总方案.md`。里面写了每条功能链路借鉴了哪个项目的哪一段。

## 运行约束

- 只能跑一个进程。账号锁在进程内存里，不能多副本同时登录、创建或删除。
- 数据库只用 PostgreSQL。
- 账号上的代理是唯一出口。空代理表示直连，不会用旧会话里的代理补上。
- 管理登录请求不接受代理字段。多传代理字段会被拒绝，不是忽略。
- 不要设置 `IPM_DEBUG_APPLE_ACCOUNT`。打开后协议层会把供应商响应打到标准错误。
- 创建结果没落库且待核对标记也写失败时，停止记录写在 `ICLOUD_MAIL_HALT_FILE`。容器部署要把这个文件放在持久卷上。
- 仓库地址是 `https://github.com/biubiubiu125/icloud-mail`。镜像地址是 `ghcr.io/biubiubiu125/icloud-mail`。每次 push 和手动触发都会自动构建并推送 GHCR。最终镜像基于 Alpine，并安装 `ca-certificates`。

## 启动

复制 `config.example.json` 为 `config.json`，或直接用环境变量。环境变量优先。

```powershell
$env:ICLOUD_MAIL_DATABASE_URL = 'postgres://icloud_mail:change-me@127.0.0.1:5432/icloud_mail?sslmode=disable'
$env:ICLOUD_MAIL_ADMIN_USERNAME = 'admin'
$env:ICLOUD_MAIL_ADMIN_PASSWORD = 'change-me-now'
$env:ICLOUD_MAIL_HTTP_ADDR = ':8080'
go run ./cmd/icloud-mail
```

首次启动如果库里没有管理员，且设置了上面的用户名和密码，会只创建一次。用户名至少 3 位，密码至少 8 位。没有预置管理员时，管理页只能从本机创建唯一管理员；创建后不能再走这个入口。服务不在本机打开时，用环境变量创建管理员。

管理页写操作需要请求头 `X-Requested-With: icloud-mail`。接码接口不认管理员 Cookie。

## 接码接口

全局密钥放在 `X-API-Key`、`Authorization: Bearer` 或 `api_key` 查询参数。优先用请求头。

- `POST /api/v1/mailboxes/claim`：领取一个未使用邮箱。池子空时返回 HTTP 200 和 `no_available_mailbox`。
- `POST /api/v1/mailboxes/lookup`：按邮箱查询，不返回邮箱密钥。
- `GET /api/v1/mailboxes/{email}/code?key=邮箱密钥`：取验证码。
- `POST /api/v1/mailboxes/{email}/commit|release|renew`
- `POST /api/v1/mailbox-leases/{id}/commit|release|renew`：只认全局密钥。

## 验证

```powershell
wsl.exe bash -lc 'cd /mnt/c/Users/Administrator/codex-1/icloud-mail && go test -count=1 ./...'
```

数据库测试需要 `ICLOUD_MAIL_TEST_DSN`。没有这个变量时会跳过，不代表 PostgreSQL 已验证。
