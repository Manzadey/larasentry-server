# Larasentry Server

A blazing fast, lightweight Go-based microservice designed to receive exceptions from Laravel applications (via `larasentry-client`), aggregate them, and send consolidated alerts to Telegram.

## Features
- **High Performance:** Written in Go using channels and goroutines. HTTP requests from clients are non-blocking.
- **Lightweight Storage:** Uses SQLite with WAL mode. Exception occurrences are deduplicated (grouped by hash).
- **Auto-Cleanup:** Old incidents (older than 30 days) are automatically deleted by a background worker.
- **Telegram Alerts:** Instead of spamming on every error, it sends aggregated summary reports every 5 minutes (configurable).

## Running via Docker Compose

This is the recommended way to run the server.

1. Clone the repository
2. Copy the `.env.example` to `.env` and fill in your Telegram bot details:
```env
PORT=8080
DB_PATH=/app/data/tracker.db
TG_BOT_TOKEN=your-telegram-bot-token
TG_CHAT_ID=your-chat-id
ALERT_INTERVAL=5m
ALLOWED_IPS=203.0.113.10 # IP, которым открыт /api/*, через запятую; пусто — только с хоста, где запущен сервис
```
3. Start the server:
```bash
docker-compose up -d --build
```

The SQLite database will be persisted in the `./data` directory on your host machine.

## API Endpoints

### 1. Receive Error (POST)
`POST /api/events`

Used by Laravel clients to push errors. API-ключи не требуются — проект регистрируется и определяется автоматически:
- В теле запроса: `"project": "my-app"` (или `"project_name"`)
- Либо через заголовок: `X-Project: my-app`
- Либо через query-параметр: `?project=my-app`
- Если имя не передано, событие сохраняется в `Default Project`.
*(Для обратной совместимости также поддерживается `Authorization: Bearer <API_KEY>`)*.

### 2. List Errors (GET)
`GET /api/incidents`

Возвращает список инцидентов. Поддерживает фильтрацию по проекту: `?project=my-app` или `?project_id=1`. Без параметров возвращает инциденты всех проектов.

## CI/CD и автодеплой (GitHub Actions)

Пайплайн `.github/workflows/deploy.yml`:
- **test** — на каждый PR и push в `main`: `go mod verify`, `go test ./...`, проверка сборки.
- **build** — на push в `main` (и `workflow_dispatch`): статический бинарник `linux/amd64`, артефакт хранится 14 дней.
- **deploy** — окружение `production`: копирует бинарник и `deploy/deploy.sh` на сервер по SSH и запускает скрипт.

`deploy/deploy.sh` на сервере останавливает сервис, кладёт бинарник и `tracker.db*` в `~/backups/<дата>/` (хранит 5 последних), подменяет бинарник, запускает сервис и 15 секунд ждёт `/api/health`. Не поднялся — возвращает бинарник и базу из бэкапа, и job падает.

### Сервер

Сервис работает как systemd-юнит от пользователя `larasentry`, всё лежит в его домашней папке:

```ini
# /etc/systemd/system/larasentry.service
[Unit]
Description=Larasentry Server
After=network.target

[Service]
Type=simple
User=larasentry
WorkingDirectory=/home/larasentry
ExecStart=/home/larasentry/larasentry-server
Restart=always
RestartSec=3
EnvironmentFile=/home/larasentry/.env

[Install]
WantedBy=multi-user.target
```

Разовая настройка (под пользователем с sudo):

1. Разрешить `larasentry` останавливать и запускать только свой сервис:
   ```bash
   echo 'larasentry ALL=(root) NOPASSWD: /usr/bin/systemctl stop larasentry, /usr/bin/systemctl start larasentry, /usr/bin/systemctl restart larasentry' | sudo tee /etc/sudoers.d/larasentry
   sudo chmod 440 /etc/sudoers.d/larasentry && sudo visudo -cf /etc/sudoers.d/larasentry
   ```
2. В `~larasentry/.env` — `LISTEN_ADDR=127.0.0.1:8080` (порт не виден соседям по сети, только nginx) и `ALLOWED_IPS`.
3. Заполнить переменные в `scripts/gh-secrets.sh` и запустить `bash scripts/gh-secrets.sh`: он создаст ключ для CI, окружение `production` с секретами и напечатает строку для `~larasentry/.ssh/authorized_keys`.

### nginx перед сервисом

`/api/*` закрыт списком `ALLOWED_IPS`, а UI и Swagger — basic auth на nginx. Basic auth нельзя вешать на `/api/`: Laravel-клиенты перестанут отправлять события.

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:8080;
    # Обязательно: сервис доверяет заголовку от nginx на своём хосте. Без него все запросы
    # через nginx выглядят как запросы с хоста и проходят ALLOWED_IPS
    proxy_set_header X-Real-IP $remote_addr;
    # Иначе nginx копит поток событий (SSE) в буфере
    proxy_buffering off;
}

location / {
    auth_basic "LaraSentry";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_buffering off;
}
```

Файл паролей: `htpasswd -c /etc/nginx/.htpasswd admin` (пакет `apache2-utils`).

Если перед nginx стоит ещё один прокси (TLS снимается на другой машине), nginx видит адрес прокси, а не клиента, и `ALLOWED_IPS` теряет смысл. Тогда в `server` нужно взять адрес клиента из заголовка внешнего прокси:

```nginx
set_real_ip_from 10.10.10.2;          # адрес внешнего прокси
real_ip_header X-Forwarded-For;       # или X-Real-IP — какой он передаёт
real_ip_recursive on;
```

## License
MIT License

