// Package llm defines the small, provider-neutral chat interface the tutor
// needs. Ollama and OpenAI-compatible APIs (Groq, xAI, ...) both implement it.
package llm

import (
	"context"
	"encoding/json"
)

type Message struct {
	Role    string
	Content string
}

type Request struct {
	Model       string
	Messages    []Message
	MaxTokens   int // hard cap on generated tokens
	Temperature float64

	// JSONSchema, when set, asks for a single JSON object as the reply.
	// Ollama enforces the schema; OpenAI-compatible APIs get JSON mode and
	// rely on the prompt to describe the shape.
	JSONSchema json.RawMessage

	// Ollama only; other providers ignore these.
	NumCtx    int
	KeepAlive string
	Think     *bool
}

type Stats struct {
	PromptTokens     int // 0 when the provider does not report it
	CompletionTokens int
}

// Chatter streams one chat completion, calling onDelta for every piece of
// content. Returning an error from onDelta aborts the request.
type Chatter interface {
	ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Stats, error)
}
