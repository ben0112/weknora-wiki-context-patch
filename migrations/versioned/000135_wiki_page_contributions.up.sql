-- wiki_page_contributions: durable per-source contribution ledger for wiki
-- pages. One row records the verbatim source block one knowledge contributed
-- to one page (slug). Reduce persists rows after a successful page write;
-- a re-ingest whose block hash is unchanged produces no delta and the page
-- rewrite is skipped. Content changes mark the previous row superseded and a
-- retraction marks the source's rows retracted.
CREATE TABLE IF NOT EXISTS wiki_page_contributions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(64) NOT NULL,
    slug TEXT NOT NULL,
    knowledge_id VARCHAR(64) NOT NULL,
    chunk_ids JSONB NOT NULL DEFAULT '[]',
    content TEXT NOT NULL DEFAULT '',
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_contrib_scope_hash
    ON wiki_page_contributions (knowledge_base_id, slug, knowledge_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_wiki_contrib_scope
    ON wiki_page_contributions (knowledge_base_id, slug);
CREATE INDEX IF NOT EXISTS idx_wiki_contrib_knowledge
    ON wiki_page_contributions (knowledge_id);
