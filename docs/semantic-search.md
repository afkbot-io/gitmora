# Local semantic search

Gitmora can combine PostgreSQL lexical retrieval with local embeddings and pgvector
cosine distance. Web, `gitmora search`, raw API and MCP use the same endpoint and
access rules. There is no server chat, generated summary or autonomous review.
The embedding service receives sanitized retained chunks and queries, never provider
credentials. Runtime model inference stays on the private Docker network.

## Install or upgrade

The bundled database image is `gitmora-postgres:18.6-vector1`, containing pgvector
0.8.7. New installations enable the extension automatically. For an existing
installation, back up the database, build/select the new database image and enable
the extension as the database administrator **before migration 28**:

```sh
make build-postgres-image
# Set GITMORA_POSTGRES_IMAGE=gitmora-postgres:18.6-vector1 in compose.env.
# Recreate only your installation's PostgreSQL service with all its selected overlays first.
# Substitute its exact container name; keep its existing data volume attached.
docker exec YOUR_GITMORA_POSTGRES_CONTAINER psql -X -v ON_ERROR_STOP=1 \
  -U postgres -d workgraph -c 'CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public'
```

Use the ordinary [deployment upgrade procedure](deployment.md) to migrate and
recreate the API/worker. External PostgreSQL installations also need pgvector
installed by their administrator. The application role remains a non-superuser.
Vectors are derived data; source capture bytes and evidence identities do not change.
For a non-superuser logical restore into a database with the administrator-owned
extension already installed, pass `pg_restore --no-owner --no-acl --no-comments`.
The application role must not replace the extension's ownership or comment metadata.
Exact duplicate chunks reuse a completed vector only within the same project/profile;
each citation keeps its own original source and producer metadata. This reduces
inference work across repeated immutable revisions without merging their history.

## Provision a CPU model

The optional overlay uses Ollama 0.40.14 and can run `embeddinggemma:300m` on CPU.
Review the model's own license before redistributing its weights. Its output is
768-dimensional; the prompt prefixes below follow its model card. Model size is about 622 MB on disk. The overlay runs two isolated model services, each bounded to 2 GB RAM and two CPU cores, sharing the same read-only weights. Allow up to 4 GB RAM plus PostgreSQL space for vectors. Interactive requests use `embedding-queries`; background batches use `embeddings`. Separate queues prevent long backfill inputs from delaying short search queries. Inference concurrency settings alone do not guarantee this isolation. This is a starting profile, not a capacity guarantee for concurrent teams.

Set `GITMORA_STATE` to your private installation directory. Provisioning temporarily
uses an ordinary Docker network to download weights. It receives no source data:

```sh
GITMORA_STATE=/srv/gitmora/state
mkdir -p "$GITMORA_STATE/embedding-models"
chmod 700 "$GITMORA_STATE/embedding-models"
docker run -d --name gitmora-embedding-setup \
  -p 127.0.0.1:11436:11434 \
  -v "$GITMORA_STATE/embedding-models:/root/.ollama" \
  ollama/ollama:0.40.14 serve
docker exec gitmora-embedding-setup ollama pull embeddinggemma:300m
curl -fsS http://127.0.0.1:11436/api/tags
# Copy the complete 64-character sha256 digest of that model, not the short list ID.
docker stop gitmora-embedding-setup
docker rm gitmora-embedding-setup
```

Add the following to the private `runtime.env`, replacing the digest:

```dotenv
WORKGRAPH_EMBEDDING_URL=http://embeddings:11434
WORKGRAPH_EMBEDDING_MODEL=embeddinggemma:300m
WORKGRAPH_EMBEDDING_REVISION=FULL_64_CHARACTER_MODEL_DIGEST
WORKGRAPH_EMBEDDING_DIMENSIONS=768
WORKGRAPH_EMBEDDING_QUERY_PREFIX="task: search result | query: "
WORKGRAPH_EMBEDDING_PASSAGE_PREFIX="title: none | text: "
```

Add `deploy/compose.embeddings.yaml` to the installation's existing overlay list
using `deployment init --overlay` on a new installation, or the following atomic edit on an existing installation:

```sh
bin/gitmorad deployment configure --directory /srv/gitmora/state \
  --add-overlay "$PWD/deploy/compose.embeddings.yaml"
```

