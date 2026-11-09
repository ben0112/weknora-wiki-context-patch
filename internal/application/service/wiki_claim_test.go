package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeWikiClaimKey(t *testing.T) {
	if normalizeWikiClaimKey("补贴比例为 50%！") != "补贴比例为50" {
		t.Fatalf("key = %q", normalizeWikiClaimKey("补贴比例为 50%！"))
	}
	if normalizeWikiClaimKey("ABC def-1") != "abcdef1" {
		t.Fatalf("key = %q", normalizeWikiClaimKey("ABC def-1"))
	}
}

func TestWikiClaimSimilarity(t *testing.T) {
	a := "对参展费用给予不超过50%的补贴"
	b := "对参展费用给予不超过50%的补贴。"
	c := "申请企业应于展会结束后30个工作日内提交申请材料"
	if wikiClaimSimilarity(a, b) < wikiClaimMergeSimilarity {
		t.Fatalf("similar claims scored below merge threshold: %f", wikiClaimSimilarity(a, b))
	}
	if wikiClaimSimilarity(a, c) >= wikiClaimMergeSimilarity {
		t.Fatalf("unrelated claims scored %f", wikiClaimSimilarity(a, c))
	}
}

func TestParseWikiClaimCandidates(t *testing.T) {
	got := parseWikiClaimCandidates(`[{"claim":"第一条事实","section":"## 第1节"},{"claim":"第二条事实","section":""}]`)
	if len(got) != 2 || got[0].Section != "## 第1节" {
		t.Fatalf("got %+v", got)
	}
	if got := parseWikiClaimCandidates("```json\n[{\"claim\":\"a\",\"section\":\"\"}]\n```"); len(got) != 1 {
		t.Fatalf("fenced parse failed: %+v", got)
	}
	if got := parseWikiClaimCandidates("not json at all"); got != nil {
		t.Fatalf("garbage should fall back to nil: %+v", got)
	}
	// Duplicate claims collapse.
	if got := parseWikiClaimCandidates(`[{"claim":"重复事实","section":""},{"claim":"重复事实","section":""}]`); len(got) != 1 {
		t.Fatalf("duplicates not collapsed: %+v", got)
	}
}

type fakeClaimRepo struct {
	active []*types.WikiPageClaim
}

func (f *fakeClaimRepo) ListActiveByScope(_ context.Context, _, _ string) ([]*types.WikiPageClaim, error) {
	return f.active, nil
}

func (f *fakeClaimRepo) StripKnowledgeCitations(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeClaimRepo) InsertClaims(_ context.Context, claims []*types.WikiPageClaim) error {
	f.active = append(f.active, claims...)
	return nil
}

func (f *fakeClaimRepo) AppendCitations(_ context.Context, claimID int64, citations []types.WikiClaimCitation) error {
	for _, c := range f.active {
		if c.ID == claimID {
			c.Citations = mustJSONCitations(citations)
		}
	}
	return nil
}

func (f *fakeClaimRepo) RetractByKnowledge(_ context.Context, _ string, _ []string, _ []string) error {
	return nil
}

func (f *fakeClaimRepo) CountByScope(_ context.Context, _, _ string) (int64, error) {
	return int64(len(f.active)), nil
}
