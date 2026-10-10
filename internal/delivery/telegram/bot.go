package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"server/internal/domain"
)

type BotSender struct {
	botToken string
	chatID   string
}

func NewBotSender(botToken, chatID string) domain.AlertSender {
	if botToken == "" || chatID == "" {
		return nil
	}
	return &BotSender{
		botToken: botToken,
		chatID:   chatID,
	}
}

func (b *BotSender) SendMessage(ctx context.Context, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.botToken)

	payload := map[string]interface{}{
		"chat_id":    b.chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status: %d", resp.StatusCode)
	}

	log.Println("Successfully sent Telegram alert.")
	return nil
}
