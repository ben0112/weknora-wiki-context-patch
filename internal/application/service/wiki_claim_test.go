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

func (f *fakeClaimRepo) ListConflictByScope(_ context.Context, _, _ string) ([]*types.WikiPageClaim, error) {
	var out []*types.WikiPageClaim
	for _, c := range f.active {
		if c.Status == types.WikiClaimConflict {
			out = append(out, c)
		}
	}
	return out, nil
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

func (f *fakeClaimRepo) SupersedeClaim(_ context.Context, claimID int64) error {
	for _, c := range f.active {
		if c.ID == claimID {
			c.Status = types.WikiClaimSuperseded
		}
	}
	return nil
}

func (f *fakeClaimRepo) MarkConflict(_ context.Context, claimID int64) error {
	for _, c := range f.active {
		if c.ID == claimID {
			c.Status = types.WikiClaimConflict
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

func TestWikiClaimMergeDecisionFor(t *testing.T) {
	existing := &types.WikiPageClaim{ID: 1, ClaimKey: normalizeWikiClaimKey("对参展费用给予不超过50%的补贴"), ClaimText: "对参展费用给予不超过50%的补贴"}
	// Same fact, same value, wording drift → cite.
	if got := wikiClaimMergeDecisionFor(existing, wikiClaimCandidate{Claim: "对参展费用，给予不超过50%的补贴。"}); got != wikiClaimMergeDecisionCite {
		t.Fatalf("wording drift = %d, want cite", got)
	}
	// Same fact, different value → supersede.
	if got := wikiClaimMergeDecisionFor(existing, wikiClaimCandidate{Claim: "对参展费用给予不超过60%的补贴"}); got != wikiClaimMergeDecisionSupersede {
		t.Fatalf("value change = %d, want supersede", got)
	}
	// Same subject, contradictory value, differently worded → conflict.
	if got := wikiClaimMergeDecisionFor(existing, wikiClaimCandidate{Claim: "参展费用补贴比例调整为60%"}); got != wikiClaimMergeDecisionConflict {
		t.Fatalf("contradiction = %d, want conflict", got)
	}
	// Unrelated → insert.
	if got := wikiClaimMergeDecisionFor(existing, wikiClaimCandidate{Claim: "申请企业应于展会结束后30个工作日内提交申请材料"}); got != wikiClaimMergeDecisionInsert {
		t.Fatalf("unrelated = %d, want insert", got)
	}
}

func TestWikiClaimNumbersAndTimeRange(t *testing.T) {
	if !wikiClaimNumbersDiffer("补贴50%", "补贴60%") {
		t.Fatal("different values must differ")
	}
	if wikiClaimNumbersDiffer("补贴50%", "补贴50%") {
		t.Fatal("equal values must not differ")
	}
	from, to := wikiClaimTimeRange("自2026年12月1日起补贴比例调整为60%")
	if from == nil || from.Format("2006-01-02") != "2026-12-01" || to != nil {
		t.Fatalf("time range = %v, %v", from, to)
	}
	from2, to2 := wikiClaimTimeRange("政策有效期2026-01-01至2026-12-31")
	if from2 == nil || to2 == nil || from2.Format("2006-01-02") != "2026-01-01" || to2.Format("2006-01-02") != "2026-12-31" {
		t.Fatalf("two-date range = %v, %v", from2, to2)
	}
}
