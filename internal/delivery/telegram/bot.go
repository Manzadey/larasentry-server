package telegram

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
	"server/internal/domain"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type BotSender struct {
	b      *bot.Bot
	chatID string
}

func NewBotSender(botToken, chatID, proxyURL string) domain.AlertSender {
	if botToken == "" || chatID == "" {
		return nil
	}

	var opts []bot.Option
	if proxyURL != "" {
		pURL, err := url.Parse(proxyURL)
		if err == nil {
			transport := &http.Transport{
				Proxy: http.ProxyURL(pURL),
			}
			httpClient := &http.Client{Transport: transport}
			opts = append(opts, bot.WithHTTPClient(time.Minute, httpClient))
		} else {
			log.Printf("Invalid proxy URL: %v", err)
		}
	}

	b, err := bot.New(botToken, opts...)
	if err != nil {
		log.Printf("Failed to create telegram bot: %v", err)
		return nil
	}

	return &BotSender{
		b:      b,
		chatID: chatID,
	}
}

func (s *BotSender) SendMessage(ctx context.Context, text string) error {
	_, err := s.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    s.chatID,
		Text:      text,
		ParseMode: models.ParseModeMarkdownV1,
	})
	if err != nil {
		return fmt.Errorf("telegram API returned error: %w", err)
	}

	log.Println("Successfully sent Telegram alert.")
	return nil
}
