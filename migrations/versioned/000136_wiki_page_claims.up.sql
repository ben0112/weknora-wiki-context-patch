-- wiki_page_claims: atomic, normalized claims extracted from the contribution
-- ledger rows. One claim carries the citations (knowledge/chunk pairs) that
-- support it, so the same fact contributed by several sources collapses into
-- ONE active claim with many citations. Status lifecycle:
--   active -> superseded (replaced by a newer claim, supersedes_id links)
--   active -> retracted (source removed)
--   active/conflict marks claims that contradict another active claim.
CREATE TABLE IF NOT EXISTS wiki_page_claims (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(64) NOT NULL,
    slug TEXT NOT NULL,
    contribution_id BIGINT NOT NULL DEFAULT 0,
    knowledge_id VARCHAR(64) NOT NULL,
    chunk_id VARCHAR(64) NOT NULL DEFAULT '',
    section_key TEXT NOT NULL DEFAULT '',
    claim_key TEXT NOT NULL,
    claim_text TEXT NOT NULL,
    citations JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    supersedes_id BIGINT NOT NULL DEFAULT 0,
    valid_from TIMESTAMPTZ,
    valid_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one ACTIVE claim per (kb, slug, claim_key): duplicates merge into
-- the active row's citations instead of inserting a second row.
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_claims_active_key
    ON wiki_page_claims (knowledge_base_id, slug, claim_key) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_wiki_claims_scope
    ON wiki_page_claims (knowledge_base_id, slug, status);
CREATE INDEX IF NOT EXISTS idx_wiki_claims_knowledge
    ON wiki_page_claims (knowledge_id);
