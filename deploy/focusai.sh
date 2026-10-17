#!/usr/bin/env bash
# 商城单机部署管理脚本（Docker Compose）。在服务器上以 root 运行，用法见 usage 或 deploy/README.md。
# 约定：服务器上不编译，只拉取 GitHub Actions 构建的镜像；密钥只存在 $SHOP_DIR/secrets/（600），不打印、不写日志。

set -Eeuo pipefail
umask 077

SHOP_DIR="${SHOP_DIR:-/opt/focusai-shop}"
IMAGE_REPO="${IMAGE_REPO:-ghcr.io/focuspg/dujiao-next}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKUP_DIR="${BACKUP_DIR:-$SHOP_DIR/backups}"
HOURLY_KEEP_HOURS="${HOURLY_KEEP_HOURS:-48}"
DAILY_KEEP_DAYS="${DAILY_KEEP_DAYS:-14}"
CDK_CONTAINERS=(cdk-caddy cdk-recharge)
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"

log() { printf '[%s] %s\n' "$(date '+%F %T')" "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
用法: focusai.sh <命令> [参数]

  install --domain <域名> --image <标签> [--take-over-cdk]
                         首次安装。--take-over-cdk 会停止 CDK 站的两个容器（数据卷保留）并接管 80/443
  upgrade <标签>         升级到指定镜像标签（如 sha-1a2b3c4），升级前自动备份
  rollback               换回上一次的镜像标签（不回滚数据库；有不可逆迁移时需配合 restore）
  status                 查看容器状态、内存、最近备份
  logs [服务]            查看最近日志（默认 shop）
  set-secret <变量名>    隐藏输入一个密钥写入 secrets/shop.env 并重启商城
                         例：DIRECT_CHARGE_ZOVOCARD_API_KEY、DIRECT_CHARGE_ZOVOCARD_WEBHOOK_SECRET
  set-env <变量名> <值>  写入非敏感的环境变量（如 DIRECT_CHARGE_ZOVOCARD_BASE_URL）并重启商城
  zovocard-check         用已配置的 Key 检查 ZovoCard 余额与套餐（只读，不下单）
  backup [hourly|daily]  立即备份数据库（daily 额外打包上传文件与配置）
  restore <备份文件>     用备份覆盖当前数据库（会先再备份一次，需要手动输入确认）
  restore-cdk            停止商城入口，恢复 CDK 站的两个容器
  take-over-cdk          再次停止 CDK 站并由商城接管 80/443

环境变量：SHOP_DIR（默认 /opt/focusai-shop）、IMAGE_REPO（默认 ghcr.io/focuspg/dujiao-next）
EOF
}

require_root() { [[ $EUID -eq 0 ]] || die "请用 root 运行"; }

compose() { docker compose --project-directory "$SHOP_DIR" -f "$SHOP_DIR/compose.yml" "$@"; }

env_get() {
	local file=$1 key=$2
	[[ -f $file ]] || return 0
	grep -E "^${key}=" "$file" | tail -n1 | cut -d= -f2-
}

# env_set 原子地写入或替换 KEY=VALUE，不经过命令行参数以外的任何输出
env_set() {
	local file=$1 key=$2 value=$3 tmp
	[[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] || die "变量名不合法：$key"
	[[ $value != *$'\n'* ]] || die "值不能包含换行"
	tmp="$(mktemp "$file.XXXXXX")"
	if [[ -f $file ]]; then
		grep -vE "^${key}=" "$file" >"$tmp" || true
	fi
	printf '%s=%s\n' "$key" "$value" >>"$tmp"
	chmod 600 "$tmp"
	mv -f "$tmp" "$file"
}

rand_hex() { openssl rand -hex "${1:-32}"; }

rand_password() {
	local raw
	raw="$(openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | cut -c1-20)"
	printf 'Fa%s7' "$raw"
}

wait_healthy() {
	local service=$1 deadline status cid
	deadline=$((SECONDS + HEALTH_TIMEOUT))
	while ((SECONDS < deadline)); do
		cid="$(compose ps -q "$service" 2>/dev/null || true)"
		if [[ -n $cid ]]; then
			status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid" 2>/dev/null || true)"
			case $status in
			healthy | running) return 0 ;;
			unhealthy | exited | dead) break ;;
			esac
		fi
		sleep 3
	done
	log "$service 未就绪，最近日志："
	compose logs --tail 60 "$service" || true
	return 1
}

