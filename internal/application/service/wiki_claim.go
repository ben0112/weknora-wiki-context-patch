package service

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"
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
// citation. 0.78 over 2-rune shingles catches punctuation/measure-word drift
// ("给予不超过50%的补贴" vs "给予不超过50%的补贴。") while leaving genuinely
// different facts (which typically land below 0.6) apart.
const wikiClaimMergeSimilarity = 0.78

// wikiClaimConflictSimilarity is the lower bound at which two claims are
// considered to be ABOUT the same fact; below it they are unrelated. When
// similarity lands between this bound and the merge threshold AND the numeric
// values differ, the pair is a contradiction rather than a duplicate.
const wikiClaimConflictSimilarity = 0.55

// wikiClaimCandidate is one extracted claim plus the section heading it was
// found under.
type wikiClaimCandidate struct {
	Claim   string `json:"claim"`
	Section string `json:"section"`
}

// wikiClaimNumberRE extracts numbers (with optional decimals and percent
// sign) for value comparison between claims.
var wikiClaimNumberRE = regexp.MustCompile(`\d+(?:\.\d+)?%?`)

// wikiClaimNumbers returns the set of numeric values appearing in a claim.
func wikiClaimNumbers(text string) map[string]struct{} {
	matches := wikiClaimNumberRE.FindAllString(text, -1)
	out := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		out[m] = struct{}{}
	}
	return out
}

// wikiClaimNumbersDiffer reports whether two claims carry different numeric
// values (including one side having none).
func wikiClaimNumbersDiffer(a, b string) bool {
	na, nb := wikiClaimNumbers(a), wikiClaimNumbers(b)
	if len(na) != len(nb) {
		return true
	}
	for k := range na {
		if _, ok := nb[k]; !ok {
			return true
		}
	}
	return false
}

// wikiClaimMergeDecision is the outcome of comparing a new claim against an
// existing active claim.
type wikiClaimMergeDecision int

const (
	wikiClaimMergeDecisionInsert    wikiClaimMergeDecision = iota
	wikiClaimMergeDecisionCite                             // same fact: merge citation
	wikiClaimMergeDecisionSupersede                        // same fact, newer value: replace
	wikiClaimMergeDecisionConflict                         // same fact, contradictory values
)

// wikiClaimMergeDecision decides how a new claim relates to an existing
// active claim:
//   - identical (key) or near-identical wording with equal values → citation merge;
//   - same fact with different numeric values → supersede (the newer claim wins);
//   - same-topic wording with different values below the merge threshold → conflict;
//   - otherwise unrelated → insert new.
func wikiClaimMergeDecisionFor(existing *types.WikiPageClaim, cand wikiClaimCandidate) wikiClaimMergeDecision {
	key := normalizeWikiClaimKey(cand.Claim)
	sim := wikiClaimSimilarity(existing.ClaimText, cand.Claim)
	differ := wikiClaimNumbersDiffer(existing.ClaimText, cand.Claim)
	switch {
	case existing.ClaimKey == key || sim >= wikiClaimMergeSimilarity:
		if differ {
			return wikiClaimMergeDecisionSupersede
		}
		return wikiClaimMergeDecisionCite
	case sim >= wikiClaimConflictSimilarity && differ:
		return wikiClaimMergeDecisionConflict
	default:
		return wikiClaimMergeDecisionInsert
	}
}

// wikiClaimDateRE matches YYYY-MM-DD / YYYY/MM/DD / YYYY年M月D日 dates.
var wikiClaimDateRE = regexp.MustCompile(`(\d{4})[年/\-](\d{1,2})[月/\-](\d{1,2})日?`)