The edit preserves installation ID, secrets, port and existing ordered overlays. Keep all previously selected
identity/provider overlays. Start with `deployment up`. The model service publishes
no host port; the overlay sets `WORKGRAPH_EMBEDDING_QUERY_URL=http://embedding-queries:11434`
only on the API. For a custom deployment, set this optional variable to an isolated
replica serving the identical model/digest. It changes routing without invalidating
existing vectors. Both endpoints validate the pinned digest, including cached queries.
Initial model loading can cause a bounded first-request fallback; repeat after the
model has loaded. A separate `embedding-worker` backfills two chunks per batch and
continues after restarts. Ordinary capture, provider synchronization and lexical
indexing use their existing worker and do not wait for model inference.

Keep the configured model immutable. Changing the model, digest, endpoint or vector
dimension selects another derived profile and backfills it. Never replace weights
under an unchanged model/digest. Old profiles do not participate in new searches;
remove obsolete profiles only after verifying the new one. Do not delete original
captures. To disable, first run `deployment down` with the existing overlay list (volumes are preserved),
remove all embedding settings, then run
`deployment configure --directory /srv/gitmora/state --remove-overlay /absolute/path/to/deploy/compose.embeddings.yaml`
and `deployment up`. Stopping with the old list prevents orphaned inference workers.
Ordinary lexical retrieval continues to work; cached model files are retained.

## Search contract

`POST /v1/context/search` and MCP `context.search` accept `mode`:

| Mode | Behavior |
| --- | --- |
| `auto` or omitted | Hybrid when configured; lexical otherwise. First-page model outages fall back to lexical and set `semantic_unavailable`. |
| `lexical` | Existing word/identifier search without a model call. |
| `hybrid` | Lexical plus semantic, with the same first-page fallback. |
| `semantic` | Semantic only; unavailable inference returns 503 instead of pretending a lexical search was semantic. |

SHA, unique commit prefixes, session UUID and metadata-only lookups retain exact
behavior. Text can be narrowed by repository, session, commit range, path, agent,
model and role. A semantic match never broadens an MR's recorded message interval.
Explicit source expansion still rechecks membership and current provider access.

Candidate selection excludes chunks without either a lexical match or a completed vector for the active profile, so partial backfill does not require ranking unembedded history. Authorization and source-availability checks still run before ranking or limits.

Each semantic lane uses exact cosine retrieval over its authorized source scope.
It deliberately avoids approximate top-k followed by permission filtering, which
can hide valid matches behind inaccessible projects. Lexical and semantic lanes
contribute at most 200 unique source parts each, combined with reciprocal rank
fusion. `candidate_window` reports this per-lane bound; lexical mode remains
available to traverse a larger exact-word result set. No ANN scale/latency claim
is made for this first implementation. Benchmark larger corpora before choosing
profile-specific HNSW indexes or a separate engine such as OpenSearch.

`search_mode` reports the actual engine. `semantic_pending` and `semantic_failed`
report missing/retrying derived chunks in the authorized repository/project scope,
not counts of relevant missing answers. While either is nonzero, semantic coverage
is incomplete. Similarity is a ranking signal, not proof that a source answers the
question. The initial threshold is cosine 0.35; evaluate it with your corpus.

Query vectors use a bounded in-memory cache (256 entries, five-minute expiry); no query text is logged or stored in that cache. Model digest is checked before cached vectors are reused.

Continuation pins the query, model profile, query-vector digest and index cutoff.
Late vectors do not enter an existing page sequence. A bounded, user/query-bound snapshot stores only candidate IDs and scores for
15 minutes (at most 16 per user, with opportunistic expiry cleanup). Later pages
reauthorize these candidates and do not invoke the model. Even an embedding write
that began before the first search and committed afterward cannot reorder them.
Expired/evicted snapshots require a new search; pages never silently switch engines. Source permission/latest-revision changes are still checked live.

```sh
gitmora search 'почему изменили обновление доступа' \
  --server https://gitmora.example.com --project PROJECT_UUID --repository REPO_UUID
gitmora search 'refreshToken' --mode lexical \
  --server https://gitmora.example.com --project PROJECT_UUID
```

Output is JSON with exact citations and `next`. Save the `next` object as
`cursor.json` and repeat the same query with `--after cursor.json` to continue.
Existing `gitmora api` and MCP clients work against custom Gitmora server URLs.

## Evaluate your use cases

Run the integration tests for multi-commit chats, model filters, exact expansion,
pagination, revocation, fallback, stale leases and projection replacement. Those
use deterministic vectors to verify contracts, not model quality. For real-model
quality, run `scripts/evaluate-search.py` with a labeled JSON fixture and a scoped
short-lived read token. Include paraphrases, Russian/English, exact identifiers,
irrelevant questions, incident SHAs and several changes within one dialogue.
Compare lexical/hybrid Recall@k, MRR and p50/p95 latency; inspect the cited originals.
Never publish source transcripts or credentials as evaluation artifacts.