sync_files() {
	install -d -m 755 "$SHOP_DIR"
	# 从 $SHOP_DIR 里的副本运行时没有新文件可同步
	[[ $SCRIPT_DIR != "$SHOP_DIR" ]] || return 0
	[[ -f $SCRIPT_DIR/compose.yml ]] || die "找不到 $SCRIPT_DIR/compose.yml，请从仓库的 deploy/ 目录运行"
	install -m 644 "$SCRIPT_DIR/compose.yml" "$SHOP_DIR/compose.yml"
	install -m 644 "$SCRIPT_DIR/Caddyfile" "$SHOP_DIR/Caddyfile"
	install -m 755 "$SCRIPT_DIR/focusai.sh" "$SHOP_DIR/focusai.sh"
}

ports_busy_by_others() {
	local busy
	busy="$(docker ps --format '{{.Names}} {{.Ports}}' | grep -E '0\.0\.0\.0:(80|443)->' | awk '{print $1}' | grep -vE '^shop-' || true)"
	printf '%s' "$busy"
}

check_memory() {
	local avail
	avail="$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo)"
	log "当前可用内存约 ${avail}MB"
	if ((avail < 200)); then
		log "警告：可用内存低于 200MB，启动后可能大量使用 swap"
	fi
}

stop_cdk() {
	local name
	for name in "${CDK_CONTAINERS[@]}"; do
		if docker inspect "$name" >/dev/null 2>&1; then
			log "停止 CDK 容器 $name（容器与数据卷保留，开机不再自动启动）"
			docker update --restart=no "$name" >/dev/null
			docker stop "$name" >/dev/null
		fi
	done
}

