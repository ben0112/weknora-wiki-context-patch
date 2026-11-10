package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestWikiStripHeadingLinks(t *testing.T) {
	in := "# 标题\n正文 [[slug|x]] 保持\n## [[slug2|链接标题]]\n内容\n"
	out := wikiStripHeadingLinks(in)
	if out == in {
		t.Fatal("no change")
	}
	if strings.Contains(out, "[[slug2") || strings.Contains(out, "## [[") {
		t.Fatalf("heading link not stripped:\n%s", out)
	}
	if !strings.Contains(out, "[[slug|x]]") {
		t.Fatalf("body link must survive:\n%s", out)
	}
	if !strings.Contains(out, "## 链接标题") {
		t.Fatalf("heading display text lost:\n%s", out)
	}
}

func TestWikiStripHeadingLinksKeepsPlainHeading(t *testing.T) {
	// Bare [[slug]] (no display text) keeps the slug text.
	in := "## [[concept/foo]]\n"
	out := wikiStripHeadingLinks(in)
	if out != "## concept/foo\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestExactIdentityTargetAliasMatch(t *testing.T) {
	pages := map[string]*types.WikiPageLite{
		"entity/guo-ji-zhan-hui": {
			Slug:     "entity/guo-ji-zhan-hui",
			Title:    "国际展会",
			PageType: types.WikiPageTypeEntity,
			Aliases:  types.StringArray{"国际展览会"},
		},
	}
	candidates := map[string]bool{"entity/guo-ji-zhan-hui": true}
	item := extractedItem{Name: "国际展览会", Slug: "entity/guo-ji-zhan-lan-hui"}
	if got := exactIdentityTarget(item, types.WikiPageTypeEntity, candidates, pages); got != "entity/guo-ji-zhan-hui" {
		t.Fatalf("alias drift did not resolve to the existing page: %q", got)
	}
	// Unrelated name must not match.
	item2 := extractedItem{Name: "参展补贴流程", Slug: "concept/can-zhan-bu-tie-liu-cheng"}
	if got := exactIdentityTarget(item2, types.WikiPageTypeConcept, map[string]bool{"entity/guo-ji-zhan-hui": true}, pages); got != "" {
		t.Fatalf("unrelated name matched: %q", got)
	}
}
