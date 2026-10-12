#!/usr/bin/env bash
# Выкатывает новый бинарник на сервере. Запускается из CI: bash ~/deploy.sh ~/larasentry-server.new
# Ждёт systemd-юнит larasentry и sudoers-правило на systemctl stop/start larasentry (см. README).
set -euo pipefail

NEW_BINARY=${1:?usage: deploy.sh <new binary>}
SERVICE=larasentry
BINARY=larasentry-server
KEEP_BACKUPS=5

cd "$HOME"

env_value() {
	sed -n "s/^$1=//p" .env 2>/dev/null | tail -n 1 | tr -d "\"'"
}

listen=$(env_value LISTEN_ADDR)
port=${listen##*:}
port=${port:-$(env_value PORT)}
health_url="http://127.0.0.1:${port:-8080}/api/health"

healthy() {
	for _ in $(seq 15); do
		curl -fs -o /dev/null "$health_url" && return 0
		sleep 1
	done
	return 1
}

backup="backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup"

sudo systemctl stop "$SERVICE"
# Сервис остановлен, поэтому копия базы вместе с -wal/-shm согласована
for file in "$BINARY" tracker.db tracker.db-wal tracker.db-shm; do
	if [ -e "$file" ]; then
		cp -a "$file" "$backup"/
	fi
done
chmod +x "$NEW_BINARY"
mv -f "$NEW_BINARY" "$BINARY"
sudo systemctl start "$SERVICE"

if healthy; then
	echo "Deployed, $health_url is healthy. Backup: $backup"
	ls -1d backups/*/ | sort -r | tail -n +$((KEEP_BACKUPS + 1)) | xargs rm -rf
	exit 0
fi

echo "Health check failed, rolling back to $backup" >&2
sudo systemctl stop "$SERVICE"
# Свежий -wal от новой версии нельзя применять к старой базе — повредит её
rm -f tracker.db-wal tracker.db-shm
cp -a "$backup"/. .
sudo systemctl start "$SERVICE"
healthy && echo "Rolled back, service is healthy" >&2 || echo "Rollback did not bring the service up" >&2
exit 1
