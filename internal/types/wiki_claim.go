package types

import "time"

// WikiPageClaim is one row of the `wiki_page_claims` table: an atomic,
// normalized claim extracted from a contribution block. Citations is the JSON
// list of {knowledge_id, chunk_id} pairs that support the claim, so the same
// fact contributed by several sources collapses into one active claim.
type WikiPageClaim struct {
	ID              int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID        uint64     `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string     `json:"knowledge_base_id"`
	Slug            string     `json:"slug"`
	ContributionID  int64      `json:"contribution_id"`
	KnowledgeID     string     `json:"knowledge_id"`
	ChunkID         string     `json:"chunk_id"`
	SectionKey      string     `json:"section_key"`
	ClaimKey        string     `json:"claim_key"`
	ClaimText       string     `json:"claim_text"`
	Citations       JSON       `json:"citations"`
	Status          string     `json:"status"`
	SupersedesID    int64      `json:"supersedes_id"`
	ValidFrom       *time.Time `json:"valid_from,omitempty"`
	ValidTo         *time.Time `json:"valid_to,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Wiki claim status values.
const (
	WikiClaimActive     = "active"
	WikiClaimSuperseded = "superseded"
	WikiClaimRetracted  = "retracted"
	WikiClaimConflict   = "conflict"
)

// TableName binds WikiPageClaim to the `wiki_page_claims` table.
func (WikiPageClaim) TableName() string {
	return "wiki_page_claims"
}

// WikiClaimCitation is one {knowledge_id, chunk_id} pair stored in the
// Citations JSON column.
type WikiClaimCitation struct {
	KnowledgeID string `json:"knowledge_id"`
	ChunkID     string `json:"chunk_id"`
}
