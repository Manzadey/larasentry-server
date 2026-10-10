package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type AlertManager struct {
	processor *Processor
	botToken  string
	chatID    string
	interval  time.Duration
}

func NewAlertManager(processor *Processor, botToken, chatID string, interval time.Duration) *AlertManager {
	return &AlertManager{
		processor: processor,
		botToken:  botToken,
		chatID:    chatID,
		interval:  interval,
	}
}

func (a *AlertManager) Start() {
	if a.botToken == "" || a.chatID == "" {
		log.Println("Telegram bot token or chat ID is empty. Alerting disabled.")
		return
	}

	ticker := time.NewTicker(a.interval)
	log.Printf("Starting Alert Manager (Interval: %v)...\n", a.interval)

	for range ticker.C {
		a.processAlerts()
	}
}

func (a *AlertManager) processAlerts() {
	buffer := a.processor.GetAndClearBuffer()
	if len(buffer) == 0 {
		return
	}

	// Build markdown message
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🚨 **Отчет об ошибках (за последние %v)**\n\n", a.interval))

	for _, incident := range buffer {
		sb.WriteString(fmt.Sprintf("🔴 **%s** (x%d)\n", incident.ExceptionClass, incident.EventsCount))
		sb.WriteString(fmt.Sprintf("`%s:%d`\n", incident.File, incident.Line))
		sb.WriteString(fmt.Sprintf("_%s_\n\n", incident.Message))
	}

	a.sendTelegram(sb.String())
}

func (a *AlertManager) sendTelegram(text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", a.botToken)

	payload := map[string]interface{}{
		"chat_id":    a.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("Error sending telegram alert: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Telegram API returned status: %d\n", resp.StatusCode)
	} else {
		log.Println("Successfully sent Telegram alert.")
	}
}
