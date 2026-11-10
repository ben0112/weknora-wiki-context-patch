package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

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
	now := time.Now()
	for _, sec := range sections {
		h := strings.TrimSpace(sec.heading)
		secClaims := byHeading[h]
		if len(secClaims) == 0 && len(conflictByHeading[h]) == 0 {
			// Legacy migration (slice 3b): this pre-ledger section has no
			// claims yet. Adopt it as one coarse legacy claim but keep its
			// current text this round; the next update renders it from the
			// ledger.
			body := strings.TrimSpace(sec.body)
			if body != "" && s.claimRepo != nil {
				legacy := &types.WikiPageClaim{
					TenantID:        in.tenantID,
					KnowledgeBaseID: in.kbID,
					Slug:            in.slug,
					KnowledgeID:     wikiLegacyKnowledgeID,
					SectionKey:      h,
					ClaimKey:        normalizeWikiClaimKey(body),
					ClaimText:       body,
					Citations:       types.JSON([]byte("[]")),
					Status:          types.WikiClaimActive,
				}
				if err := s.claimRepo.InsertClaims(ctx, []*types.WikiPageClaim{legacy}); err != nil {
					logger.Warnf(ctx, "wiki ingest: legacy section backfill for slug %s heading %q failed: %v", in.slug, h, err)
				} else {
					logger.Infof(ctx, "wiki ingest: legacy section backfill adopted heading %q for slug %s", h, in.slug)
				}
			}
			rendered = append(rendered, sec) // unchanged this round
			continue
		}

		// Split current vs expired claims: expired ones move out of the main
		// prose and are listed in a 已失效说明 subsection instead.
		var claimsBlock, expiredLines, conflictLines strings.Builder
		for _, c := range secClaims {
			if c.ValidTo != nil && c.ValidTo.Before(now) {
				fmt.Fprintf(&expiredLines, "- %s（失效于 %s）\n", c.ClaimText, c.ValidTo.Format("2006-01-02"))
				continue
			}
			fmt.Fprintf(&claimsBlock, "- %s\n", c.ClaimText)
		}
		for _, c := range conflictByHeading[h] {
			src := c.KnowledgeID
			if len(src) > 8 {
				src = src[:8]
			}
			fmt.Fprintf(&conflictLines, "- %s（来源：%s）\n", c.ClaimText, src)
		}
		if strings.TrimSpace(claimsBlock.String()) == "" && strings.TrimSpace(conflictLines.String()) == "" {
			// Only expired claims: rewriting the section would empty it, so
			// keep the current text and append nothing this round.
			rendered = append(rendered, sec)
			continue
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
				"Expired":        expiredLines.String(),
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
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, h))
		// Deterministic guarantees: the model may skip the auxiliary
		// subsections, so append them verbatim when they are missing.
		if cf := strings.TrimSpace(conflictLines.String()); cf != "" && !strings.Contains(body, "Contradictions") {
			body += "\n\n### Contradictions / Updates\n" + cf
		}
		if ex := strings.TrimSpace(expiredLines.String()); ex != "" && !strings.Contains(body, "已失效说明") {
			body += "\n\n### 已失效说明\n" + ex
		}
		rendered = append(rendered, wikiPageSection{heading: sec.heading, body: body})
	}

	merged := mergeWikiPageSections(preamble, rendered)
	// Link discipline: a wiki link inside a heading line is never valid —
	// strip the markup deterministically so the heading stays plain text.
	merged = wikiStripHeadingLinks(merged)
	origHeadings := wikiPageHeadings(in.existingBody)
	if !wikiPageHeadingsPreserved(origHeadings, wikiPageHeadings(merged)) {
		return "", fmt.Errorf("%w: claim projection would drop an original heading", ErrWikiContextOverflow)
	}
	return merged, nil
}

// wikiHeadingLineRE matches an ATX heading line.
var wikiHeadingLineRE = regexp.MustCompile(`^#{1,6}[ \t]+.*$`)

// wikiStripHeadingLinks removes [[...]] markup from heading lines, keeping
// the display text (or the slug when there is no display text).
func wikiStripHeadingLinks(content string) string {
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		if !wikiHeadingLineRE.MatchString(line) || !strings.Contains(line, "[[") {
			continue
		}
		lines[i] = wikiLinkRE.ReplaceAllStringFunc(line, func(m string) string {
			sub := wikiLinkRE.FindStringSubmatch(m)
			if len(sub) == 3 && sub[2] != "" {
				return sub[2]
			}
			if len(sub) >= 2 {
				return sub[1]
			}
			return m
		})
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
}
