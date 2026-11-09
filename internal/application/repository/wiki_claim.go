package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// wikiClaimRepository implements interfaces.WikiClaimRepository on GORM.
type wikiClaimRepository struct {
	db *gorm.DB
}

// NewWikiClaimRepository constructs the repository.
func NewWikiClaimRepository(db *gorm.DB) interfaces.WikiClaimRepository {
	return &wikiClaimRepository{db: db}
}

// ListActiveByScope returns the active claims for one page.
func (r *wikiClaimRepository) ListActiveByScope(
	ctx context.Context, kbID, slug string,
) ([]*types.WikiPageClaim, error) {
	var rows []*types.WikiPageClaim
	err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND slug = ? AND status = ?", kbID, slug, types.WikiClaimActive).
		Find(&rows).Error
	return rows, err
}

// StripKnowledgeCitations removes one knowledge's citations from the active
// claims of a page. Claims left with no citations are superseded; shared
// claims stay active with the remaining citations.
func (r *wikiClaimRepository) StripKnowledgeCitations(
	ctx context.Context, kbID, slug, knowledgeID string,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []*types.WikiPageClaim
		if err := tx.Where("knowledge_base_id = ? AND slug = ? AND status = ?",
			kbID, slug, types.WikiClaimActive).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			kept, changed := filterClaimCitations(row.Citations, knowledgeID)
			if !changed {
				continue
			}
			if len(kept) == 0 {
				if err := tx.Model(row).Updates(map[string]interface{}{
					"status":     types.WikiClaimSuperseded,
					"updated_at": time.Now(),
				}).Error; err != nil {
					return err
				}
				continue
			}
			raw, _ := json.Marshal(kept)
			if err := tx.Model(row).Updates(map[string]interface{}{
				"citations":  types.JSON(raw),
				"updated_at": time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// filterClaimCitations removes the knowledge's citation pairs and reports
// whether anything changed.
func filterClaimCitations(raw types.JSON, knowledgeID string) ([]types.WikiClaimCitation, bool) {
	var citations []types.WikiClaimCitation
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &citations)
	}
	kept := citations[:0]
	changed := false
	for _, c := range citations {
		if c.KnowledgeID == knowledgeID {
			changed = true
			continue
		}
		kept = append(kept, c)
	}
	return kept, changed
}

// InsertClaims inserts new active claim rows.
func (r *wikiClaimRepository) InsertClaims(ctx context.Context, claims []*types.WikiPageClaim) error {
	if len(claims) == 0 {
		return nil
	}
	now := time.Now()
	for _, c := range claims {
		c.CreatedAt = now
		c.UpdatedAt = now
	}
	return r.db.WithContext(ctx).Create(claims).Error
}

// SupersedeClaim marks one claim superseded.
func (r *wikiClaimRepository) SupersedeClaim(ctx context.Context, claimID int64) error {
	return r.db.WithContext(ctx).Model(&types.WikiPageClaim{}).
		Where("id = ? AND status = ?", claimID, types.WikiClaimActive).
		Updates(map[string]interface{}{"status": types.WikiClaimSuperseded, "updated_at": time.Now()}).Error
}

// MarkConflict marks one claim as conflicting.
func (r *wikiClaimRepository) MarkConflict(ctx context.Context, claimID int64) error {
	return r.db.WithContext(ctx).Model(&types.WikiPageClaim{}).
		Where("id = ? AND status = ?", claimID, types.WikiClaimActive).
		Updates(map[string]interface{}{"status": types.WikiClaimConflict, "updated_at": time.Now()}).Error
}

// AppendCitations merges citation pairs into an existing claim row.
func (r *wikiClaimRepository) AppendCitations(
	ctx context.Context, claimID int64, citations []types.WikiClaimCitation,
) error {
	if len(citations) == 0 {
		return nil
	}
	var row types.WikiPageClaim
	if err := r.db.WithContext(ctx).First(&row, claimID).Error; err != nil {
		return err
	}
	var existing []types.WikiClaimCitation
	if len(row.Citations) > 0 {
		_ = json.Unmarshal(row.Citations, &existing)
	}
	seen := make(map[string]struct{}, len(existing))
	for _, c := range existing {
		seen[c.KnowledgeID+"\x00"+c.ChunkID] = struct{}{}
	}
	changed := false
	for _, c := range citations {
		if _, ok := seen[c.KnowledgeID+"\x00"+c.ChunkID]; ok {
			continue
		}
		existing = append(existing, c)
		seen[c.KnowledgeID+"\x00"+c.ChunkID] = struct{}{}
		changed = true
	}
	if !changed {
		return nil
	}
	raw, _ := json.Marshal(existing)
	return r.db.WithContext(ctx).Model(&row).Updates(map[string]interface{}{
		"citations":  types.JSON(raw),
		"updated_at": time.Now(),
	}).Error
}

// RetractByKnowledge marks the given knowledge's active claims retracted on
// the given slugs.
func (r *wikiClaimRepository) RetractByKnowledge(
	ctx context.Context, kbID string, slugs []string, knowledgeIDs []string,
) error {
	if len(slugs) == 0 || len(knowledgeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&types.WikiPageClaim{}).
		Where("knowledge_base_id = ? AND slug IN ? AND knowledge_id IN ? AND status = ?",
			kbID, slugs, knowledgeIDs, types.WikiClaimActive).
		Update("status", types.WikiClaimRetracted).Error
}

// CountByScope counts active claims for one page (ops/test helper).
func (r *wikiClaimRepository) CountByScope(ctx context.Context, kbID, slug string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&types.WikiPageClaim{}).
		Where("knowledge_base_id = ? AND slug = ? AND status = ?", kbID, slug, types.WikiClaimActive).
		Count(&n).Error
	return n, err
}
