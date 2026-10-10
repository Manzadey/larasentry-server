FROM golang:alpine AS builder

WORKDIR /app

# Скачиваем зависимости
COPY go.mod go.sum ./
RUN go mod download

# Копируем код и собираем
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o larasentry-server .

# Финальный легковесный образ
FROM alpine:latest
WORKDIR /app

# Устанавливаем сертификаты для работы HTTPS (Telegram API)
RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/larasentry-server .

# Создаем папку для базы данных
RUN mkdir -p /app/data

# Дефолтные переменные окружения
ENV PORT=8080
ENV DB_PATH=/app/data/tracker.db

EXPOSE 8080
CMD ["./larasentry-server"]
