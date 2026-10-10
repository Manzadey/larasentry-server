package main

import "time"

// EventPayload represents the incoming JSON from Laravel
type EventPayload struct {
	ExceptionClass string `json:"exception_class"`
	Message        string `json:"message"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	StackTrace     string `json:"stack_trace"`
	AppURL         string `json:"app_url"`
	RequestURL     string `json:"request_url"`
	RequestMethod  string `json:"request_method"`
}

// Project represents a client application
type Project struct {
	ID     int64
	Name   string
	APIKey string
}

// Incident represents a grouped error in the database
type Incident struct {
	ID             int64
	ProjectID      int64
	Hash           string
	ExceptionClass string
	Message        string
	File           string
	Line           int
	StackTrace     string
	AppURL         string
	RequestURL     string
	EventsCount    int
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}