// wikiClaimTimeRange extracts valid_from/valid_to from explicit dates in the
// claim text. The first date becomes valid_from and, when a second date is
// present, the later one becomes valid_to.
func wikiClaimTimeRange(text string) (*time.Time, *time.Time) {
	matches := wikiClaimDateRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, nil
	}
	parse := func(m []string) time.Time {
		t, err := time.Parse("2006-1-2", m[1]+"-"+m[2]+"-"+m[3])
		if err != nil {
			return time.Time{}
		}
		return t
	}
	from := parse(matches[0])
	var to *time.Time
	if len(matches) > 1 {
		if v := parse(matches[1]); !v.IsZero() {
			to = &v
		}
	}
	if from.IsZero() {
		return nil, to
	}
	return &from, to
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
			action := wikiClaimMergeDecisionInsert
			target := -1
			for i := range existing {
				d := wikiClaimMergeDecisionFor(existing[i], c)
				if d == wikiClaimMergeDecisionInsert {
					continue
				}
				// Prefer the strongest relation: supersede > conflict > cite.
				if d > action {
					action = d
					target = i
				}
			}
			from, to := wikiClaimTimeRange(c.Claim)
			switch action {
			case wikiClaimMergeDecisionCite:
				if err := s.claimRepo.AppendCitations(ctx, existing[target].ID, []types.WikiClaimCitation{citation}); err != nil {
					logger.Warnf(ctx, "wiki ingest: claim citation merge id=%d failed: %v", existing[target].ID, err)
				}
				continue
			case wikiClaimMergeDecisionSupersede:
				old := existing[target]
				if err := s.claimRepo.SupersedeClaim(ctx, old.ID); err != nil {
					logger.Warnf(ctx, "wiki ingest: claim supersede id=%d failed: %v", old.ID, err)
				}
				carried := append(parseClaimCitations(old.Citations), citation)
				toInsert = append(toInsert, s.newWikiClaimRow(kbID, slug, tenantID, add, citation, c, key, carried, old.ID, from, to))
				existing = removeWikiClaimAt(existing, target)
				logger.Infof(ctx, "wiki ingest: claim superseded id=%d -> new claim for slug %s", old.ID, slug)
			case wikiClaimMergeDecisionConflict:
				old := existing[target]
				if err := s.claimRepo.MarkConflict(ctx, old.ID); err != nil {
					logger.Warnf(ctx, "wiki ingest: claim conflict mark id=%d failed: %v", old.ID, err)
				}
				toInsert = append(toInsert, s.newWikiClaimRow(kbID, slug, tenantID, add, citation, c, key,
					[]types.WikiClaimCitation{citation}, 0, from, to))
				existing = removeWikiClaimAt(existing, target)
				logger.Infof(ctx, "wiki ingest: claim conflict marked id=%d vs new claim for slug %s", old.ID, slug)
			default:
				toInsert = append(toInsert, s.newWikiClaimRow(kbID, slug, tenantID, add, citation, c, key,
					[]types.WikiClaimCitation{citation}, 0, from, to))
			}
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

// parseClaimCitations decodes a claim row's citations JSON.
func parseClaimCitations(raw types.JSON) []types.WikiClaimCitation {
	var citations []types.WikiClaimCitation
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &citations)
	}
	return citations
}

// newWikiClaimRow assembles one claim row for insertion.
func (s *wikiIngestService) newWikiClaimRow(
	kbID, slug string,
	tenantID uint64,
	add SlugUpdate,
	citation types.WikiClaimCitation,
	cand wikiClaimCandidate,
	key string,
	citations []types.WikiClaimCitation,
	supersedesID int64,
	from, to *time.Time,
) *types.WikiPageClaim {
	return &types.WikiPageClaim{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Slug:            slug,
		KnowledgeID:     add.KnowledgeID,
		ChunkID:         citation.ChunkID,
		SectionKey:      strings.TrimSpace(cand.Section),
		ClaimKey:        key,
		ClaimText:       cand.Claim,
		Citations:       types.JSON(mustJSONCitations(citations)),
		Status:          types.WikiClaimActive,
		SupersedesID:    supersedesID,
		ValidFrom:       from,
		ValidTo:         to,
	}
}

// removeWikiClaimAt drops index i from a claim slice (order not preserved).
func removeWikiClaimAt(claims []*types.WikiPageClaim, i int) []*types.WikiPageClaim {
	claims[i] = claims[len(claims)-1]
	return claims[:len(claims)-1]
}
