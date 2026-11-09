package service

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitMergeWikiPageSections(t *testing.T) {
	content := "preamble text\n" +
		"# 标题一\nbody one\n" +
		"## 标题二\nbody two\n" +
		"### 标题三\nbody three\n"
	preamble, sections := splitWikiPageSections(content)
	if !strings.HasPrefix(preamble, "preamble") {
		t.Fatalf("preamble = %q", preamble)
	}
	if len(sections) != 3 {
		t.Fatalf("sections = %d, want 3", len(sections))
	}
	if sections[0].heading != "# 标题一" || strings.TrimSpace(sections[0].body) != "body one" {
		t.Fatalf("section0 = %+v", sections[0])
	}
	if sections[2].heading != "### 标题三" || strings.TrimSpace(sections[2].body) != "body three" {
		t.Fatalf("section2 = %+v", sections[2])
	}
	merged := mergeWikiPageSections(preamble, sections)
	if strings.Count(merged, "# 标题一") != 1 || strings.Count(merged, "body two") != 1 {
		t.Fatalf("merge lost content:\n%s", merged)
	}
	if !wikiPageHeadingsPreserved(wikiPageHeadings(content), wikiPageHeadings(merged)) {
		t.Fatal("headings not preserved after round trip")
	}
}

func TestWikiPageHeadingsPreserved(t *testing.T) {
	orig := []string{"# A", "## B", "## B"}
	if !wikiPageHeadingsPreserved(orig, []string{"# A", "## B", "## B", "## C"}) {
		t.Fatal("duplicates + extra heading should pass")
	}
	if wikiPageHeadingsPreserved(orig, []string{"# A", "## B"}) {
		t.Fatal("missing duplicate heading should fail")
	}
	if wikiPageHeadingsPreserved(orig, []string{"# A", "## B", "## B2"}) {
		t.Fatal("changed heading should fail")
	}
}

func TestRewritePageInSectionsRefusesUnsectionablePage(t *testing.T) {
	s := &wikiIngestService{}
	_, err := s.rewritePageInSections(
		t.Context(), nil, "slug", "title", "concept", "zh-CN",
		"no headings here", "", "new", "", "")
	if err == nil {
		t.Fatal("expected error for page without sections")
	}
	if !errors.Is(err, ErrWikiContextOverflow) {
		t.Fatalf("error %v does not wrap ErrWikiContextOverflow", err)
	}
}

func TestBoolStr(t *testing.T) {
	if boolStr(true) != "1" || boolStr(false) != "" {
		t.Fatal("boolStr convention broken")
	}
}
