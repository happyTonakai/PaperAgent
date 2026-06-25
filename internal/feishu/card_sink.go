package feishu

import (
	"fmt"
	"log"
	"strings"
)

// chatCardSlot tracks a single Feishu card within a multi-card chat reply.
// startAt is a byte offset into the total accumulated answer string,
// set during streaming in patchActive and consumed in OnDone to slice
// out the final per-card content. It must stay aligned with byte
// offsets produced by fitMarkdownContent (no re-normalization, no
// whitespace trimming) so the slice yields exactly what was rendered.
type chatCardSlot struct {
	id      string
	startAt int
}

// cardSink streams a chat answer into a sequence of Feishu interactive
// cards. The first card carries the paper title (via buildChatStreamingCard);
// subsequent cards are continuation cards (buildChatStreamingContinuationCard).
// Cards are split automatically when a single card would exceed the
// platform's content limits, using fitMarkdownContent to find a safe
// boundary that doesn't fall inside a table or code block.
//
// LaTeX math expressions in the LLM output are converted to Unicode
// (latexToUnicode) before being rendered, because Feishu's markdown
// rendering of inline math is unreliable.
//
// cardSink is not safe for concurrent use; the engine calls its methods
// serially from a single goroutine.
type cardSink struct {
	bot      *Bot
	chatID   string
	paperID  string
	title    string
	round    int
	patchMin int // re-patch the active card once this many bytes have accumulated since the last patch

	slots     []chatCardSlot
	total     strings.Builder
	lastPatch int
}

// newCardSink creates a cardSink with the given initial card ID. The
// caller is responsible for having sent the thinking card and obtained
// its ID before calling newCardSink. round is the chat round number
// (paper.CurrentRound() + 1 at the time the request was accepted); it
// is included in the final "done" card so the user can see which
// round this is.
func newCardSink(bot *Bot, chatID, paperID, title, initialCardID string, round int) *cardSink {
	return &cardSink{
		bot:      bot,
		chatID:   chatID,
		paperID:  paperID,
		title:    title,
		round:    round,
		patchMin: 200,
		slots:    []chatCardSlot{{id: initialCardID, startAt: 0}},
	}
}

// OnChunk appends text to the running total and, when enough new text
// has accumulated since the last patch, refreshes the active card —
// splitting into a new continuation card if the content would overflow
// Feishu's limits.
func (s *cardSink) OnChunk(text string) error {
	s.total.WriteString(text)
	total := s.total.String()

	if len(total)-s.lastPatch < s.patchMin {
		return nil
	}
	s.lastPatch = len(total)
	return s.patchActive(total)
}

// patchActive re-renders the active card from its startAt offset,
// splitting off a new continuation card if the content overflows.
//
// Error-handling note: this method returns errors, but the engine's
// streamOnce treats a non-nil OnChunk return as advisory — it logs and
// keeps streaming rather than aborting. So if sendInteractiveCard fails
// here (no new slot is appended), the next OnChunk call re-enters with
// the same active slot and re-patches it with the now-larger total; the
// frozen snapshot is overwritten rather than preserved. This is an
// accepted edge case (card send failures are rare) and matches the prior
// inline behavior in cmdChat.
func (s *cardSink) patchActive(total string) error {
	active := &s.slots[len(s.slots)-1]
	cardContent := total[active.startAt:]
	isFirst := len(s.slots) == 1

	fits, overflow := fitMarkdownContent(cardContent, func(c string) string {
		converted := latexToUnicode(c)
		if isFirst {
			return buildChatStreamingCard(s.paperID, s.title, converted)
		}
		return buildChatStreamingContinuationCard(converted)
	})

	if overflow != "" {
		// Freeze current card as a continuation card (converted).
		if err := s.bot.patchCard(active.id, buildContinuationCard(latexToUnicode(fits))); err != nil {
			return fmt.Errorf("freeze card: %w", err)
		}

		// Send new streaming continuation card (converted).
		overflowStart := active.startAt + len(fits)
		overflowContent := total[overflowStart:]
		newID, err := s.bot.sendInteractiveCard(s.chatID, buildChatStreamingContinuationCard(latexToUnicode(overflowContent)))
		if err != nil {
			return fmt.Errorf("send overflow card: %w", err)
		}
		if newID != "" {
			s.slots = append(s.slots, chatCardSlot{id: newID, startAt: overflowStart})
			log.Printf("[feishu] chat card full -> card #%d (total so far: %d chars)", len(s.slots), len(total))
		}
		return nil
	}

	// Fits in current card; just patch it.
	if isFirst {
		return s.bot.patchCard(active.id, buildChatStreamingCard(s.paperID, s.title, latexToUnicode(fits)))
	}
	return s.bot.patchCard(active.id, buildChatStreamingContinuationCard(latexToUnicode(fits)))
}

// OnToolCall is a no-op for the Feishu path. Tool-call visualization in
// cards is part of a separate fix and is not in scope for this refactor.
func (s *cardSink) OnToolCall(name string) {}

// OnDone replaces the last streaming card with a "done" card that
// includes round number and token counts. If the final content still
// doesn't fit in a single card (e.g., the LLM produced a very long
// trailing paragraph after the last split), an extra done card is sent.
func (s *cardSink) OnDone(answer string, promptTokens, completionTokens, cachedTokens int) {
	if len(s.slots) == 0 {
		return
	}
	// Index s.total rather than the answer argument. Both are equal today
	// (the engine feeds buf and the sink the same chunks in the same order,
	// and tool-message persistence touches neither), but using the sink's
	// own accumulated total keeps OnDone self-consistent with the byte
	// offsets we tracked in patchActive even if that coupling ever changes.
	total := s.total.String()
	last := &s.slots[len(s.slots)-1]
	lastContent := total[last.startAt:]

	fits, overflow := fitMarkdownContent(lastContent, func(c string) string {
		return buildChatDoneCard(s.paperID, s.title, latexToUnicode(c), s.round, promptTokens, completionTokens, cachedTokens)
	})

	if overflow != "" {
		if err := s.bot.patchCard(last.id, buildContinuationCard(latexToUnicode(fits))); err != nil {
			log.Printf("[feishu] patchCard final: %v", err)
		}
		_, _ = s.bot.sendInteractiveCard(s.chatID, buildChatDoneCard(s.paperID, s.title, latexToUnicode(overflow), s.round, promptTokens, completionTokens, cachedTokens))
		return
	}
	if err := s.bot.patchCard(last.id, buildChatDoneCard(s.paperID, s.title, latexToUnicode(fits), s.round, promptTokens, completionTokens, cachedTokens)); err != nil {
		log.Printf("[feishu] patchCard done: %v", err)
	}
}

// OnError emits a plain-text error message; the partial card content
// (if any) remains visible above the error.
func (s *cardSink) OnError(err error) {
	s.bot.sendText(s.chatID, fmt.Sprintf("❌ 回答失败：%v", err))
}
