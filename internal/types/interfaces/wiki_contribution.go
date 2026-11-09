package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiContributionRepository persists the per-source contribution ledger for
// wiki pages (`wiki_page_contributions`). See types.WikiPageContribution for
// the row semantics.
type WikiContributionRepository interface {
	// ListActiveByScope returns the active contributions for one page.
	ListActiveByScope(ctx context.Context, kbID, slug string) ([]*types.WikiPageContribution, error)
	// ReplaceActiveForKnowledge marks every active contribution of
	// (kb, slug, knowledge) superseded and inserts the given replacements as
	// active rows, atomically.
	ReplaceActiveForKnowledge(ctx context.Context, kbID, slug, knowledgeID string, contributions []*types.WikiPageContribution) error
	// RetractByKnowledge marks every active contribution of the given
	// knowledge on the given slugs retracted.
	RetractByKnowledge(ctx context.Context, kbID string, slugs []string, knowledgeIDs []string) error
}
