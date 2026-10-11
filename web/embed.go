package web

import "embed"

// TemplatesFS embeds all html templates for the UI
//
//go:embed templates/*
var TemplatesFS embed.FS
