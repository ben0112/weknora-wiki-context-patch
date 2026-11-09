package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiAdditionBlock renders the verbatim evidence block one addition
// contributes to a page, in exactly the format the editor prompt receives
// (<document>…</document>). The same string is persisted in the contribution
// ledger; the hash (see wikiAdditionHash) is computed over a
// whitespace-normalized form of it.
func wikiAdditionBlock(add SlugUpdate, chunkContentByID map[string]string) (string, []string) {
	cited := collectCitedChunkContent(add.SourceChunks, chunkContentByID)
	var b strings.Builder
	if cited != "" {
		fmt.Fprintf(&b,
			"<document>\n<title>%s</title>\n<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
			add.DocTitle, add.Item.Name, add.Item.Description, cited)
	} else {
		fmt.Fprintf(&b,
			"<document>\n<title>%s</title>\n<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
			add.DocTitle, add.Item.Name, add.Item.Description, add.Item.Details)
	}
	return b.String(), add.SourceChunks
}

// normalizeWikiText collapses every whitespace run into a single space and
// trims the edges, so chunk-boundary and separator differences disappear.
func normalizeWikiText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// wikiAdditionHash fingerprints one contribution block. Re-parsing a
// document re-chunks it, which shifts chunk boundaries and separators
// without changing the underlying text; hashing the whitespace-normalized
// block keeps the two renderings comparable, so unchanged content produces
// an empty delta instead of a re-run page rewrite.
func wikiAdditionHash(block string) string {
	sum := sha256.Sum256([]byte(normalizeWikiText(block)))
	return hex.EncodeToString(sum[:])
}

// filterUnchangedAdditions drops additions whose evidence block is already
// recorded as an active contribution of the same (page, knowledge): a repeat
// ingest with unchanged evidence produces no delta and must not re-run the
// page rewrite. New blocks and changed blocks are kept.
func (s *wikiIngestService) filterUnchangedAdditions(
	ctx context.Context, tenantID uint64, kbID, slug string, additions []SlugUpdate,
) ([]SlugUpdate, error) {
	chunkContentByID := s.resolveCitedChunks(ctx, tenantID, additions)
	existing, err := s.contribRepo.ListActiveByScope(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}
	hashByKnowledge := make(map[string]map[string]struct{}, len(existing))
	for _, c := range existing {
		set := hashByKnowledge[c.KnowledgeID]
		if set == nil {
			set = make(map[string]struct{})
			hashByKnowledge[c.KnowledgeID] = set
		}
		set[c.ContentHash] = struct{}{}
	}

	kept := make([]SlugUpdate, 0, len(additions))
	batchSeen := make(map[string]struct{}, len(additions))
	for _, add := range additions {
		block, _ := wikiAdditionBlock(add, chunkContentByID)
		h := wikiAdditionHash(block)
		key := add.KnowledgeID + "\x00" + h
		if _, ok := hashByKnowledge[add.KnowledgeID][h]; ok {
			continue
		}
		if _, dup := batchSeen[key]; dup {
			continue
		}
		batchSeen[key] = struct{}{}
		kept = append(kept, add)
	}
	if len(kept) < len(additions) {
		logger.Infof(ctx, "wiki ingest: contribution delta for slug %s: %d/%d additions unchanged and skipped",
			slug, len(additions)-len(kept), len(additions))
	}
	return kept, nil
}

// persistContributions records the kept additions' evidence blocks in the
// ledger after a successful page write, superseding the previous blocks of
// the same knowledge. It is best-effort: ledger failures must not fail the
// page write.
func (s *wikiIngestService) persistContributions(
	ctx context.Context, kbID, slug string, tenantID uint64,
	additions []SlugUpdate, chunkContentByID map[string]string,
) {
	if s.contribRepo == nil || len(additions) == 0 {
		return
	}
	byKnowledge := make(map[string][]*types.WikiPageContribution)
	for _, add := range additions {
		block, chunkIDs := wikiAdditionBlock(add, chunkContentByID)
		encoded, err := json.Marshal(chunkIDs)
		if err != nil {
			encoded = []byte("[]")
		}
		byKnowledge[add.KnowledgeID] = append(byKnowledge[add.KnowledgeID], &types.WikiPageContribution{
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			Slug:            slug,
			KnowledgeID:     add.KnowledgeID,
			ChunkIDs:        types.JSON(encoded),
			Content:         block,
			ContentHash:     wikiAdditionHash(block),
			Status:          types.WikiContributionActive,
		})
	}
	for knowledgeID, rows := range byKnowledge {
		if err := s.contribRepo.ReplaceActiveForKnowledge(ctx, kbID, slug, knowledgeID, rows); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to persist contributions for slug %s knowledge %s: %v",
				slug, knowledgeID, err)
		}
	}
}

// retractContributions marks the deleted sources' active contributions on the
// slug retracted after the page write succeeded. Best-effort.
func (s *wikiIngestService) retractContributions(
	ctx context.Context, kbID, slug string, knowledgeIDs []string,
) {
	if s.contribRepo == nil || len(knowledgeIDs) == 0 {
		return
	}
	if err := s.contribRepo.RetractByKnowledge(ctx, kbID, []string{slug}, knowledgeIDs); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to retract contributions for slug %s: %v", slug, err)
	}
}
