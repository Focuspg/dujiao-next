# 单机部署（Docker Compose）

适用于 1 核 / 1G 内存的单台服务器：Caddy（HTTPS 与证书）+ 商城（API、后台任务、前台与后台页面同一进程）+ PostgreSQL + Redis，全部跑在 Docker 里，各自设了内存上限。服务器上不编译，只拉取 GitHub Actions 构建好的镜像。

| 文件 | 作用 |
| --- | --- |
| `compose.yml` | 四个容器、内存上限、网络（数据库与 Redis 在不出网的内部网络） |
| `Caddyfile` | 站点反向代理，自动申请证书 |
| `config.yml.tmpl` | 非敏感配置模板，安装时生成 `config.yml` |
| `focusai.sh` | 安装、升级、回滚、备份、恢复、填写密钥 |

## 镜像

推送到 `main` 后，`.github/workflows/image.yml` 构建 `ghcr.io/focuspg/dujiao-next:sha-<7位提交号>`。部署时始终使用 `sha-` 标签，不用 `main`，保证每次部署的版本可追溯、可回滚。

镜像包第一次发布后默认是私有的：在 GitHub 仓库 → Packages → dujiao-next → Package settings 里改为 Public，服务器就能免登录拉取；或在服务器上 `docker login ghcr.io`（使用只读 `read:packages` 令牌）。

## 首次安装

```bash
git clone https://github.com/Focuspg/dujiao-next /opt/focusai-shop/repo
cd /opt/focusai-shop/repo && git checkout <提交号>
./deploy/focusai.sh install --domain focusai.trade --image sha-<7位提交号> [--take-over-cdk]
```

- 安装会生成所有密钥并写入 `/opt/focusai-shop/secrets/`（权限 600），不会显示在终端或日志里。
- 后台地址是随机路径（`/console-xxxxxxxx`），安装结束时显示；初始管理员账号与密码在 `secrets/admin-initial-password`，首次登录后修改密码并开启 2FA。
- 80/443 被其它容器占用时安装会停下；确认要停止 CDK 站后加 `--take-over-cdk`。CDK 的两个容器只会被停止并取消开机自启，容器与数据卷都保留，`restore-cdk` 可随时恢复。
- 商城健康检查通过后才会停止 CDK 站并切换入口；启动失败不会影响 CDK 站。

## 填写上游密钥（不要发到聊天里）

```bash
/opt/focusai-shop/focusai.sh set-secret DIRECT_CHARGE_ZOVOCARD_API_KEY
/opt/focusai-shop/focusai.sh set-secret DIRECT_CHARGE_ZOVOCARD_APP_ID
/opt/focusai-shop/focusai.sh set-secret DIRECT_CHARGE_ZOVOCARD_WEBHOOK_SECRET
/opt/focusai-shop/focusai.sh zovocard-check
```

默认接 ZovoCard 沙盒；联调通过后切换生产：

```bash
/opt/focusai-shop/focusai.sh set-env DIRECT_CHARGE_ZOVOCARD_BASE_URL https://zovocard.com/openapi/v1
```

ZovoCard 回调地址填 `https://<域名>/api/v1/webhooks/zovocard`。SMTP 邮件、易支付商户在后台“系统设置 / 支付渠道”里配置。

## 升级与回滚

```bash
cd /opt/focusai-shop/repo && git pull
./deploy/focusai.sh upgrade sha-<新提交号>   # 先拉镜像、再备份、再切换，最后经 Caddy 冒烟测试
/opt/focusai-shop/focusai.sh rollback          # 换回上一个镜像
```

回滚只换程序，不回滚数据库。新版本若改了数据库结构且旧版本无法兼容，用升级前的备份 `restore`。

## 备份与恢复

- 每小时第 17 分备份数据库，保留 48 小时；每天 04:37 额外备份上传文件、配置与密钥，保留 14 天。目录 `/opt/focusai-shop/backups/`。
- 备份用 `secrets/backup.key` 加密（AES-256），明文不落盘。**把这把密钥另存一份到服务器以外**，否则服务器丢失时备份也无法解密。
- 目前备份只在本机，异地副本尚未配置。
- 恢复：`/opt/focusai-shop/focusai.sh restore /opt/focusai-shop/backups/hourly/db-<时间>.dump.enc`，会先备份当前库、要求输入 `RESTORE` 确认、清空任务队列后重启。恢复后先在后台核对支付、代充任务和退款，再处理“待核验”任务，不要直接重放。

## 日常查看

```bash
/opt/focusai-shop/focusai.sh status      # 容器、内存、最近备份
/opt/focusai-shop/focusai.sh logs shop   # 商城日志（caddy / postgres / redis 同理）
```

内存上限：Caddy 96M、商城 320M、PostgreSQL 192M、Redis 64M。如果 `free -m` 显示 swap 持续增长，就该升级服务器了。
