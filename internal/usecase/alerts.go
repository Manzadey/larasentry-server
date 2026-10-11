package usecase

import (
	"context"
	"fmt"
	"log"
	"server/internal/domain"
	"strings"
	"time"
)

type AlertManager struct {
	processor *Processor
	sender    domain.AlertSender
	interval  time.Duration
}

func NewAlertManager(processor *Processor, sender domain.AlertSender, interval time.Duration) *AlertManager {
	return &AlertManager{
		processor: processor,
		sender:    sender,
		interval:  interval,
	}
}

func (a *AlertManager) Start() {
	if a.sender == nil {
		log.Println("Alert sender is nil. Alerting disabled.")
		return
	}

	ticker := time.NewTicker(a.interval)
	log.Printf("Starting Alert Manager (Interval: %v)...\n", a.interval)

	for range ticker.C {
		a.processAlerts(context.Background())
	}
}

func (a *AlertManager) processAlerts(ctx context.Context) {
	buffer := a.processor.GetAndClearBuffer()
	if len(buffer) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🚨 **Отчет об ошибках (за последние %v)**\n\n", a.interval))

	for _, incident := range buffer {
		if incident.IsRegression {
			sb.WriteString(fmt.Sprintf("⚠️ **РЕГРЕССИЯ** (вернулась в `%s`, решена в `%s`)\n", incident.LastRelease, incident.ResolvedInRelease))
		}
		sb.WriteString(fmt.Sprintf("🔴 **%s** (x%d)\n", incident.ExceptionClass, incident.EventsCount))
		if incident.LastRelease != "" {
			envStr := incident.Environment
			if envStr == "" {
				envStr = "production"
			}
			sb.WriteString(fmt.Sprintf("📦 Релиз: `%s` | Среда: `%s`\n", incident.LastRelease, envStr))
		}
		sb.WriteString(fmt.Sprintf("`%s:%d`\n", incident.File, incident.Line))
		sb.WriteString(fmt.Sprintf("_%s_\n\n", incident.Message))
	}

	if err := a.sender.SendMessage(ctx, sb.String()); err != nil {
		log.Printf("Failed to send alert: %v\n", err)
	}
}
