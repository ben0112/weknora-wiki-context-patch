package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// wikiContributionRepository implements interfaces.WikiContributionRepository
// on top of GORM.
type wikiContributionRepository struct {
	db *gorm.DB
}

// NewWikiContributionRepository constructs the repository.
func NewWikiContributionRepository(db *gorm.DB) interfaces.WikiContributionRepository {
	return &wikiContributionRepository{db: db}
}

// ListActiveByScope returns the active contributions for one page.
func (r *wikiContributionRepository) ListActiveByScope(
	ctx context.Context, kbID, slug string,
) ([]*types.WikiPageContribution, error) {
	var rows []*types.WikiPageContribution
	err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND slug = ? AND status = ?", kbID, slug, types.WikiContributionActive).
		Find(&rows).Error
	return rows, err
}

// ReplaceActiveForKnowledge supersedes the active contributions of one
// (kb, slug, knowledge) and inserts the replacements as active rows.
func (r *wikiContributionRepository) ReplaceActiveForKnowledge(
	ctx context.Context, kbID, slug, knowledgeID string,
	contributions []*types.WikiPageContribution,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&types.WikiPageContribution{}).
			Where("knowledge_base_id = ? AND slug = ? AND knowledge_id = ? AND status = ?",
				kbID, slug, knowledgeID, types.WikiContributionActive).
			Update("status", types.WikiContributionSuperseded).Error; err != nil {
			return err
		}
		if len(contributions) == 0 {
			return nil
		}
		now := time.Now()
		for _, c := range contributions {
			c.CreatedAt = now
			c.UpdatedAt = now
		}
		return tx.Create(contributions).Error
	})
}

// RetractByKnowledge marks the active contributions of the given knowledge
// IDs on the given slugs retracted.
func (r *wikiContributionRepository) RetractByKnowledge(
	ctx context.Context, kbID string, slugs []string, knowledgeIDs []string,
) error {
	if len(slugs) == 0 || len(knowledgeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.WikiPageContribution{}).
		Where("knowledge_base_id = ? AND slug IN ? AND knowledge_id IN ? AND status = ?",
			kbID, slugs, knowledgeIDs, types.WikiContributionActive).
		Update("status", types.WikiContributionRetracted).Error
}
