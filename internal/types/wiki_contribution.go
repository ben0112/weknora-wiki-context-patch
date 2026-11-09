package types

import "time"

// WikiPageContribution is one row in the `wiki_page_contributions` ledger: a
// verbatim source block one knowledge contributed to one wiki page (slug).
// Reduce persists rows after a successful page write. Re-ingesting the same
// knowledge with an unchanged block hash produces no delta, so the page
// rewrite is skipped; changed blocks supersede the previous row, and a source
// retraction marks the source's rows retracted.
//
// The ledger is the durable substrate for the stage-3 contribution model:
// pages are (eventually) a projection over active contributions, so updates
// never need to re-synthesize the whole page.
type WikiPageContribution struct {
	ID              int64     `json:"id"                gorm:"primaryKey;autoIncrement"`
	TenantID        uint64    `json:"tenant_id"         gorm:"index"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Slug            string    `json:"slug"`
	KnowledgeID     string    `json:"knowledge_id"`
	ChunkIDs        JSON      `json:"chunk_ids"`
	Content         string    `json:"content"`
	ContentHash     string    `json:"content_hash"`
	Status          string    `json:"status"` // active | superseded | retracted
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Wiki contribution status values.
const (
	WikiContributionActive     = "active"
	WikiContributionSuperseded = "superseded"
	WikiContributionRetracted  = "retracted"
)

// TableName binds WikiPageContribution to the `wiki_page_contributions` table.
func (WikiPageContribution) TableName() string {
	return "wiki_page_contributions"
}
