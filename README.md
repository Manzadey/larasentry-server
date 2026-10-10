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

Used by the Laravel client to push errors. Requires `Authorization: Bearer <API_KEY>`.

### 2. List Errors (GET)
`GET /api/incidents`

Returns a JSON array of the latest errors for a specific project. Requires `Authorization: Bearer <API_KEY>`.

## License
MIT License
