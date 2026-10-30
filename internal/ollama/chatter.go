package ollama

import (
	"context"

	"speakeasy/internal/llm"
)

// Chatter adapts a Client to the provider-neutral llm.Chatter interface.
type Chatter struct{ C *Client }

func (c Chatter) ChatStream(ctx context.Context, r llm.Request, onDelta func(string) error) (llm.Stats, error) {
	opts := map[string]any{
		"num_predict": r.MaxTokens,
		"temperature": r.Temperature,
	}
	if r.NumCtx > 0 {
		opts["num_ctx"] = r.NumCtx
	}
	msgs := make([]Message, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = Message{Role: m.Role, Content: m.Content}
	}
	st, err := c.C.ChatStream(ctx, ChatRequest{
		Model:     r.Model,
		Messages:  msgs,
		Think:     r.Think,
		Format:    r.JSONSchema,
		KeepAlive: r.KeepAlive,
		Options:   opts,
	}, onDelta)
	return llm.Stats{PromptTokens: st.PromptTokens, CompletionTokens: st.EvalTokens}, err
}
