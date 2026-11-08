package service

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

func TestWikiEstimateMessageTokens(t *testing.T) {
	cases := []struct {
		name     string
		messages []chat.Message
		want     int
	}{
		{"ascii", []chat.Message{{Role: "user", Content: "hello world, 1234"}}, 11},
		{"cjk", []chat.Message{{Role: "user", Content: "中华人民共和国"}}, 13},
		{"system+user overhead", []chat.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "user text"},
		}, 13},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wikiEstimateMessageTokens(tc.messages)
			if got != tc.want {
				t.Fatalf("wikiEstimateMessageTokens(%q)=%d, want %d",
					tc.messages[0].Content, got, tc.want)
			}
		})
	}
}

func TestWikiOutputBudget(t *testing.T) {
	// The exact production failure shape: 233168 prompt tokens against a
	// 262144 window leaves 26928 tokens after the safety margin, so the
	// completion budget must be clamped below the fixed 32768.
	got, err := wikiOutputBudget(262144, 233168)
	if err != nil {
		t.Fatalf("wikiOutputBudget returned error: %v", err)
	}
	if got != 26928 {
		t.Fatalf("wikiOutputBudget(262144, 233168)=%d, want 26928", got)
	}

	// Small prompts keep the full fixed budget.
	if got, err := wikiOutputBudget(262144, 1000); err != nil || got != wikiLLMMaxTokens {
		t.Fatalf("wikiOutputBudget(262144, 1000)=(%d,%v), want (%d,nil)", got, err, wikiLLMMaxTokens)
	}

	// A prompt that leaves no room for the minimum output must fail fast with
	// the terminal sentinel instead of being sent to the provider.
	_, err = wikiOutputBudget(262144, 261000)
	if err == nil {
		t.Fatal("wikiOutputBudget(262144, 261000) returned nil error, want overflow")
	}
	if !errors.Is(err, ErrWikiContextOverflow) {
		t.Fatalf("error %v does not wrap ErrWikiContextOverflow", err)
	}
}
