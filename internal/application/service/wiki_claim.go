package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// wikiClaimsEnabled gates the claim ledger (and its extra LLM extraction
// calls) behind WEKNORA_WIKI_CLAIM_EXTRACTION. Default off so upstream-style
// deployments don't pay for claim extraction unexpectedly; the isolated patch
// stack turns it on for verification.
func wikiClaimsEnabled() bool {
	return strings.TrimSpace(os.Getenv("WEKNORA_WIKI_CLAIM_EXTRACTION")) == "1"
}

// normalizeWikiClaimKey fingerprints a claim for exact-match dedup: lowercase
// and keep only letters/digits, so punctuation, whitespace and case drift
// disappear.
func normalizeWikiClaimKey(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wikiClaimCharBigrams returns the set of 2-rune shingles of a normalized
// claim, used for cheap near-duplicate detection.
func wikiClaimCharBigrams(text string) map[string]struct{} {
	runes := []rune(strings.ToLower(text))
	out := make(map[string]struct{}, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i])+string(runes[i+1])] = struct{}{}
	}
	return out
}

// wikiClaimSimilarity is the Dice coefficient over 2-rune shingles. Two
// Chinese claims with trivial wording drift stay >= 0.8; unrelated claims sit
// far below.
func wikiClaimSimilarity(a, b string) float64 {
	ba, bb := wikiClaimCharBigrams(a), wikiClaimCharBigrams(b)
	if len(ba) == 0 || len(bb) == 0 {
		return 0
	}
	common := 0
	for k := range ba {
		if _, ok := bb[k]; ok {
			common++
		}
	}
	return 2 * float64(common) / float64(len(ba)+len(bb))
}

// wikiClaimMergeSimilarity is the threshold at which two differently-worded
// claims are treated as the same fact and merged into one claim with an extra
// citation.
const wikiClaimMergeSimilarity = 0.82

// wikiClaimCandidate is one extracted claim plus the section heading it was
// found under.
type wikiClaimCandidate struct {
	Claim   string `json:"claim"`
	Section string `json:"section"`
}

// parseWikiClaimCandidates parses the extraction LLM's JSON array. A failure
// falls back to the empty result — the caller treats the whole block as one
// claim rather than dropping the contribution.
func parseWikiClaimCandidates(text string) []wikiClaimCandidate {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	var out []wikiClaimCandidate
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(out))
	kept := out[:0]
	for _, c := range out {
		c.Claim = strings.TrimSpace(c.Claim)
		if c.Claim == "" {
			continue
		}
		key := normalizeWikiClaimKey(c.Claim)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, c)
	}
	return kept
}

// upsertPageClaims extracts atomic claims from this batch's new contribution
// blocks and merges them into the claim ledger: identical claims (exact key
// or near-duplicate wording) fold into the existing active claim as an extra
// citation; the batch knowledge's previous citations are stripped first so a
// re-ingest replaces rather than accumulates.
func (s *wikiIngestService) upsertPageClaims(
	ctx context.Context,
	chatModel chat.Chat,
	kbID, slug string,
	tenantID uint64,
	additions []SlugUpdate,
	chunkContentByID map[string]string,
) {
	if !wikiClaimsEnabled() || s.claimRepo == nil || len(additions) == 0 {
		return
	}
	// Replace semantics at claim level: drop this batch's knowledge from the
	// existing claims first (shared claims keep other citations).
	for _, kid := range uniqueKnowledgeIDs(additions) {
		if err := s.claimRepo.StripKnowledgeCitations(ctx, kbID, slug, kid); err != nil {
			logger.Warnf(ctx, "wiki ingest: claim strip for slug %s knowledge %s failed: %v", slug, kid, err)
		}
	}
	existing, err := s.claimRepo.ListActiveByScope(ctx, kbID, slug)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: claim lookup for slug %s failed: %v", slug, err)
		return
	}

	var toInsert []*types.WikiPageClaim
	for _, add := range additions {
		block, _ := wikiAdditionBlock(add, chunkContentByID)
		candidates := parseWikiClaimCandidates(s.extractClaimsText(ctx, chatModel, block))
		if len(candidates) == 0 {
			// Extraction failed (or returned nothing): keep the whole block as
			// one coarse claim so the ledger never silently loses a source.
			candidates = []wikiClaimCandidate{{Claim: block}}
		}
		citation := types.WikiClaimCitation{KnowledgeID: add.KnowledgeID, ChunkID: firstChunkID(add)}
		for _, c := range candidates {
			key := normalizeWikiClaimKey(c.Claim)
			merged := false
			for _, ex := range existing {
				if ex.ClaimKey != key && wikiClaimSimilarity(ex.ClaimText, c.Claim) < wikiClaimMergeSimilarity {
					continue
				}
				if err := s.claimRepo.AppendCitations(ctx, ex.ID, []types.WikiClaimCitation{citation}); err != nil {
					logger.Warnf(ctx, "wiki ingest: claim citation merge id=%d failed: %v", ex.ID, err)
				}
				merged = true
				break
			}
			if merged {
				continue
			}
			toInsert = append(toInsert, &types.WikiPageClaim{
				TenantID:        tenantID,
				KnowledgeBaseID: kbID,
				Slug:            slug,
				KnowledgeID:     add.KnowledgeID,
				ChunkID:         citation.ChunkID,
				SectionKey:      strings.TrimSpace(c.Section),
				ClaimKey:        key,
				ClaimText:       c.Claim,
				Citations:       types.JSON(mustJSONCitations([]types.WikiClaimCitation{citation})),
				Status:          types.WikiClaimActive,
			})
		}
	}
	if err := s.claimRepo.InsertClaims(ctx, toInsert); err != nil {
		logger.Warnf(ctx, "wiki ingest: claim insert for slug %s failed: %v", slug, err)
		return
	}
	if len(toInsert) > 0 {
		logger.Infof(ctx, "wiki ingest: claim ledger for slug %s: %d new claims inserted", slug, len(toInsert))
	}
}

// extractClaimsText runs one claim-extraction LLM call for a contribution
// block. On any failure it returns "" and the caller falls back to the whole
// block as a single coarse claim.
func (s *wikiIngestService) extractClaimsText(ctx context.Context, chatModel chat.Chat, block string) string {
	out, err := s.generateWithTemplate(ctx, chatModel,
		agent.WikiClaimExtractionSystemPrompt+"\n\n"+agent.WikiClaimExtractionUserPrompt,
		map[string]string{"Block": block})
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: claim extraction failed: %v", err)
		return ""
	}
	return out
}

// uniqueKnowledgeIDs returns the distinct knowledge IDs of a batch.
func uniqueKnowledgeIDs(additions []SlugUpdate) []string {
	seen := make(map[string]struct{}, len(additions))
	out := make([]string, 0, len(additions))
	for _, a := range additions {
		if a.KnowledgeID == "" {
			continue
		}
		if _, ok := seen[a.KnowledgeID]; ok {
			continue
		}
		seen[a.KnowledgeID] = struct{}{}
		out = append(out, a.KnowledgeID)
	}
	return out
}

// firstChunkID picks the first cited chunk for the claim's chunk anchor.
func firstChunkID(add SlugUpdate) string {
	if len(add.SourceChunks) > 0 {
		return add.SourceChunks[0]
	}
	return ""
}

// mustJSONCitations renders a citation list as JSON.
func mustJSONCitations(citations []types.WikiClaimCitation) []byte {
	raw, err := json.Marshal(citations)
	if err != nil {
		return []byte("[]")
	}
	return raw
}
