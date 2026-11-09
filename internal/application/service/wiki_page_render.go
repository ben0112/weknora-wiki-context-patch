package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// claimProjectionInput is the scoped claim projection used by
// renderWikiPageFromClaimsScoped.
type claimProjectionInput struct {
	kbID, slug, pageTitle, pageType, language string
	existingBody, availableSlugs              string
	tenantID                                  uint64
}

// wikiLegacyKnowledgeID marks claims synthesized from a pre-ledger page
// during the lazy backfill migration.
const wikiLegacyKnowledgeID = "legacy"

// legacyTenantID guards the backfill tenant: 0 means unknown and is stored
// as-is for un-scoped deployments.
func (s *wikiIngestService) legacyTenantID(t uint64) uint64 { return t }

// renderWikiPageFromClaimsScoped is the scoped entry point: claims are
// fetched for (kbID, slug), grouped by section heading, and each affected
// section is re-rendered from its claims.
func (s *wikiIngestService) renderWikiPageFromClaimsScoped(
	ctx context.Context,
	chatModel chat.Chat,
	in claimProjectionInput,
) (string, error) {
	if s.claimRepo == nil {
		return "", fmt.Errorf("claim repository unavailable")
	}
	active, err := s.claimRepo.ListActiveByScope(ctx, in.kbID, in.slug)
	if err != nil {
		return "", err
	}
	conflictRows, err := s.claimRepo.ListConflictByScope(ctx, in.kbID, in.slug)
	if err != nil {
		return "", err
	}

	preamble, sections := splitWikiPageSections(in.existingBody)
	if len(sections) < 2 {
		// A single-block page has no stable section anchors; leave it to the
		// full-modify path instead of risking a wholesale rewrite.
		return "", fmt.Errorf("%w: projection needs >=2 headings, page has %d", ErrWikiContextOverflow, len(sections))
	}

	// Legacy migration (slice 3b): a page built before the claim ledger has no
	// claims yet. Adopt it by turning each of its sections into one coarse
	// claim, so the next update renders from the ledger instead of the whole
	// page. Idempotent: only runs when BOTH lists are empty.
	if len(active) == 0 && len(conflictRows) == 0 {
		legacy := make([]*types.WikiPageClaim, 0, len(sections))
		for _, sec := range sections {
			body := strings.TrimSpace(sec.body)
			if body == "" {
				continue
			}
			legacy = append(legacy, &types.WikiPageClaim{
				TenantID:        s.legacyTenantID(in.tenantID),
				KnowledgeBaseID: in.kbID,
				Slug:            in.slug,
				KnowledgeID:     wikiLegacyKnowledgeID,
				SectionKey:      strings.TrimSpace(sec.heading),
				ClaimKey:        normalizeWikiClaimKey(body),
				ClaimText:       body,
				Citations:       types.JSON([]byte("[]")),
				Status:          types.WikiClaimActive,
			})
		}
		if len(legacy) > 0 {
			if err := s.claimRepo.InsertClaims(ctx, legacy); err != nil {
				logger.Warnf(ctx, "wiki ingest: legacy claim backfill for slug %s failed: %v", in.slug, err)
			} else {
				active = legacy
				logger.Infof(ctx, "wiki ingest: legacy claim backfill adopted %d sections for slug %s", len(legacy), in.slug)
			}
		}
	}
	if len(active) == 0 && len(conflictRows) == 0 {
		return "", fmt.Errorf("%w: no claims for projection", ErrWikiContextOverflow)
	}

	// Group claims by the page heading they belong to.
	headingOf := func(sectionKey string) string {
		sk := strings.TrimSpace(sectionKey)
		for _, sec := range sections {
			h := strings.TrimSpace(sec.heading)
			if sk == h || strings.HasPrefix(sk, h+" ") || strings.HasPrefix(h, sk) {
				return h
			}
		}
		return ""
	}
	byHeading := make(map[string][]*types.WikiPageClaim)
	for _, c := range active {
		h := headingOf(c.SectionKey)
		byHeading[h] = append(byHeading[h], c)
	}
	conflictByHeading := make(map[string][]*types.WikiPageClaim)
	for _, c := range conflictRows {
		h := headingOf(c.SectionKey)
		conflictByHeading[h] = append(conflictByHeading[h], c)
	}

	rendered := make([]wikiPageSection, 0, len(sections))
	for _, sec := range sections {
		h := strings.TrimSpace(sec.heading)
		secClaims := byHeading[h]
		if len(secClaims) == 0 && len(conflictByHeading[h]) == 0 {
			rendered = append(rendered, sec) // unchanged
			continue
		}
		var conflictLines strings.Builder
		for _, c := range conflictByHeading[h] {
			src := c.KnowledgeID
			if len(src) > 8 {
				src = src[:8]
			}
			fmt.Fprintf(&conflictLines, "- %s（来源：%s）\n", c.ClaimText, src)
		}
		sort.SliceStable(secClaims, func(i, j int) bool { return secClaims[i].ID < secClaims[j].ID })
		var claimsBlock strings.Builder
		for _, c := range secClaims {
			fmt.Fprintf(&claimsBlock, "- %s\n", c.ClaimText)
		}
		out, err := s.generateWithTemplate(ctx, chatModel,
			agent.WikiPageSectionRenderSystemPrompt+"\n\n"+agent.WikiPageSectionRenderUserPrompt,
			map[string]string{
				"PageSlug":       in.slug,
				"PageTitle":      in.pageTitle,
				"PageType":       in.pageType,
				"SectionHeading": h,
				"Claims":         claimsBlock.String(),
				"Conflicts":      conflictLines.String(),
				"AvailableSlugs": in.availableSlugs,
				"Language":       in.language,
			})
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(out)
		if !strings.HasPrefix(trimmed, h) {
			logger.Warnf(ctx, "wiki ingest: claim projection for slug %s changed heading %q; keeping original section",
				in.slug, h)
			rendered = append(rendered, sec)
			continue
		}
		rendered = append(rendered, wikiPageSection{heading: sec.heading, body: strings.TrimSpace(strings.TrimPrefix(trimmed, h))})
	}

	merged := mergeWikiPageSections(preamble, rendered)
	origHeadings := wikiPageHeadings(in.existingBody)
	if !wikiPageHeadingsPreserved(origHeadings, wikiPageHeadings(merged)) {
		return "", fmt.Errorf("%w: claim projection would drop an original heading", ErrWikiContextOverflow)
	}
	return merged, nil
}
