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
ALLOWED_IPS=127.0.0.1,192.168.1.1 # Опционально: разрешенные IP-адреса через запятую
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

В проекте настроен пайплайн автоматического тестирования и деплоя (`.github/workflows/deploy.yml`):
- При создании Pull Request или Push в `main` запускаются тесты (`go test ./...`) и валидация сборки.
- При успешном прохождении тестов на ветке `main` (или ручном запуске через `workflow_dispatch`) происходит автоматический деплой на сервер по SSH с пересборкой контейнеров Docker Compose.

### Необходимые GitHub Secrets

В репозитории GitHub перейдите в **Settings** -> **Secrets and variables** -> **Actions** и добавьте следующие секреты:

| Секрет | Описание | Обязательный |
|---|---|---|
| `SERVER_HOST` | IP-адрес или домен вашего сервера (например, `194.87.12.34`) | Да |
| `SERVER_USER` | Пользователь для подключения по SSH (например, `root` или `ubuntu`) | Да |
| `SERVER_SSH_KEY` | Приватный SSH-ключ для авторизации (содержимое `~/.ssh/id_ed25519` или `~/.ssh/id_rsa`) | Да (если используется ключ) |
| `SERVER_PASSWORD` | Пароль пользователя SSH (если авторизация по паролю вместо ключа) | Опционально |
| `SERVER_PATH` | Абсолютный путь к папке проекта на сервере (например, `/opt/larasentry-server`) | Да |
| `SERVER_PORT` | Порт SSH (по умолчанию `22`) | Опционально |
| `SERVER_PASSPHRASE` | Пароль от приватного SSH-ключа (если ключ зашифрован) | Опционально |

### Первичная подготовка сервера

1. Склонируйте репозиторий в нужную директорию на сервере:
   ```bash
   git clone git@github.com:Manzadey/larasentry-server.git /opt/larasentry-server
   cd /opt/larasentry-server
   ```
2. Создайте файл `.env` с боевыми настройками:
   ```bash
   cp .env.example .env
   nano .env
   ```
3. Убедитесь, что публичный SSH-ключ раннера GitHub добавлен в `~/.ssh/authorized_keys` на сервере.
4. Убедитесь, что Docker и Docker Compose установлены и запущены.

## License
MIT License

