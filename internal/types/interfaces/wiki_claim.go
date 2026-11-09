package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiClaimRepository persists the claim ledger (`wiki_page_claims`).
type WikiClaimRepository interface {
	// ListActiveByScope returns the active claims for one page.
	ListActiveByScope(ctx context.Context, kbID, slug string) ([]*types.WikiPageClaim, error)
	// ReplaceActiveForKnowledge supersedes the active claims that cite ONLY
	// the given knowledge (rows whose citations become empty once that
	// knowledge is removed) and strips the knowledge's citations from shared
	// claims, in one transaction. It does not touch rows that still cite
	// other sources.
	StripKnowledgeCitations(ctx context.Context, kbID, slug, knowledgeID string) error
	// InsertClaims inserts new active claim rows.
	InsertClaims(ctx context.Context, claims []*types.WikiPageClaim) error
	// AppendCitations merges the given citations into an existing claim row.
	AppendCitations(ctx context.Context, claimID int64, citations []types.WikiClaimCitation) error
	// RetractByKnowledge marks the given knowledge's active claims retracted
	// on the given slugs.
	RetractByKnowledge(ctx context.Context, kbID string, slugs []string, knowledgeIDs []string) error
	// CountByScope is a test/ops helper.
	CountByScope(ctx context.Context, kbID, slug string) (int64, error)
}