cmd_install() {
	require_root
	local domain="" image="" take_over=0
	while (($#)); do
		case $1 in
		--domain) domain=${2:-}; shift 2 ;;
		--image) image=${2:-}; shift 2 ;;
		--take-over-cdk) take_over=1; shift ;;
		*) die "未知参数：$1" ;;
		esac
	done
	[[ $domain =~ ^[A-Za-z0-9.-]+$ ]] || die "请用 --domain 指定域名"
	[[ $image =~ ^[A-Za-z0-9._-]+$ ]] || die "请用 --image 指定镜像标签，例如 sha-1a2b3c4"
	command -v docker >/dev/null || die "未安装 docker"
	docker compose version >/dev/null 2>&1 || die "未安装 docker compose 插件"
	command -v openssl >/dev/null || die "未安装 openssl"
	[[ ! -f $SHOP_DIR/.env ]] || die "$SHOP_DIR 已安装过；升级请用 upgrade"

	local busy
	busy="$(ports_busy_by_others)"
	if [[ -n $busy ]]; then
		((take_over)) || die "80/443 正被这些容器占用：$busy。确认停止 CDK 站后加 --take-over-cdk 重试"
	fi
	check_memory

	log "准备目录 $SHOP_DIR"
	sync_files
	install -d -m 700 "$SHOP_DIR/secrets" "$BACKUP_DIR" "$BACKUP_DIR/hourly" "$BACKUP_DIR/daily"
	install -d -m 755 "$SHOP_DIR/data" "$SHOP_DIR/data/uploads" "$SHOP_DIR/data/logs"

	log "生成密钥（只写入 $SHOP_DIR/secrets，不会显示）"
	local pg_pass admin_path site_prefix
	pg_pass="$(rand_hex 24)"
	admin_path="/console-$(rand_hex 4)"
	site_prefix="f$(rand_hex 2)"
	{
		printf 'POSTGRES_USER=shop\nPOSTGRES_DB=shop\nPOSTGRES_PASSWORD=%s\n' "$pg_pass"
	} >"$SHOP_DIR/secrets/postgres.env"
	local shop_env="$SHOP_DIR/secrets/shop.env"
	: >"$shop_env"
	env_set "$shop_env" APP_SECRET_KEY "$(rand_hex 32)"
	env_set "$shop_env" JWT_SECRET "$(rand_hex 32)"
	env_set "$shop_env" USER_JWT_SECRET "$(rand_hex 32)"
	env_set "$shop_env" DATABASE_DSN "host=postgres user=shop password=$pg_pass dbname=shop port=5432 sslmode=disable TimeZone=Asia/Shanghai"
	env_set "$shop_env" DIRECT_CHARGE_VAULT_KEY "$(rand_hex 32)"
	env_set "$shop_env" DIRECT_CHARGE_FINGERPRINT_KEY "$(rand_hex 32)"
	env_set "$shop_env" DIRECT_CHARGE_ZOVOCARD_BASE_URL "https://sandbox.zovocard.com/openapi/v1"
	env_set "$shop_env" DIRECT_CHARGE_ZOVOCARD_API_KEY ""
	env_set "$shop_env" DIRECT_CHARGE_ZOVOCARD_APP_ID ""
	env_set "$shop_env" DIRECT_CHARGE_ZOVOCARD_WEBHOOK_SECRET ""
	env_set "$shop_env" BOOTSTRAP_DEFAULT_ADMIN_USERNAME "admin"
	local admin_pass
	admin_pass="$(rand_password)"
	env_set "$shop_env" BOOTSTRAP_DEFAULT_ADMIN_PASSWORD "$admin_pass"
	printf 'admin\n%s\n' "$admin_pass" >"$SHOP_DIR/secrets/admin-initial-password"
	unset admin_pass pg_pass
	rand_hex 32 >"$SHOP_DIR/secrets/backup.key"
	chmod 600 "$SHOP_DIR"/secrets/*

	sed -e "s|__SHOP_DOMAIN__|$domain|g" -e "s|__ADMIN_PATH__|$admin_path|g" -e "s|__SITE_PREFIX__|$site_prefix|g" \
		"$SCRIPT_DIR/config.yml.tmpl" >"$SHOP_DIR/config.yml"
	chmod 644 "$SHOP_DIR/config.yml"

	{
		printf 'SHOP_DOMAIN=%s\n' "$domain"
		printf 'SHOP_IMAGE=%s:%s\n' "$IMAGE_REPO" "$image"
		printf 'PREVIOUS_IMAGE=\n'
	} >"$SHOP_DIR/.env"
	chmod 600 "$SHOP_DIR/.env"

	log "拉取镜像"
	compose pull --policy missing

	log "启动数据库与 Redis"
	compose up -d postgres redis
	wait_healthy postgres || die "PostgreSQL 启动失败"
	wait_healthy redis || die "Redis 启动失败"

	log "启动商城（首次启动会建表）"
	compose up -d shop
	wait_healthy shop || die "商城启动失败，未切换入口；CDK 站未受影响"

	if [[ -n $busy ]]; then
		stop_cdk
	fi
	log "启动 Caddy 并申请证书"
	compose up -d caddy
	wait_healthy caddy || die "Caddy 启动失败；可运行 restore-cdk 恢复 CDK 站"

	install_timers
	cmd_backup daily

	log "安装完成"
	cat <<EOF

  前台：https://$domain/
  后台：https://$domain$admin_path/
  后台初始账号与密码保存在：$SHOP_DIR/secrets/admin-initial-password（首次登录后请修改密码并开启 2FA）
  备份加密密钥：$SHOP_DIR/secrets/backup.key（请另存一份到服务器以外的安全位置，丢失后备份无法解密）

  下一步：
    $SHOP_DIR/focusai.sh set-secret DIRECT_CHARGE_ZOVOCARD_API_KEY
    $SHOP_DIR/focusai.sh set-secret DIRECT_CHARGE_ZOVOCARD_WEBHOOK_SECRET
    $SHOP_DIR/focusai.sh zovocard-check
EOF
}

smoke_test() {
	local domain
	domain="$(env_get "$SHOP_DIR/.env" SHOP_DOMAIN)"
	if curl -fsS --max-time 15 --resolve "$domain:443:127.0.0.1" "https://$domain/health" >/dev/null; then
		log "冒烟测试通过：https://$domain/health"
	else
		log "警告：经由 Caddy 访问 /health 失败，请检查 caddy 日志"
		return 1
	fi
}

cmd_upgrade() {
	require_root
	local tag=${1:-} current
	[[ $tag =~ ^[A-Za-z0-9._-]+$ ]] || die "请指定镜像标签，例如 sha-1a2b3c4"
	[[ -f $SHOP_DIR/.env ]] || die "尚未安装"
	current="$(env_get "$SHOP_DIR/.env" SHOP_IMAGE)"
	[[ $current != "$IMAGE_REPO:$tag" ]] || die "已经是 $tag"

	sync_files
	log "先拉取新镜像：$IMAGE_REPO:$tag"
	docker image inspect "$IMAGE_REPO:$tag" >/dev/null 2>&1 || docker pull "$IMAGE_REPO:$tag" >/dev/null
	log "升级前备份"
	cmd_backup hourly
	env_set "$SHOP_DIR/.env" PREVIOUS_IMAGE "$current"
	env_set "$SHOP_DIR/.env" SHOP_IMAGE "$IMAGE_REPO:$tag"
	compose up -d shop
	if ! wait_healthy shop; then
		log "新版本未就绪。可运行 rollback 换回 $current；若新版本已改动数据库结构，需配合 restore 恢复升级前备份"
		exit 1
	fi
	compose up -d caddy
	smoke_test || true
	docker image prune -f >/dev/null || true
	log "已升级到 $tag"
}

cmd_rollback() {
	require_root
	local previous current
	previous="$(env_get "$SHOP_DIR/.env" PREVIOUS_IMAGE)"
	current="$(env_get "$SHOP_DIR/.env" SHOP_IMAGE)"
	[[ -n $previous ]] || die "没有可回滚的上一个版本"
	log "回滚：$current → $previous（数据库不回滚）"
	env_set "$SHOP_DIR/.env" SHOP_IMAGE "$previous"
	env_set "$SHOP_DIR/.env" PREVIOUS_IMAGE "$current"
	compose up -d shop
	wait_healthy shop || die "回滚后仍未就绪，请查看日志或用 restore 恢复升级前备份"
	log "已回滚"
}

cmd_status() {
	compose ps
	echo
	local ids=()
	mapfile -t ids < <(compose ps -q)
	if ((${#ids[@]})); then
		docker stats --no-stream --format 'table {{.Name}}\t{{.MemUsage}}\t{{.CPUPerc}}' "${ids[@]}" || true
	fi
	echo
	free -m
	echo
	log "镜像：$(env_get "$SHOP_DIR/.env" SHOP_IMAGE)"
	log "最近备份："
	find "$BACKUP_DIR" -type f -name '*.enc' -printf '%TY-%Tm-%Td %TH:%TM  %s\t%p\n' 2>/dev/null | sort | tail -n 3
}

cmd_logs() {
	compose logs --tail 200 "${1:-shop}"
}

cmd_set_secret() {
	require_root
	local key=${1:-} value
	[[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] || die "请指定变量名，例如 DIRECT_CHARGE_ZOVOCARD_API_KEY"
	[[ -t 0 ]] || die "请在交互终端中运行，以便隐藏输入"
	read -rsp "请输入 $key 的值（不会显示）：" value
	echo
	[[ -n $value ]] || die "值为空，未修改"
	env_set "$SHOP_DIR/secrets/shop.env" "$key" "$value"
	unset value
	log "已写入 $key，重启商城"
	compose up -d --force-recreate shop
	wait_healthy shop || die "商城重启后未就绪"
}

cmd_set_env() {
	require_root
	local key=${1:-} value=${2:-}
	[[ $key =~ ^[A-Z][A-Z0-9_]*$ ]] || die "请指定变量名"
	case $key in
	*KEY* | *SECRET* | *PASSWORD* | *DSN*) die "$key 看起来是密钥，请用 set-secret" ;;
	esac
	env_set "$SHOP_DIR/secrets/shop.env" "$key" "$value"
	log "已写入 $key=$value，重启商城"
	compose up -d --force-recreate shop
	wait_healthy shop || die "商城重启后未就绪"
}

cmd_zovocard_check() {
	compose exec -T shop ./dujiao-next admin zovocard-check --product gpt
}

encrypt_to() {
	openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass "file:$SHOP_DIR/secrets/backup.key" -out "$1"
}

decrypt_from() {
	openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass "file:$SHOP_DIR/secrets/backup.key" -in "$1"
}

cmd_backup() {
	require_root
	local kind=${1:-hourly} stamp dest tmp
	[[ $kind == hourly || $kind == daily ]] || die "备份类型只能是 hourly 或 daily"
	stamp="$(date '+%Y%m%d-%H%M%S')"
	install -d -m 700 "$BACKUP_DIR/$kind"
	dest="$BACKUP_DIR/$kind/db-$stamp.dump.enc"
	# 同一秒内重复备份时不覆盖已有文件（恢复前会先备份一次）
	while [[ -e $dest ]]; do
		sleep 1
		stamp="$(date '+%Y%m%d-%H%M%S')"
		dest="$BACKUP_DIR/$kind/db-$stamp.dump.enc"
	done
	tmp="$dest.part"
	# pg_dump 自定义格式自带压缩；直接加密落盘，明文不落地
	if ! compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' | encrypt_to "$tmp"; then
		rm -f "$tmp"
		die "数据库备份失败"
	fi
	mv -f "$tmp" "$dest"
	log "数据库备份：$dest"
	if [[ $kind == daily ]]; then
		dest="$BACKUP_DIR/daily/files-$stamp.tar.gz.enc"
		tar -C "$SHOP_DIR" -czf - data/uploads config.yml secrets/shop.env secrets/postgres.env .env | encrypt_to "$dest.part"
		mv -f "$dest.part" "$dest"
		log "文件备份：$dest"
	fi
	find "$BACKUP_DIR/hourly" -type f -name '*.enc' -mmin "+$((HOURLY_KEEP_HOURS * 60))" -delete
	find "$BACKUP_DIR/daily" -type f -name '*.enc' -mtime "+$DAILY_KEEP_DAYS" -delete
	find "$BACKUP_DIR" -type f -name '*.part' -mmin +120 -delete
}

cmd_restore() {
	require_root
	local file=${1:-} answer magic
	[[ -f $file ]] || die "找不到备份文件：$file"
	[[ $file == *db-*.dump.enc ]] || die "请指定数据库备份（db-*.dump.enc）"
	magic="$(decrypt_from "$file" 2>/dev/null | head -c 5 || true)"
	[[ $magic == PGDMP ]] || die "无法解密或不是数据库备份"
	echo "将用 $file 覆盖当前数据库。恢复后不要立即重放外部操作，先在后台核对支付与代充任务。"
	read -rp "确认请输入 RESTORE：" answer
	[[ $answer == RESTORE ]] || die "已取消"
	log "恢复前先备份当前数据库"
	cmd_backup hourly
	compose stop shop
	decrypt_from "$file" | compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction'
	# 队列里的任务可能与恢复后的数据库不一致，清空后由程序按数据库重新调度
	compose exec -T redis redis-cli -n 1 FLUSHDB >/dev/null
	compose up -d shop
	wait_healthy shop || die "恢复后商城未就绪"
	log "恢复完成"
}

cmd_take_over_cdk() {
	require_root
	[[ -f $SHOP_DIR/.env ]] || die "尚未安装"
	stop_cdk
	compose up -d caddy
	wait_healthy caddy || die "Caddy 启动失败；可运行 restore-cdk 恢复 CDK 站"
	smoke_test || true
}

cmd_restore_cdk() {
	require_root
	local name
	log "停止商城入口（Caddy），释放 80/443"
	compose stop caddy || true
	for name in "${CDK_CONTAINERS[@]}"; do
		if docker inspect "$name" >/dev/null 2>&1; then
			docker update --restart=unless-stopped "$name" >/dev/null
			docker start "$name" >/dev/null
			log "已启动 $name"
		fi
	done
	log "入口已交还 CDK 站，商城的程序与数据库仍在运行。重新切回商城：再次运行 take-over-cdk"
}

install_timers() {
	if [[ ! -d /run/systemd/system ]]; then
		log "警告：未检测到 systemd，未安装定时备份"
		return 0
	fi
	cat >/etc/systemd/system/focusai-backup@.service <<EOF
[Unit]
Description=focusai shop %i backup
After=docker.service

[Service]
Type=oneshot
ExecStart=$SHOP_DIR/focusai.sh backup %i
EOF
	cat >/etc/systemd/system/focusai-backup-hourly.timer <<'EOF'
[Unit]
Description=focusai shop hourly database backup

[Timer]
OnCalendar=*-*-* *:17:00
Persistent=true
Unit=focusai-backup@hourly.service

[Install]
WantedBy=timers.target
EOF
	cat >/etc/systemd/system/focusai-backup-daily.timer <<'EOF'
[Unit]
Description=focusai shop daily full backup

[Timer]
OnCalendar=*-*-* 04:37:00
Persistent=true
Unit=focusai-backup@daily.service

[Install]
WantedBy=timers.target
EOF
	systemctl daemon-reload
	systemctl enable --now focusai-backup-hourly.timer focusai-backup-daily.timer >/dev/null
	log "已启用每小时与每日备份定时任务"
}

main() {
	local cmd=${1:-}
	shift || true
	case $cmd in
	install) cmd_install "$@" ;;
	upgrade) cmd_upgrade "$@" ;;
	rollback) cmd_rollback ;;
	status) cmd_status ;;
	logs) cmd_logs "$@" ;;
	set-secret) cmd_set_secret "$@" ;;
	set-env) cmd_set_env "$@" ;;
	zovocard-check) cmd_zovocard_check ;;
	backup) cmd_backup "$@" ;;
	restore) cmd_restore "$@" ;;
	restore-cdk) cmd_restore_cdk ;;
	take-over-cdk) cmd_take_over_cdk ;;
	-h | --help | help | "") usage ;;
	*) usage; exit 1 ;;
	esac
}

main "$@"
