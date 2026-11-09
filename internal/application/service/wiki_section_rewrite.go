package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// wikiSectionRewriteMaxSections bounds how many sections one sectioned rewrite
// may visit. A page with more sections than this is refused so a pathological
// hub page cannot silently multiply LLM calls.
const wikiSectionRewriteMaxSections = 16

// wikiSectionHeadingRe matches a single Markdown ATX heading line (any level).
var wikiSectionHeadingRe = regexp.MustCompile(`(?m)^(#{1,6}[ \t]+.*)$`)

// wikiPageSection is one heading-delimited block of a wiki page.
type wikiPageSection struct {
	heading string
	body    string
}

// splitWikiPageSections splits content into a preamble (text before the first
// heading) and heading-delimited sections. The returned sections each carry
// the heading line and the body that follows it (up to the next heading).
func splitWikiPageSections(content string) (preamble string, sections []wikiPageSection) {
	idx := wikiSectionHeadingRe.FindAllStringSubmatchIndex(content, -1)
	if len(idx) == 0 {
		return content, nil
	}
	preamble = content[:idx[0][0]]
	for i, m := range idx {
		heading := content[m[0]:m[1]]
		start := m[1]
		end := len(content)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		body := content[start:end]
		body = strings.TrimLeft(body, "\n")
		sections = append(sections, wikiPageSection{heading: heading, body: body})
	}
	return preamble, sections
}

// mergeWikiPageSections reassembles a page from its preamble and sections.
func mergeWikiPageSections(preamble string, sections []wikiPageSection) string {
	var b strings.Builder
	if preamble != "" {
		b.WriteString(strings.TrimRight(preamble, "\n"))
	}
	for _, s := range sections {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(s.heading)
		if s.body != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimRight(s.body, "\n"))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// wikiPageHeadings extracts every ATX heading line, in order.
func wikiPageHeadings(content string) []string {
	lines := wikiSectionHeadingRe.FindAllString(content, -1)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(l)
	}
	return out
}

// wikiPageHeadingsPreserved reports whether every original heading still
// appears at least as many times in the updated page. Extra headings (new
// sections the model appended) are allowed; a missing original heading means
// existing content was silently dropped and the rewrite must be rejected.
func wikiPageHeadingsPreserved(origHeadings, updatedHeadings []string) bool {
	counts := make(map[string]int, len(updatedHeadings))
	for _, h := range updatedHeadings {
		counts[h]++
	}
	for _, h := range origHeadings {
		if counts[h] == 0 {
			return false
		}
		counts[h]--
	}
	return true
}

// rewritePageInSections updates an existing page one section at a time. It is
// the fallback for a full-page modify whose prompt exceeds the model context
// window (ErrWikiContextOverflow): instead of failing (or trimming content),
// each heading-delimited section is rewritten with a bounded prompt and the
// sections are reassembled afterwards. Existing content is never dropped —
// the rewrite is rejected unless every original heading survives.
//
// It returns the full updated page content, or an error (usually the original
// overflow error) so the caller's terminal dead-letter path still applies.
func (s *wikiIngestService) rewritePageInSections(
	ctx context.Context,
	chatModel chat.Chat,
	slug, pageTitle, pageType, language string,
	existingContent, sharedContexts, newContent, deletedContent, availableSlugs string,
) (string, error) {
	preamble, sections := splitWikiPageSections(existingContent)
	if len(sections) < 2 {
		return "", fmt.Errorf("%w: sectioned rewrite needs >=2 headings, page has %d",
			ErrWikiContextOverflow, len(sections))
	}
	if len(sections) > wikiSectionRewriteMaxSections {
		return "", fmt.Errorf("%w: sectioned rewrite refused, page has %d sections (max %d)",
			ErrWikiContextOverflow, len(sections), wikiSectionRewriteMaxSections)
	}
	origHeadings := wikiPageHeadings(existingContent)
	hasAdditions := newContent != ""
	hasRetractions := deletedContent != ""

	for i := range sections {
		data := map[string]string{
			"PageSlug":             slug,
			"PageTitle":            pageTitle,
			"PageType":             pageType,
			"Language":             language,
			"ExistingSection":      sections[i].heading + "\n" + sections[i].body,
			"NewContent":           newContent,
			"DeletedContent":       deletedContent,
			"AvailableSlugs":       availableSlugs,
			"SharedSourceContexts": "",
			"HasAdditions":         boolStr(hasAdditions),
			"HasRetractions":       boolStr(hasRetractions),
		}
		// The shared source contexts are page-level framing, not per-section
		// evidence: include them only with the first section to keep every
		// section prompt small.
		if i == 0 {
			data["SharedSourceContexts"] = sharedContexts
		}

		out, err := s.generateWithTemplate(ctx, chatModel, agent.WikiPageSectionModifyUserPrompt, data)
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(out)
		if !strings.HasPrefix(trimmed, strings.TrimSpace(sections[i].heading)) {
			logger.Warnf(ctx, "wiki ingest: sectioned rewrite for slug %s dropped/changed heading %q; aborting",
				slug, sections[i].heading)
			return "", fmt.Errorf("%w: sectioned rewrite changed heading %q", ErrWikiContextOverflow, sections[i].heading)
		}
		sections[i].body = strings.TrimSpace(strings.TrimPrefix(trimmed, strings.TrimSpace(sections[i].heading)))
	}

	merged := mergeWikiPageSections(preamble, sections)
	if !wikiPageHeadingsPreserved(origHeadings, wikiPageHeadings(merged)) {
		return "", fmt.Errorf("%w: sectioned rewrite would drop an original heading", ErrWikiContextOverflow)
	}
	return merged, nil
}

// boolStr renders a boolean as the "1"/"" convention the wiki templates use.
func boolStr(v bool) string {
	if v {
		return "1"
	}
	return ""
}
