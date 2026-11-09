package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakeContribRepo is an in-memory stand-in for
// interfaces.WikiContributionRepository.
type fakeContribRepo struct {
	rows             []*types.WikiPageContribution
	replaceCalls     [][]*types.WikiPageContribution
	replaceKnowledge []string
	retractSlugs     [][]string
	retractKids      [][]string
}

func (f *fakeContribRepo) ListActiveByScope(_ context.Context, _, _ string) ([]*types.WikiPageContribution, error) {
	var out []*types.WikiPageContribution
	for _, r := range f.rows {
		if r.Status == types.WikiContributionActive {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeContribRepo) ReplaceActiveForKnowledge(_ context.Context, _, _, knowledgeID string, contributions []*types.WikiPageContribution) error {
	f.replaceKnowledge = append(f.replaceKnowledge, knowledgeID)
	f.replaceCalls = append(f.replaceCalls, contributions)
	for _, c := range contributions {
		f.rows = append(f.rows, c)
	}
	return nil
}

func (f *fakeContribRepo) RetractByKnowledge(_ context.Context, _ string, slugs []string, knowledgeIDs []string) error {
	f.retractSlugs = append(f.retractSlugs, slugs)
	f.retractKids = append(f.retractKids, knowledgeIDs)
	for _, r := range f.rows {
		for _, kid := range knowledgeIDs {
			if r.KnowledgeID == kid && r.Status == types.WikiContributionActive {
				r.Status = types.WikiContributionRetracted
			}
		}
	}
	return nil
}

func TestWikiAdditionBlockAndHash(t *testing.T) {
	add := SlugUpdate{
		DocTitle:     "doc",
		SourceChunks: []string{"c1"},
		Item:         extractedItem{Name: "n", Description: "d"},
	}
	chunks := map[string]string{"c1": "chunk text"}
	block, ids := wikiAdditionBlock(add, chunks)
	if block == "" || ids == nil {
		t.Fatal("empty block")
	}
	h1 := wikiAdditionHash(add, chunks)
	if h1 != wikiAdditionHash(add, chunks) {
		t.Fatal("hash not deterministic")
	}
	other := add
	other.Item = extractedItem{Name: "other", Description: "different"}
	if wikiAdditionHash(other, chunks) != h1 {
		t.Fatal("hash must ignore the LLM Name/Description paraphrase")
	}
	if wikiAdditionHash(add, map[string]string{"c1": "different text"}) == h1 {
		t.Fatal("hash must change when the cited evidence changes")
	}
}

func TestPersistContributions(t *testing.T) {
	fake := &fakeContribRepo{}
	s := &wikiIngestService{contribRepo: fake}
	add := SlugUpdate{
		DocTitle:     "doc",
		KnowledgeID:  "k1",
		SourceChunks: []string{"c1"},
		Item:         extractedItem{Name: "n", Description: "d"},
	}
	s.persistContributions(t.Context(), "kb", "slug", 10000,
		[]SlugUpdate{add}, map[string]string{"c1": "chunk text"})
	if len(fake.replaceCalls) != 1 || len(fake.replaceCalls[0]) != 1 {
		t.Fatalf("replaceCalls = %d", len(fake.replaceCalls))
	}
	row := fake.replaceCalls[0][0]
	if row.KnowledgeID != "k1" || row.Status != types.WikiContributionActive || row.ContentHash == "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestRetractContributions(t *testing.T) {
	fake := &fakeContribRepo{}
	s := &wikiIngestService{contribRepo: fake}
	s.retractContributions(t.Context(), "kb", "slug", []string{"k1", "k2"})
	if len(fake.retractKids) != 1 || len(fake.retractKids[0]) != 2 {
		t.Fatalf("retractKids = %v", fake.retractKids)
	}
}
