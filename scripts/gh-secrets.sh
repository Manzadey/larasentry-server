#!/usr/bin/env bash
# Задаёт секреты окружения production для деплоя из .github/workflows/deploy.yml.
# Заполни переменные ниже и запусти: bash scripts/gh-secrets.sh
set -euo pipefail

REPO="Manzadey/larasentry-server"
SERVER_HOST=""            # внешний адрес, например 185.137.233.175
SERVER_PORT=""            # порт SSH, например 22103
SERVER_USER="larasentry"
# Отдельный ключ только для CI: его можно отозвать, не трогая личный доступ
DEPLOY_KEY="$HOME/.ssh/larasentry_deploy"

for var in REPO SERVER_HOST SERVER_PORT SERVER_USER DEPLOY_KEY; do
	if [ -z "${!var}" ]; then
		echo "Заполни $var в $0" >&2
		exit 1
	fi
done

gh auth status >/dev/null

if [ ! -f "$DEPLOY_KEY" ]; then
	ssh-keygen -q -t ed25519 -N "" -C "github-actions@$REPO" -f "$DEPLOY_KEY"
	echo "Создан ключ $DEPLOY_KEY"
fi

known_hosts=$(ssh-keyscan -p "$SERVER_PORT" "$SERVER_HOST" 2>/dev/null)
if [ -z "$known_hosts" ]; then
	echo "ssh-keyscan не получил ключ хоста $SERVER_HOST:$SERVER_PORT" >&2
	exit 1
fi

gh api -X PUT "repos/$REPO/environments/production" >/dev/null

set_secret() {
	gh secret set "$1" --repo "$REPO" --env production
}
printf '%s' "$SERVER_HOST" | set_secret SERVER_HOST
printf '%s' "$SERVER_PORT" | set_secret SERVER_PORT
printf '%s' "$SERVER_USER" | set_secret SERVER_USER
set_secret SERVER_SSH_KEY <"$DEPLOY_KEY"
printf '%s\n' "$known_hosts" | set_secret SERVER_KNOWN_HOSTS

cat <<EOF

Секреты окружения production заданы. Добавь строку в ~$SERVER_USER/.ssh/authorized_keys на сервере
(restrict запрещает ключу pty и пробросы портов, scp и команды работают):

restrict $(cat "$DEPLOY_KEY.pub")
EOF
