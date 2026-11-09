# Supporting research: CAO campaign knowledge

This is the evidence appendix to the proposed
[campaign knowledge service ADR](../campaign-knowledge-service.md), not a
normative specification or implementation. Provider claims, schemas, API
examples, and capacity scenarios below retain their stated evidence limits.
Repository paths in citations are relative to the repository root; linked
source revisions remain pinned where available.

**Snapshot:** November 8-9, 2026. **Compact edition:** November 9, 2026.

**Decision:** Provide a generic, scoped campaign knowledge service: versioned structured records, evidence, typed graph links, and optional campaign-owned embedding spaces. Backing storage is interchangeable only where required semantics hold. Routine maintenance is lazy; authorization, revocation, and required erasure are not.

| Question | Answer |
|---|---|
| What campaigns store | Schema-versioned data, findings, facts, tested lessons, and relationships |
| Where it lives | A conforming SQL, key/value, document, object/manifest, log/snapshot, graph, Git, or composite adapter |
| What becomes active | Only the demanded campaign/family, repository/shared namespace, experiment arm, and embedding space |
| How proprietary algorithms fit | Reviewed campaign workers compute representations; isolated immutable space identities preserve compatibility |
| What queries mean | Exact extraction, graph traversal, semantic recall, and generated answers are separate operations |
| What memory cannot do | Grant authority, silently repair missing evidence, or turn a relevance/confidence score into truth |

Expand the evidence you need; the core design starts open. All **120 references** are retained with direct source links. Reported results are not production guarantees. A broader RAG technique and offering survey is outside this document.

| Reading path | Purpose |
|---|---|
| [Background](#part-i-background-and-existing-knowledge) | Existing systems, evidence, and consequential limitations |
| [References](#part-ii-references) | Complete source inventory, pinned versions, and access caveats |
| [Implementation](#part-iii-suggested-implementation) | Campaign contracts, lazy execution, embeddings, APIs, backing choices, and validation |

## Part I: Background and existing knowledge

<details>
<summary><strong>1. Foundations and research: what is worth borrowing</strong></summary>

**Four distinct layers:** source evidence records what was observed; a knowledge record states an interpretation; an index/profile/summary helps navigate it; a context packet selects what a task may consume. A transcript is not curated knowledge, a summary is not verified truth, and retrieval is not instruction authority.

Keep **type** (fact/preference/episode/procedure/summary), **scope** (who owns/may use it), **assertion** (explicit/observed/inferred), and **lifecycle state** independent. Later effective time and later ingestion time are not interchangeable. Semantic memory means factual knowledge; semantic search means relevance retrieval.

| Research family | Mechanism to reuse | Limit |
|---|---|---|
| Truth maintenance, belief revision, provenance | Justifications, corrections versus withdrawal, versioned derivation and responsible writers [1](https://www.sciencedirect.com/science/article/pii/0004370279900080), [2](https://doi.org/10.2307/2274239), [3](https://www.w3.org/TR/prov-dm/) | No need for logical closure, a theorem prover, or full RDF implementation; Doyle full text and AGM full article were not read |
| Generative Agents, MemoryBank, MemGPT | Separate working/durable tiers; selective retrieval and reinforcement [4](https://arxiv.org/abs/2304.03442v2), [5](https://arxiv.org/abs/2305.10250v3), [12](https://arxiv.org/abs/2310.08560v2) | Human-inspired decay/context paging is not factual verification or erasure; simulation results do not establish fleet behavior |
| Reflexion, Voyager | Observable feedback and versioned tested procedures [6](https://arxiv.org/abs/2303.11366v4), [9](https://arxiv.org/abs/2305.16291v2) | A failed episode can justify a narrow caution, not a universal recipe or new execution permissions |
| CoALA and memory surveys | Separate writing, management, reading, and action responsibilities [10](https://arxiv.org/abs/2309.02427v3), [13](https://arxiv.org/abs/2404.13501v1), [15](https://arxiv.org/abs/2602.06052v4) | Taxonomies guide design; they do not establish an optimal backend or policy |
| A-MEM, Zep, Mem0 | Atomic records, bounded neighbors, temporal versions, small operation vocabulary [7](https://arxiv.org/abs/2502.12110v11), [8](https://arxiv.org/abs/2504.19413v1), [21](https://arxiv.org/abs/2501.13956v1) | Similarity links need not be evidence; current Mem0 behavior differs from the paper |
| Sleep-time compute, MemoryOS, MemOS | Amortized preparation, hot/cold tiers, inspectable resource metadata [14](https://arxiv.org/abs/2504.13171v1), [23](https://arxiv.org/abs/2506.06326v1), [27](https://github.com/MemTensor/MemOS), [38](https://github.com/BAI-LAB/MemoryOS), [39](https://arxiv.org/abs/2507.03724v4) | Prepare demanded stable scopes, not the cold fleet; research frameworks and shipped plugins differ |
| Hindsight, Mi-Memory | Separate evidence/beliefs; lifecycle diagnostics and bounded promotion [11](https://arxiv.org/abs/2512.12818v1), [18](https://arxiv.org/abs/2607.18975v1) | Model judgments and preliminary design components are not production guarantees |

“Evolution” can mean representation edits, belief correction, consolidation, procedure learning, policy learning, or changing model weights. Start with versioned external records; defer personalized weight/activation memory because selective inspection, correction, and erasure are harder.

</details>

<details>
<summary><strong>2. Industry contracts and first-party engineering lessons</strong></summary>

| Surface | Useful contract | Consequential distinction |
|---|---|---|
| Letta | Persisted agents and editable/shared core blocks [25](https://docs.letta.com/v1-sdk/concepts/stateful-agents) | Shared edits need conflict controls; context eviction is not storage deletion |
| LangGraph / LangMem | Thread checkpoints; namespaced cross-thread records; extraction/consolidation over an owned store [19](https://docs.langchain.com/oss/python/concepts/memory), [20](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/) | Namespace conventions do not implement authorization or complete provenance/erasure |
| Mem0 | Managed/OSS add, update, delete, scoped search [22](https://docs.mem0.ai/core-concepts/memory-operations/add), [24](https://docs.mem0.ai/core-concepts/memory-operations/update), [26](https://docs.mem0.ai/core-concepts/memory-operations/delete) | Current add is additive; expiry can hide search/get-all without preventing ID fetch |
| Graphiti / Zep | Incremental temporal entities/facts/episodes and hybrid graph retrieval [30](https://github.com/getzep/graphiti) | Historical invalidation preserves data; framework and managed service are different products |
| Hindsight | Banks, retain/recall/reflect, observations and derived pages [11](https://arxiv.org/abs/2512.12818v1), [29](https://github.com/vectorize-io/hindsight), [31](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models) | Consolidation and confidence are model-mediated, not hard truth guarantees |
| Google Memory Bank | Managed scoped facts, revisions, TTL, CRUD/rollback [35](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank), [36](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank/revisions) | Parent deletion can leave revisions accessible for up to 48 hours |
| AWS AgentCore Memory | Actor/session events and namespace/strategy extraction [40](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory.html), [41](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-strategies.html), [42](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-organization.html) | No long-term extraction without strategies; cross-strategy correction and cascading erasure require separate evidence |
| OpenAI Agents SDK sessions | Persistent conversation state and history controls [43](https://openai.github.io/openai-agents-python/sessions/) | Session persistence is not a cross-session fact/lineage service |
| Cognee, Honcho, MemOS ecosystem | Connected knowledge, peer reasoning, or resource/plugin composition [44](https://github.com/topoteretes/cognee), [45](https://github.com/plastic-labs/honcho), [27](https://github.com/MemTensor/MemOS) | Distinguish explicit facts from inferred profiles and documented architecture from exact deployment guarantees |

**Verified version drift:** the Mem0 2025 paper describes ADD/UPDATE/DELETE/NOOP reconciliation; the pinned OSS implementation instead uses additive extraction. Explicit update/delete remain separate. Do not select a release by assuming the paper's destructive conflict repair. [8](https://arxiv.org/abs/2504.19413v1), [22](https://docs.mem0.ai/core-concepts/memory-operations/add), [46](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/memory/main.py#L925-L963)

**GitHub:** compact citation-backed repository facts and just-in-time verification are strong prior art. Current docs distinguish repository facts from user preferences, prohibit pooling facts across repositories/forks, and describe default 90-day expiry with repository configuration of 7-180 days. Upvotes reset expiry; ordinary retrieval does not; disabling/downvoting is not erasure. Citation checks are model judgments. Blog comparisons of 90% versus 83% merge rates and 77% versus 75% positive feedback mean **7 and 2 percentage points**, not universal causal gains. [57](https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/), [58](https://github.blog/changelog/2026-05-26-copilot-memory-has-more-controls-for-deletion-scope-and-the-copilot-cli/), [59](https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory)

**Anthropic/Claude:** online salience and offline consolidation serve different purposes. Keep tested lessons and exact critical source terms; avoid repeating authoritative mutable state. Reported shared-table lost writes reinforce record-local CAS/ownership, not a global fleet summary. [60](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents), [62](https://www.anthropic.com/research/effective-harnesses-for-long-running-agents), [75](https://www.anthropic.com/research/components-of-agent-memory)

Claude app memory, Claude Code auto memory, authored instructions, and the API memory tool are separate surfaces. Code notes are machine-local and shared across same-repository worktrees; the auto-loaded index is bounded to 200 lines/25 KB, with topic files on demand. Transcript cleanup does not remove memory notes. The API memory tool is caller-owned virtual file operations that can map to a database; the handler still enforces scope/path/lifecycle. Isolated worktrees therefore need not isolate experimental memories. [61](https://claude.com/resources/articles/context-management), [63](https://claude.com/resources/articles/memory), [64](https://code.claude.com/docs/en/memory), [65](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool), [74](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools)

**OpenAI/Codex:** persistent local memory exists; it is distinct from ChatGPT memory and compaction. Current docs describe opt-in generation/use, asynchronous eligibility, reset, local-home storage, and v1/v2 migration. The pinned May 26 pipeline has bounded leased extraction, explicit successful no-output, failure backoff, serialized local consolidation, and a **diff-based dirty gate** rather than a timestamp watermark. It does not establish the latest v2 implementation or derivative erasure. [66](https://openai.com/index/unrolling-the-codex-agent-loop/), [67](https://developers.openai.com/blog/run-long-horizon-tasks-with-codex), [68](https://openai.com/index/codex-for-almost-everything/), [69](https://learn.chatgpt.com/docs/customization/memories?surface=app), [70](https://github.com/openai/codex/blob/d579dafb706e20a5aa47ac21d06ba01009f9625c/codex-rs/memories/README.md)

ChatGPT dreaming compute claims are not Codex/fleet cost multipliers. Model-sensitive skill/prompt pruning and outcome/process evals support versioned reader/writer releases. Early 10-20-prompt regressions do not establish statistical power for fleet promotion. [71](https://openai.com/index/chatgpt-memory-dreaming/), [72](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra), [73](https://developers.openai.com/blog/eval-skills)

**Synthesis:** small evidence-backed records, progressive disclosure, validation on use, bounded changed-input consolidation. Files/indexes/wiki/profile are navigation or working artifacts—not authority or proof.

</details>

<details>
<summary><strong>3. GitHub projects: the eight-repository implementation cross-check</strong></summary>

Pinned documentation was read for eight projects; selected Hippo, DiffMem, and Basic Memory implementation paths and one Hippo evaluation were inspected. No project runtime, tests, or models were run. Stars/activity aided discovery, not quality scoring.

| Project | Actual substrate | Fit and caveat |
|---|---|---|
| Coding-agent Hippo (`kitfunso/hippo-memory`) | Node CLI/MCP/HTTP; canonical SQLite and Markdown mirrors; default no-model BM25 [97](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/README.md) | Good capture/feedback/invalidation primitives; mirrors are not a Git-primary contract |
| Python Hippo (`amoghrman/hippo`) | PostgreSQL/pgvector plus generative conflict judge [101](https://github.com/amoghrman/hippo/blob/1a72789774eb31d9a02400f993a85d59c4b6ddb5/README.md) | Supersede/merge/coexist and conflict log; forget is soft deletion; no provider credentials disables conflict detection with a warning |
| HippoRAG 2 | Document/entity/fact graph, vectors, model-dependent extraction; MCP retrieve/index/delete/QA [100](https://github.com/OSU-NLP-Group/HippoRAG/blob/d5c8329422e0a0b834a15874545cb6a74b4f9b26/README.md) | Optional multi-hop retrieval, not full knowledge governance; incompatible model/index identities are rejected |
| DiffMem | Markdown current state, Git history, orphan branches/worktrees; local primary, optional GitHub backup [102](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/README.md) | Strong Git prior art; branch-per-user is not 100k-scope isolation; README MIT claim lacked a standard license-file confirmation in this check |
| Basic Memory | Canonical Markdown entities/observations/relations; derived local database/indexes [103](https://github.com/basicmachines-co/basic-memory/blob/b7d4006bd581c80c2bf4e4ca9a95f745ccee83a5/README.md) | Text-first records and coalescing; AGPL-3.0, optional reranking and documented update/telemetry defaults |
| MCP Memory Service | SQLite/ONNX; optional capture, sync, beliefs, summaries, quality and consolidation [104](https://github.com/doobidoo/mcp-memory-service/blob/238e609e7804874a772d6711b8acbd06526c7c55/README.md) | Rich local comparison, not minimal by default; tags/bootstrap do not establish enforced repository/arm isolation |
| OpenViking | Virtual context filesystem; directory search and generated L0/L1/L2 layers [105](https://github.com/volcengine/OpenViking/blob/c361405712d052cfd80440373c6e092aadf0714c/README.md) | Useful progressive disclosure, not Git storage; model/server work and broad platform; component-specific licenses, main AGPLv3 |
| Mnemoria | Rust append-only binary log, advisory lock, BM25/CPU embeddings [106](https://github.com/one-bit/mnemoria/blob/7776a85a4e0437d2b0d5bbee16063430204b26dc/README.md) | Local storage comparison; index rebuilds on each open and model downloads on first use; binary Git compatibility is not merge/erasure parity |

**Hippo's non-lazy defaults:** initialization installs hooks/history import and a daily 6:15am runner over registered workspaces. Inspected auto-sleep gates on enabled/count threshold and a per-store latch, not durable scope activation/fencing. Opt-outs exist, but sleep can automatically send memory text to Anthropic when an API key is set. Literal retrieval strengthening and automatic global promotion conflict with our no-write-per-read and permission-aware sharing goals. Dormant exclusion is not purge of mirrors/backups/derivatives. [97](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/README.md), [98](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/src/api/sleep/auto-sleep.ts)

Its sixteen-repository evaluation reports cost ratio **0.999 [0.962, 1.037]**, roughly 12% lower teach/correction-turn spend, and **inconclusive** repeat-error reduction: -8.9 points [-18.5, +0.7], below a preregistered 15-point minimum. The tested arm predates later releases and includes an unreleased capture change; runners/records were local, limiting public reproduction. The README also reports decay tying with disabled decay and sleep lowering retrieval. Learned-value rescue is experimental/default-off and cannot choose additional deletions. [97](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/README.md), [99](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/docs/evals/2026-11-01-z0-round2-result.md)

**DiffMem distinction:** current docs/code deliberately make local Git authoritative and remote failure nonfatal; GitHub is backup, not the database. Demand-mounted worktrees are useful, but stale-local fallback, all-branch cold restore, scheduled pushes, and shared repository access cannot be copied into a mode promising Git-primary freshness. “Production” and small-VPS claims are not fleet benchmarks. [102](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/README.md)

**Basic Memory detail:** per-project process-local single flight plus a dirty bit produces one trailing rerun; it is not durable delivery/distributed fencing. Optional reranker failures do not silently revert to original ordering. Disable unintended automatic updates/background network work and check integration licenses. [103](https://github.com/basicmachines-co/basic-memory/blob/b7d4006bd581c80c2bf4e4ca9a95f745ccee83a5/README.md)

HippoRAG's Claude plugin shares an index across projects and can install PyTorch on first launch; its MCP HTTP exposure needs the documented authentication wrapper/read-only configuration. Those defaults are not repository/arm isolation or minimal startup. [100](https://github.com/OSU-NLP-Group/HippoRAG/blob/d5c8329422e0a0b834a15874545cb6a74b4f9b26/README.md)

**Shortlist by role:** Basic Memory/DiffMem for text/versioned-storage patterns; Hippo for feedback and candid negative results; HippoRAG for optional graph retrieval. None demonstrates a turnkey active-only, policy-preserving 100,000-repository service.

</details>

<details>
<summary><strong>4. Admission models, quality maintenance, and sanitization</strong></summary>

| Mechanism | Evidence | Proper role |
|---|---|---|
| Hindsight 0.8B discriminative decision model | Typed choice/score/yes-no, one forward pass, no prose; documented GPU serving [31](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models), [76](https://hindsight.vectorize.io/developer/decision-models), [77](https://huggingface.co/hindsight-ai/hs-decide-1-flash) | Relevance, restatement/change triage, evidence sufficiency, page impact—not raw retain or sanitization |
| SLICE-Mem salience gate | ModernBERT-base/512-token input before parsing/embedding; teacher/annotation supervision; separate offline relation classifier [84](https://arxiv.org/html/2610.33015v1) | Cheap pre-extraction gate; user-turn dialogue training does not establish repository transfer or factual/privacy correctness |
| A-MAC hybrid score | Utility LLM plus overlap/novelty/recency/type features [83](https://arxiv.org/html/2603.04549v1), [89](https://github.com/GuilinDev/Adaptive_Memory_Admission_Control_LLM_Agents) | Interpretable utility baseline; ROUGE-L overlap is not truth probability |
| Memory-R1, Mem-alpha, AgeMem | Generative RL operation policies; downstream QA, format/compression/validity rewards [78](https://arxiv.org/html/2508.19828v5), [79](https://arxiv.org/html/2509.25911v1), [28](https://arxiv.org/abs/2601.01885v3) | Learn bounded proposals after instrumentation; learned DELETE is not authorized purge |
| MemRL | Frozen LLM, outcome-updated episodic utility, two-stage retrieval [80](https://arxiv.org/html/2601.03192v2) | Feedback-aware reuse; not write admission or sensitive-content screening |
| MemGuard | Completion/evidence/execution/generalizability verification and persistent lifecycle descriptors [81](https://arxiv.org/html/2608.21867v1), [88](https://github.com/whyyyyy123/MemGuard) | Preserve verifier/outcome metadata beyond creation; public core lacks bundled end-to-end benchmark/runtime components |
| MemLineage | Signed ancestry, conditional trust propagation, sensitive-action gate [82](https://arxiv.org/html/2605.14421v1) | Prevent memory from laundering authority; signatures prove origin/integrity, not truth |

**Quantitative limits:** Hindsight reports median/p90 decision calls of 167/350 ms, model-call—not end-to-end—time, judge dependence and equal operation weighting. Aggregate quality improves for six of seven model combinations and cost for five; reflect can worsen and cheap LLM combinations can cost more. Evaluated retain is unaffected; consolidation-call reductions differ between docs (~40%) and blog (~20%). Best-candidate survival is not abstention. Optional quality fallback must still pass mandatory controls. [31](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models), [76](https://hindsight.vectorize.io/developer/decision-models)

A-MAC's reported admission F1 **0.583**, precision **0.417**, recall **0.972** over 225 test candidates and 2,644 versus 3,831 ms are not a high-precision or non-generative gate. A weighted utility score must not compensate for prohibited content or unsupported authority. [83](https://arxiv.org/html/2603.04549v1), [89](https://github.com/GuilinDev/Adaptive_Memory_Admission_Control_LLM_Agents)

SLICE-Mem's online 30B comparison reports Memora FAMA **46.60 vs 46.68**, LongMemEval-S accuracy **56.8% vs 59.3%**, and 92.4%/99.5% fewer LLM tokens. Gate/parser/embedding compute remains; stage timings are not uniformly end-to-end, preparation costs are extra, threshold tuning used a reporting benchmark, and consolidated LongMemEval-S accuracy falls to **56.0%**. A 150-turn single-annotator stratified audit is not production calibration. [84](https://arxiv.org/html/2610.33015v1)

MemLineage's zero-ASR finite harness and six banking pairs are conditional evidence. Parent omission, diluted edges and longer laundering chains can fail open; trusted inference is assumed, multi-host replication untested, attention attribution deferred, and stricter defaults trade utility for protection. Record permissible input identifiers/versions at the service boundary; require independent current authority for actions. [82](https://arxiv.org/html/2605.14421v1)

**Mandatory controls are separate from soft quality:**

| Risk | Mechanism and boundary |
|---|---|
| Secrets | Central scanner/allowed-field policy; Gitleaks output redaction is not input sanitization. Repo-controlled allowlists/config cannot weaken admission; scanning is incomplete [86](https://github.com/gitleaks/gitleaks) |
| PII/sensitive content | Source minimization, configured Presidio analyzer plus validated transformation/rejection; current community docs warn detection is incomplete [85](https://presidio.dataprivacystack.org/) |
| Injection | Control-token checks and optional Prompt Guard 2; 22M/86M binary models target instruction overrides, use 512-token windows and remain vulnerable to adaptive/domain attacks—not secret/PII scrubbers [59](https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory), [87](https://github.com/meta-llama/PurpleLlama/blob/main/Llama-Prompt-Guard-2/86M/MODEL_CARD.md) |
| False/unsupported claims | Versioned evidence, exact anchors plus independent checks; quotation proves presence, not truth |
| Scope/authority | Trusted policy/revocation checks; no model score overrides denial |

Inspect inputs before model submission, then transformed outputs before persistence/indexing. Apply controls to vectors, evidence excerpts, logs, rejected/dead-letter payloads and caches; never retain rejected secrets “for audit.” Redaction that changes attribution/negation/meaning requires rejection. Missing detector coverage is incomplete, not clean. Preserve distinct verification outcome, uncertainty and timestamp; no default model-generated confidence.

</details>

<details>
<summary><strong>5. Query languages and API semantics</strong></summary>

| Surface | Good fit | Required distinction |
|---|---|---|
| SQL | Exact structured selection, joins, aggregation, ordering, recursion [109](https://www.postgresql.org/docs/current/sql-select.html) | Dialects/extensions differ; `LIMIT` does not bound work, and `SELECT` can contain data-modifying CTEs |
| ISO GQL / Cypher | Property-graph patterns and projections [111](https://www.iso.org/standard/76120.html), [112](https://neo4j.com/docs/cypher-manual/current/introduction/) | GQL is not GraphQL; ISO catalog/abstract only was read; Cypher 25/version parity must be pinned |
| SPARQL 1.1 | RDF patterns, named graphs, paths, aggregates and result forms [110](https://www.w3.org/TR/sparql11-query/) | RDF/property-graph translations are not automatically lossless; path/federation costs and access need bounds |
| Gremlin | Composable graph traversal [113](https://tinkerpop.apache.org/docs/3.8.2/tutorials/getting-started/) | Pin provider/dialect; do not expose unrestricted host-language scripts |
| GraphQL | Typed API field selection, arguments and nested retrieval [114](https://graphql.org/learn/queries/) | Resolvers still implement storage, scope, pagination, batching and cost limits |
| Closed JSON/named-query API | Narrow portable operations | Define types/nulls/order/limits/errors; JSON alone is not semantics |

**Exact read/export, graph traversal, relevance recall and synthesized answers are different contracts.** Top-k recall is not exhaustive extraction; generated prose is not a lossless export.

| API | Consequential behavior |
|---|---|
| LangGraph Store [119](https://docs.langchain.com/oss/python/langgraph/stores.md) | Namespace-prefix enumeration can include descendants; limit truncation has no overflow signal; order differs by backend; offsets do not guarantee a consistent multi-page snapshot |
| Mem0 search [117](https://docs.mem0.ai/core-concepts/memory-operations/search) | Current v3 identities belong inside filters; Platform/OSS filter capabilities differ; relevance thresholds/top-k are not exact all-matches selection |
| Hindsight recall [118](https://hindsight.vectorize.io/developer/api/recall) | Temporal windows bias ranking rather than strictly filter; default tag modes include untagged facts; fact-text token budget can overflow for one result and excludes other response fields; chunks/source facts have separate truncation |
| Qdrant Query [115](https://api.qdrant.tech/api-reference/search/query-points) | Explicit named vectors, filters, prefetch, projection, approximation and consistency; equal dimensions do not imply compatible campaign spaces |
| Qdrant Scroll [116](https://api.qdrant.tech/api-reference/points/scroll-points) | Filtered page enumeration with continuation differs from recall; replica read consistency does not establish one snapshot across export requests |
| Project MCP tools | Transport/tool schema, not a universal KB language; mutation, navigation, retrieval and export remain separate capabilities |

CAO's current Dashboard Language already offers closed named projections, typed inert operands, schema validation, provenance, ordering and runtime budgets. Its source vocabulary and graph/vector capabilities are not a generic campaign KB API: no arbitrary path or proprietary-space nearest-neighbor clause is established here. Reuse compatible validated semantics; extend owned contracts explicitly. Dashboard queries remain in the worker/server data boundary, not components or client-side filtering. [120](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/docs/dashboard-language-specification.md)

</details>

<details>
<summary><strong>6. Evidence quality and existing CAO boundaries</strong></summary>

LoCoMo, LongMemEval and MemoryAgentBench cover conversational recall/update, temporal reasoning, abstention or behavioral forgetting—not complete authorization/erasure. Lost-in-the-Middle motivates selective context, without proving every modern model behaves identically. MINJA shows memory admission can be poisoned through ordinary interactions. [16](https://arxiv.org/abs/2402.17753v1), [17](https://arxiv.org/abs/2410.10813v2), [47](https://arxiv.org/abs/2507.05257v4), [48](https://arxiv.org/abs/2307.03172v3), [49](https://arxiv.org/abs/2503.03704v5)

Mem0's reported ~26% improvement over its OpenAI-memory comparison and ~91% lower p95 latency/>90% token savings versus full context use **different baselines**. Paper/model/judge/split/accounting differences prohibit a universal leaderboard. [8](https://arxiv.org/abs/2504.19413v1)

Primary methods/docs and bounded source paths were read; vendor outcomes were not reproduced. Unavailable publisher/model-card pages and limited evaluations remain documented in the reference inventory. GitHub counts/popularity and README badges are not independent accuracy, scale or compliance evidence.

At pinned CAO revision `fd69c356b8cf5a3e7a9243570e194e6805606306`, the repository-memory collector/loader supplies bounded ref/file transport and commit/object/hash validation—not the proposed lifecycle service. [50](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/activity/repository-memory.mjs#L10-L63), [51](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/server/internal/repositorymemory/memory.go#L32-L190)

CAO already separates canonical dashboard/query storage from focused operational services; composition validates provider capabilities, and application-owned maintenance is bounded. Its operational adapter named `memory` is volatile process state, not durable campaign knowledge. Redis's operational role does not automatically authorize it as a knowledge store. Reuse suitable coordination/contracts, preserve owned namespaces, and add no authority or dashboard entity by inference. [107](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md), [108](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/server-operational-storage.md)

Native capacity, acquisition sharing, and synthetic 100k inventory tests are prior art, not memory/inference throughput guarantees. Sharing permitted acquisition requirements does not authorize reuse across unrelated repositories/invocations. [52](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L1047-L1091), [53](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/github-api-capacity.md), [54](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/tests/load/control-plane-load.test.mjs#L134-L157), [55](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [56](https://docs.github.com/en/actions/reference/limits)

</details>

## Part II: References

All web references were consulted during the research snapshot unless explicitly described as a bibliographic-only lookup. Mutable documentation/repository URLs may change; primary paper versions and the implementation cross-check are pinned where consequential.

<details>
<summary><strong>Foundations and primary research (18 sources)</strong></summary>

[1](https://www.sciencedirect.com/science/article/pii/0004370279900080) Jon Doyle. *A Truth Maintenance System*. Artificial Intelligence 12(3), 231-272, 1979. Publisher bibliographic record; full-page retrieval failed during this investigation. [Source](https://www.sciencedirect.com/science/article/pii/0004370279900080)

[2](https://doi.org/10.2307/2274239) Carlos E. Alchourron, Peter Gardenfors, David Makinson. *On the Logic of Theory Change: Partial Meet Contraction and Revision Functions*. Journal of Symbolic Logic 50(2), 510-530, 1985. DOI: [DOI](https://doi.org/10.2307/2274239) . Accessible publisher abstract: [Source](https://www.cambridge.org/core/journals/journal-of-symbolic-logic/article/abs/on-the-logic-of-theory-change-partial-meet-contraction-and-revision-functions/7ED837BAD5FB6D9A7C77906D73527F9C)

[3](https://www.w3.org/TR/prov-dm/) W3C. *PROV-DM: The PROV Data Model*. Recommendation, April 30, 2013. Abstract and derivation/agent concepts. [Source](https://www.w3.org/TR/prov-dm/)

[4](https://arxiv.org/abs/2304.03442v2) Joon Sung Park et al. *Generative Agents: Interactive Simulacra of Human Behavior*. 2023. [Paper](https://arxiv.org/abs/2304.03442v2)

[5](https://arxiv.org/abs/2305.10250v3) Wanjun Zhong et al. *MemoryBank: Enhancing Large Language Models with Long-Term Memory*. 2023. [Paper](https://arxiv.org/abs/2305.10250v3)

[6](https://arxiv.org/abs/2303.11366v4) Noah Shinn et al. *Reflexion: Language Agents with Verbal Reinforcement Learning*. 2023. [Paper](https://arxiv.org/abs/2303.11366v4)

[7](https://arxiv.org/abs/2502.12110v11) Wujiang Xu et al. *A-MEM: Agentic Memory for LLM Agents*. NeurIPS 2025. Method sections 3.1-3.4; evaluation section 4. [Paper](https://arxiv.org/abs/2502.12110v11) ; [Full text](https://arxiv.org/html/2502.12110v11)

[8](https://arxiv.org/abs/2504.19413v1) Prateek Chhikara et al. *Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory*. April 2025 preprint. Section 2.1 operation semantics; abstract/experiments for qualified results. [Paper](https://arxiv.org/abs/2504.19413v1) ; [Full text](https://arxiv.org/html/2504.19413v1)

[9](https://arxiv.org/abs/2305.16291v2) Guanzhi Wang et al. *Voyager: An Open-Ended Embodied Agent with Large Language Models*. 2023. [Paper](https://arxiv.org/abs/2305.16291v2)

[10](https://arxiv.org/abs/2309.02427v3) Theodore Sumers et al. *Cognitive Architectures for Language Agents*. 2023; TMLR camera-ready version 2024. [Paper](https://arxiv.org/abs/2309.02427v3)

[11](https://arxiv.org/abs/2512.12818v1) Nicolò Boschi et al. *Hindsight is 20/20: Building Agent Memory that Retains, Recalls, and Reflects*. December 2025 preprint. Sections 1-3 and reported evaluations. [Paper](https://arxiv.org/abs/2512.12818v1) ; [Full text](https://arxiv.org/html/2512.12818v1)

[12](https://arxiv.org/abs/2310.08560v2) Charles Packer et al. *MemGPT: Towards LLMs as Operating Systems*. 2023; revised 2024. [Paper](https://arxiv.org/abs/2310.08560v2)

[13](https://arxiv.org/abs/2404.13501v1) Zeyu Zhang et al. *A Survey on the Memory Mechanism of Large Language Model based Agents*. April 2024 preprint. [Paper](https://arxiv.org/abs/2404.13501v1)

[14](https://arxiv.org/abs/2504.13171v1) Kevin Lin et al. *Sleep-time Compute: Beyond Inference Scaling at Test-time*. April 2025 preprint. [Paper](https://arxiv.org/abs/2504.13171v1) ; [Full text](https://arxiv.org/html/2504.13171v1)

[15](https://arxiv.org/abs/2602.06052v4) Wei-Chieh Huang et al. *A Survey of Agent Memory in the Second Half: Towards Self-Evolving and Long-Horizon Agents*. 2026; primary metadata reports TMLR acceptance with Survey Certification. [Paper](https://arxiv.org/abs/2602.06052v4) . Maintained literature inventory: [GitHub source](https://github.com/AgentMemoryWorld/Awesome-Agent-Memory)

[16](https://arxiv.org/abs/2402.17753v1) Adyasha Maharana et al. *Evaluating Very Long-Term Conversational Memory of LLM Agents*. LoCoMo, 2024. [Paper](https://arxiv.org/abs/2402.17753v1)

[17](https://arxiv.org/abs/2410.10813v2) Di Wu et al. *LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory*. ICLR 2025. [Paper](https://arxiv.org/abs/2410.10813v2)

[18](https://arxiv.org/abs/2607.18975v1) Xule Liu et al. *Mi-Memory: A Lifecycle Memory Framework for Personal AI*. July 2026 technical report. [Paper](https://arxiv.org/abs/2607.18975v1)

</details>

<details>
<summary><strong>Frameworks, products, and storage contracts (28 sources)</strong></summary>

[19](https://docs.langchain.com/oss/python/concepts/memory) LangChain. *Memory overview*: thread checkpoints, namespaces, profiles/collections, semantic/episodic/procedural types. [Documentation](https://docs.langchain.com/oss/python/concepts/memory)

[20](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/) LangMem. *Long-term Memory in LLM Applications*: extraction/consolidation pattern and storage choices. [Source](https://langchain-ai.github.io/langmem/concepts/conceptual_guide/)

[21](https://arxiv.org/abs/2501.13956v1) Preston Rasmussen et al. *Zep: A Temporal Knowledge Graph Architecture for Agent Memory*. January 2025 preprint. [Paper](https://arxiv.org/abs/2501.13956v1)

[22](https://docs.mem0.ai/core-concepts/memory-operations/add) Mem0. *Add Memory*: additive pipeline; managed/OSS distinctions; expiration visibility. [Documentation](https://docs.mem0.ai/core-concepts/memory-operations/add)

[23](https://arxiv.org/abs/2506.06326v1) Jiazheng Kang et al. *Memory OS of AI Agent*. 2025 preprint. [Paper](https://arxiv.org/abs/2506.06326v1)

[24](https://docs.mem0.ai/core-concepts/memory-operations/update) Mem0. *Update Memory Operation*: explicit replacement/content/metadata update. [Documentation](https://docs.mem0.ai/core-concepts/memory-operations/update)

[25](https://docs.letta.com/v1-sdk/concepts/stateful-agents) Letta. *Introduction to Stateful Agents*: persisted state, editable/shared memory blocks, evicted-message retrieval. [Documentation](https://docs.letta.com/v1-sdk/concepts/stateful-agents)

[26](https://docs.mem0.ai/core-concepts/memory-operations/delete) Mem0. *Delete Memory Operation*: individual/batch/filter deletion and explicit bulk-operation checks. [Documentation](https://docs.mem0.ai/core-concepts/memory-operations/delete)

[27](https://github.com/MemTensor/MemOS) MemTensor. *MemOS* project README: deployment distinctions, cubes, correction, plugins. [GitHub source](https://github.com/MemTensor/MemOS)

[28](https://arxiv.org/abs/2601.01885v3) Yi Yu et al. *Agentic Memory: Learning Unified Long-Term and Short-Term Memory Management for Large Language Model Agents*. AgeMem, 2026; primary metadata labels ACL 2026. [Paper](https://arxiv.org/abs/2601.01885v3)

[29](https://github.com/vectorize-io/hindsight) Vectorize. *Hindsight* project README: current observations, banks, mental models/pages, deployment model. [GitHub source](https://github.com/vectorize-io/hindsight)

[30](https://github.com/getzep/graphiti) Zep. *Graphiti* project README: entities/edges/episodes, temporal invalidation, bring-your-own database/service, distinction from Zep. [GitHub source](https://github.com/getzep/graphiti)

[31](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models) Vectorize. *Introducing Hindsight Models*. November 3, 2026. Decision-model design and vendor evaluation qualifications. [Source](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models)

[32](https://github.com/pgvector/pgvector) pgvector. README sections *Indexing*, *Filtering*, *Multitenancy*, and *Iterative Index Scans*. [GitHub source](https://github.com/pgvector/pgvector)

[33](https://www.postgresql.org/docs/current/ddl-partitioning.html) PostgreSQL. *Table Partitioning*. Partition strategies, operational benefits, and partitioned-constraint limitations. [Source](https://www.postgresql.org/docs/current/ddl-partitioning.html)

[34](https://www.postgresql.org/docs/current/ddl-rowsecurity.html) PostgreSQL. *Row Security Policies*. Default deny and owner/superuser/BYPASSRLS behavior. [Source](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)

[35](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank) Google Cloud. *Agent Platform Memory Bank*. Scope, generation/consolidation, TTL, revisions, IAM, and governance. [Documentation](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank) . Previously supplied Vertex AI URL redirects: [Source](https://cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/memory-bank/overview)

[36](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank/revisions) Google Cloud. *Inspect memory revisions*. Current/revision resources, mutation lifecycle, deletion recovery window. [Documentation](https://docs.cloud.google.com/gemini-enterprise-agent-platform/scale/memory-bank/revisions)

[37](https://arxiv.org/abs/2005.11401v4) Patrick Lewis et al. *Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks*. NeurIPS 2020. [Paper](https://arxiv.org/abs/2005.11401v4)

[38](https://github.com/BAI-LAB/MemoryOS) BAI-LAB. *MemoryOS* official README, including reported EMNLP 2025 acceptance. [GitHub source](https://github.com/BAI-LAB/MemoryOS)

[39](https://arxiv.org/abs/2507.03724v4) Zhiyu Li et al. *MemOS: A Memory OS for AI System*. 2025 preprint. [Paper](https://arxiv.org/abs/2507.03724v4)

[40](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory.html) AWS. *Add memory to your Amazon Bedrock AgentCore agent*. [Documentation](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory.html)

[41](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-strategies.html) AWS. *Memory strategies*: built-in, overrides, self-managed; no strategies means no long-term extraction. [Documentation](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-strategies.html)

[42](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-organization.html) AWS. *Memory organization in AgentCore Memory*: actor/session/strategy and namespace granularity. [Documentation](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/memory-organization.html)

[43](https://openai.github.io/openai-agents-python/sessions/) OpenAI. *Agents SDK Sessions*: conversation history lifecycle, limits, context merging, item/session removal. [Source](https://openai.github.io/openai-agents-python/sessions/)

[44](https://github.com/topoteretes/cognee) Cognee. Official project README: graph/chunk/code knowledge and session distillation. [GitHub source](https://github.com/topoteretes/cognee)

[45](https://github.com/plastic-labs/honcho) Plastic Labs. *Honcho* official README: workspaces, peers, asynchronous reasoning, representations, search. [GitHub source](https://github.com/plastic-labs/honcho)

[46](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/memory/main.py#L925-L963) Mem0 OSS implementation cross-check, pinned revision. `mem0/memory/main.py:925-963`; explicit helpers around lines 2052-2132. [GitHub source](https://github.com/mem0ai/mem0/blob/f1791daa86d469249c9b002f62644d58a1eb96a0/mem0/memory/main.py#L925-L963)

</details>

<details>
<summary><strong>Evaluation, safety, and fleet evidence (10 sources)</strong></summary>

[47](https://arxiv.org/abs/2507.05257v4) Yuanzhe Hu, Yu Wang, Julian McAuley. *Evaluating Memory in LLM Agents via Incremental Multi-Turn Interactions*. MemoryAgentBench, 2025; revised 2026. [Paper](https://arxiv.org/abs/2507.05257v4)

[48](https://arxiv.org/abs/2307.03172v3) Nelson F. Liu et al. *Lost in the Middle: How Language Models Use Long Contexts*. TACL acceptance noted in primary metadata. [Paper](https://arxiv.org/abs/2307.03172v3)

[49](https://arxiv.org/abs/2503.03704v5) Shen Dong et al. *Memory Injection Attacks on LLM Agents via Query-Only Interaction*. MINJA; 2025 with 2026 revision. [Paper](https://arxiv.org/abs/2503.03704v5)

[50](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/activity/repository-memory.mjs#L10-L63) Repository memory collector at checked revision `fd69c356b8cf5a3e7a9243570e194e6805606306`. [GitHub source](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/activity/repository-memory.mjs#L10-L63)

[51](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/server/internal/repositorymemory/memory.go#L32-L190) Repository memory snapshot/loader at the same checked revision. [GitHub source](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/server/internal/repositorymemory/memory.go#L32-L190)

[52](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L1047-L1091) Checked-out architecture: operation execution, native capacity planning, reviewed acquisition sharing boundaries, scheduler and accepted-value distinctions. `ARCHITECTURE.md:86-111,1047-1091`. Pinned revision: [GitHub source](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L1047-L1091)

[53](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/github-api-capacity.md) CAO GitHub API capacity specification: scenario inputs, expected versus hard-bound demand, request-grant admission, and collection spreading. `specs/github-api-capacity.md:13-143`. [GitHub source](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/github-api-capacity.md)

[54](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/tests/load/control-plane-load.test.mjs#L134-L157) Synthetic control-plane load tests: 100,000-repository inventory bounds and stable cells/batches. `tests/load/control-plane-load.test.mjs:134-157`. Read-only inspection; these repository tests were not executed for the research update. [GitHub source](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/tests/load/control-plane-load.test.mjs#L134-L157)

[55](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api) GitHub. *Rate limits for the REST API*. Primary installation/account distinctions and secondary concurrency, points, and content-creation limits. Consulted November 8, 2026, Pacific time. [Documentation](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)

[56](https://docs.github.com/en/actions/reference/limits) GitHub. *Actions limits*. Job-matrix, queuing, workflow, and account execution constraints; subject to change and support adjustments. Consulted November 8, 2026, Pacific time. [Documentation](https://docs.github.com/en/actions/reference/limits)

</details>

<details>
<summary><strong>First-party memory engineering and product contracts (19 sources)</strong></summary>

The following sources were consulted November 8, 2026, Pacific time / November 9, 2026, UTC. Dates identify publication, not a promise that the page remains identical to its original version.

[57](https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/) Tiferet Gazit, GitHub. *Building an agentic memory system for GitHub Copilot*. January 15, 2026. Citation-backed repository facts, just-in-time verification, historical retrieval design, and vendor evaluations. [Source](https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/)

[58](https://github.blog/changelog/2026-05-26-copilot-memory-has-more-controls-for-deletion-scope-and-the-copilot-cli/) GitHub Changelog. *Copilot Memory has more controls for deletion, scope, and the Copilot CLI*. May 26, 2026. Downvoting/deletion guidance, repository off switch, user/repository distinction. [Source](https://github.blog/changelog/2026-05-26-copilot-memory-has-more-controls-for-deletion-scope-and-the-copilot-cli/)

[59](https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory) GitHub Docs. *About GitHub Copilot Memory*. Current scope, ownership, citation judgment, voting, expiry, and disabled-versus-deleted semantics. The former `/concepts/agents/` URL redirected to this page. [Documentation](https://docs.github.com/en/copilot/concepts/prompting-and-context/copilot-memory)

[60](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents) Anthropic Applied AI team. *Effective context engineering for AI agents*. September 29, 2025. Just-in-time retrieval, progressive disclosure, compaction, structured notes, and context isolation. Former engineering URL redirects to: [Source](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents)

[61](https://claude.com/resources/articles/context-management) Anthropic / Claude. *Managing context on the Claude Developer Platform*. September 29, 2025. Context editing versus client-side memory, with separately qualified internal evaluation results. Former `anthropic.com/news/context-management` URL redirects to: [Source](https://claude.com/resources/articles/context-management)

[62](https://www.anthropic.com/research/effective-harnesses-for-long-running-agents) Anthropic. *Effective harnesses for long-running agents*. November 26, 2025. Initializer/coding-agent pattern, progress artifacts, incremental execution, acceptance criteria, and end-to-end testing limitations. [Source](https://www.anthropic.com/research/effective-harnesses-for-long-running-agents)

[63](https://claude.com/resources/articles/memory) Anthropic / Claude. *Bringing memory to Claude* (also indexed as *Bringing memory to teams*). September 11, 2025, with October 23, 2025 expansion update. Project-separated app memory, editable summary, incognito behavior. [Source](https://claude.com/resources/articles/memory)

[64](https://code.claude.com/docs/en/memory) Claude Code Docs. *How Claude remembers your project*. Current authored-instruction versus auto-memory distinction, machine-local repository/worktree sharing, 200-line/25-KB index, topic files, subagent distinction, and memory retention. [Source](https://code.claude.com/docs/en/memory)

[65](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool) Claude Platform Docs. *Memory tool*. Client-side file operations under `/memories`, caller-owned storage, database-backed handlers, and required path confinement. [Source](https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool)

[66](https://openai.com/index/unrolling-the-codex-agent-loop/) Michael Bolin, OpenAI. *Unrolling the Codex agent loop*. January 23, 2026. Prompt construction, AGENTS.md, stable-prefix caching, and conversation compaction; a historical harness description. [Source](https://openai.com/index/unrolling-the-codex-agent-loop/)

[67](https://developers.openai.com/blog/run-long-horizon-tasks-with-codex) Derrick Choi, OpenAI Developers. *Run long horizon tasks with Codex*. February 23, 2026. Durable project artifacts and a single approximately 25-hour experiment; not a production memory-service evaluation. [Source](https://developers.openai.com/blog/run-long-horizon-tasks-with-codex)

[68](https://openai.com/index/codex-for-almost-everything/) OpenAI. *Codex for (almost) everything*. April 16, 2026. Explicit Codex memory-preview announcement and continuing existing automation threads. [Source](https://openai.com/index/codex-for-almost-everything/)

[69](https://learn.chatgpt.com/docs/customization/memories?surface=app) OpenAI official documentation. *Memories*. Current local Codex memory generation/use controls, asynchronous eligibility, reset, local-home storage, and v1/v2 migration. The requested [Original URL](https://developers.openai.com/codex/memories) redirected to: [Source](https://learn.chatgpt.com/docs/customization/memories?surface=app) . The page explicitly distinguishes ChatGPT web memory from local Codex memory.

[70](https://github.com/openai/codex/blob/d579dafb706e20a5aa47ac21d06ba01009f9625c/codex-rs/memories/README.md) OpenAI Codex. *Memories* source README at commit `d579dafb706e20a5aa47ac21d06ba01009f9625c` (May 26, 2026). Bounded leased extraction, no-output/retry outcomes, scoped local-workspace consolidation, diff-based dirty checking, and artifact pruning. Read directly at the pinned revision; not a claim that all later clients/v2 use this pipeline. [GitHub source](https://github.com/openai/codex/blob/d579dafb706e20a5aa47ac21d06ba01009f9625c/codex-rs/memories/README.md)

[71](https://openai.com/index/chatgpt-memory-dreaming/) OpenAI. *Dreaming: Better memory for a more helpful ChatGPT*. June 4, 2026. Background synthesis, temporal freshness, reviewable summaries, and vendor compute-efficiency claims; ChatGPT, not Codex local-memory evidence. [Source](https://openai.com/index/chatgpt-memory-dreaming/)

[72](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra) OpenAI Developers. *Rethinking skills and prompts for GPT-6 Astra*. September 11, 2026. Model-sensitive instruction pruning and progressive disclosure, not an authorization policy. [Source](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra)

[73](https://developers.openai.com/blog/eval-skills) OpenAI Developers. *Testing Agent Skills Systematically with Evals*. January 22, 2026. Prompt sets, negative trigger controls, captured traces/artifacts, deterministic checks, and structured rubric grading. [Source](https://developers.openai.com/blog/eval-skills)

[74](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools) Isabella He, Claude Cookbook. *Context engineering: memory, compaction, and tool clearing*. March 20, 2026. Separate primitives and synthetic research-agent example; notebook inspected, not executed. [Source](https://platform.claude.com/cookbook/tool-use-context-engineering-context-engineering-tools)

[75](https://www.anthropic.com/research/components-of-agent-memory) Anthropic. *The components of agent memory*. November 6, 2026. Online salience, offline dreaming, tested lessons, source-backed correction, linked wiki evaluations, concurrent-write failures, injected memory hierarchy, and compute/cadence limitations. Full post read; vendor examples/evaluations were not independently reproduced. [Source](https://www.anthropic.com/research/components-of-agent-memory)

</details>

<details>
<summary><strong>Admission, lifecycle quality, and sanitization (14 sources)</strong></summary>

The following sources were consulted November 9, 2026, Pacific time. Primary paper methods and relevant limitations were read; reported results were not reproduced. Public READMEs/model cards are documentation evidence, not independently tested deployments.

[76](https://hindsight.vectorize.io/developer/decision-models) Hindsight. *Decision Models*. Typed decision integration, operation-specific behavior, failure paths, request logging, and self-hosted configuration. [Source](https://hindsight.vectorize.io/developer/decision-models)

[77](https://huggingface.co/hindsight-ai/hs-decide-1-flash) Hindsight AI. *hs-decide-1-flash* official model card. Qwen3.5-0.8B base, typed questions, one-pass probabilities, trained request bounds, vLLM plugin, and Apache 2.0 license. [Source](https://huggingface.co/hindsight-ai/hs-decide-1-flash) ; card read directly at [Source text](https://huggingface.co/hindsight-ai/hs-decide-1-flash/raw/main/README.md)

[78](https://arxiv.org/html/2508.19828v5) *Memory-R1: Enhancing Large Language Model Agents to Manage and Utilize Memories via Reinforcement Learning*. August 2025 preprint; v5 January 14, 2026. Two-agent method, memory operations, downstream reward, and training/evaluation boundaries. [Full text](https://arxiv.org/html/2508.19828v5)

[79](https://arxiv.org/html/2509.25911v1) *Mem-alpha: Learning Memory Construction via Reinforcement Learning*. September 30, 2025, v1. Memory construction, GRPO, QA/format/compression/content rewards, fixed RAG evaluation, and store operations. [Full text](https://arxiv.org/html/2509.25911v1)

[80](https://arxiv.org/html/2601.03192v2) *MemRL*. January 2026 preprint; v2 February 12, 2026. Frozen-model episodic memory, two-stage retrieval, and runtime feedback-updated utility. [Full text](https://arxiv.org/html/2601.03192v2)

[81](https://arxiv.org/html/2608.21867v1) *MemGuard*. August 22, 2026, v1. Multi-criterion trajectory verification, persistent lifecycle descriptors, admission/retrieval/consolidation governance, and reported matched-budget evaluations. [Full text](https://arxiv.org/html/2608.21867v1)

[82](https://arxiv.org/html/2605.14421v1) Ciyan Ouyang and Rui Hou. *MemLineage: Lineage-Guided Enforcement for LLM Agent Memory*. May 14, 2026, v1. Signed provenance, derivation propagation, action gating, deterministic/live evaluation distinction, adaptive fail-open cases, and discussion limitations. [Full text](https://arxiv.org/html/2605.14421v1)

[83](https://arxiv.org/html/2603.04549v1) *Adaptive Memory Admission Control for LLM Agents* (A-MAC). March 4, 2026, v1. Five-feature admission, ROUGE-L confidence proxy, utility LLM, learned scoring, and candidate-level evaluation. [Full text](https://arxiv.org/html/2603.04549v1)

[84](https://arxiv.org/html/2610.33015v1) *SLICE-Mem: Salience-Gated Lightweight Information Curation for Efficient LLM Agent Memory*. October 8, 2026, v1. Online gate/extraction, offline relation rules, experiments, limitations, and appendices A/B for teacher supervision, ModernBERT training, validation, and cost accounting. [Full text](https://arxiv.org/html/2610.33015v1)

[85](https://presidio.dataprivacystack.org/) Data Privacy Stack. *Presidio* official documentation. Analyzer versus anonymizer, configurable recognizers, current community ownership, and explicit incomplete-detection warning. Old Microsoft and intermediate documentation URLs redirected to [Source](https://presidio.dataprivacystack.org/)

[86](https://github.com/gitleaks/gitleaks) Gitleaks. Official README. File/git/stdin secret scanning, configuration/allowlists, scan coverage limits, output-redaction distinction, and current maintenance posture. [GitHub source](https://github.com/gitleaks/gitleaks)

[87](https://github.com/meta-llama/PurpleLlama/blob/main/Llama-Prompt-Guard-2/86M/MODEL_CARD.md) Meta. *Llama Prompt Guard 2 Model Card*. Public official GitHub copy: binary 22M/86M classifiers, 512-token bounds, modeling/evaluation, and adaptive/domain/multilingual limitations. [GitHub source](https://github.com/meta-llama/PurpleLlama/blob/main/Llama-Prompt-Guard-2/86M/MODEL_CARD.md) . Read directly at [Source text](https://raw.githubusercontent.com/meta-llama/PurpleLlama/main/Llama-Prompt-Guard-2/86M/MODEL_CARD.md) ; the attempted Hugging Face raw card returned HTTP 401.

[88](https://github.com/whyyyyy123/MemGuard) MemGuard. Public governance-core README. Available module interfaces and explicit absence of bundled benchmark harnesses, datasets, runtimes, and model clients. [GitHub source](https://github.com/whyyyyy123/MemGuard)

[89](https://github.com/GuilinDev/Adaptive_Memory_Admission_Control_LLM_Agents) A-MAC. Public reference-implementation README. Feature implementations, reported candidate metrics, sample weights, and under-review status; code and benchmark scripts were not executed. [GitHub source](https://github.com/GuilinDev/Adaptive_Memory_Admission_Control_LLM_Agents)

</details>

<details>
<summary><strong>Git storage and GitHub project implementations (17 sources)</strong></summary>

These official documentation pages were read November 9, 2026, Pacific time for the Git-backed proposal. No remote repository was created, cloned, written, or benchmarked. Recommended limits and documented APIs are not a throughput or erasure guarantee.

[90](https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits) GitHub Docs. *Repository limits*. Repository/object/directory/branch recommendations, Git read and push rates, enforced push/object limits, account repository cap, and integration data-ownership guidance. [Documentation](https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits)

[91](https://docs.github.com/en/rest/git/refs) GitHub Docs. *REST API endpoints for Git references*. Ref reads/publication, fast-forward versus force, and the absence of an expected-head request parameter. [Documentation](https://docs.github.com/en/rest/git/refs)

[92](https://docs.github.com/en/rest/repos/contents) GitHub Docs. *REST API endpoints for repository contents*. Reads at a ref, directory/file limits, file modes, and raw/object representations. [Documentation](https://docs.github.com/en/rest/repos/contents)

[93](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository) GitHub Docs. *Removing sensitive data from a repository*. History rewrite, clone/fork/cache/PR copies, support limitations, changed hashes, and recontamination. [Documentation](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)

[94](https://docs.github.com/en/rest/git/trees) GitHub Docs. *REST API endpoints for Git trees*. Base-tree preservation, multi-path changes, modes, and explicit recursive-tree truncation at 100,000 entries or 7 MB. [Documentation](https://docs.github.com/en/rest/git/trees)

[95](https://docs.github.com/en/rest/git/commits) GitHub Docs. *REST API endpoints for Git commits*. Tree-snapshot identity, parent selection, commit objects, and signature status. [Documentation](https://docs.github.com/en/rest/git/commits)

[96](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/managing-teams-and-people-with-access-to-your-repository) GitHub Docs. *Managing teams and people with access to your repository*. Repository-level access and roles; not a file-level memory access policy. [Documentation](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/managing-teams-and-people-with-access-to-your-repository)

All entries below were consulted November 9, 2026, Pacific time, at the pinned commits. The primary link is the project README; source/evaluation links identify the bounded cross-checks. Reported performance and compliance claims were not independently reproduced.

[97](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/README.md) Kitfunso. *Hippo* coding-agent memory. SQLite/Markdown authority, default hooks/scheduling, optional models/providers, feedback/invalidation, dormant retention, and evaluation caveats. [Pinned README](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/README.md)

[98](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/src/api/sleep/auto-sleep.ts) Hippo implementation cross-check. Write-triggered enabled/count/threshold gate and store latch; daily registered-workspace maintenance; learned-value module inspected selectively. [Auto-sleep](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/src/api/sleep/auto-sleep.ts#L25-L62), [daily scheduler](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/src/surface/cli/scheduler.ts#L172-L188), [memory-value module](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/src/domain/consolidate/memory-value.ts)

[99](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/docs/evals/2026-11-01-z0-round2-result.md) Hippo. *Z0 round 2 result: hippo adds no cost, and repeat mistakes are not shown to drop*. November 1, 2026. Sixteen-repository result, intervals, preregistered minimum, unreleased evaluated build distinction, and limited public reproducibility. [Evaluation](https://github.com/kitfunso/hippo-memory/blob/17d2e5b92bacee1419005991bda48cf85dfbf047/docs/evals/2026-11-01-z0-round2-result.md)

[100](https://github.com/OSU-NLP-Group/HippoRAG/blob/d5c8329422e0a0b834a15874545cb6a74b4f9b26/README.md) OSU NLP Group. *HippoRAG 2*. Associative graph retrieval, model/index identity, MCP mutation/read-only behavior, serialized calls, and shared Claude plugin index. [Pinned README](https://github.com/OSU-NLP-Group/HippoRAG/blob/d5c8329422e0a0b834a15874545cb6a74b4f9b26/README.md)

[101](https://github.com/amoghrman/hippo/blob/1a72789774eb31d9a02400f993a85d59c4b6ddb5/README.md) Amoghrman. *Hippo* Python library. PostgreSQL/pgvector, generative conflict handling, soft deletion, batch semantics, and configuration-dependent conflict checks. [Pinned README](https://github.com/amoghrman/hippo/blob/1a72789774eb31d9a02400f993a85d59c4b6ddb5/README.md), [conflict resolver](https://github.com/amoghrman/hippo/blob/1a72789774eb31d9a02400f993a85d59c4b6ddb5/src/hippo/conflict.py)

[102](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/README.md) Growth Kinetics. *DiffMem*. Git differential memory, local authority versus GitHub backup, worktree/branch model, scheduling/failure semantics, and documented migration. [Pinned README](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/README.md), [local storage](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/src/diffmem/storage/local_storage.py), [GitHub backup](https://github.com/Growth-Kinetics/DiffMem/blob/48ecbb61e7fedca40d1b41bdfb217a5f80432b20/src/diffmem/storage/github_backup.py)

[103](https://github.com/basicmachines-co/basic-memory/blob/b7d4006bd581c80c2bf4e4ca9a95f745ccee83a5/README.md) Basic Machines. *Basic Memory*. Canonical Markdown entities/observations/relations, optional retrieval/cloud paths, update/telemetry controls, current AGPL-3.0 license, and local coalescing. [Pinned README](https://github.com/basicmachines-co/basic-memory/blob/b7d4006bd581c80c2bf4e4ca9a95f745ccee83a5/README.md), [local scheduler](https://github.com/basicmachines-co/basic-memory/blob/b7d4006bd581c80c2bf4e4ca9a95f745ccee83a5/src/basic_memory/index/local_schedulers.py#L131-L182)

[104](https://github.com/doobidoo/mcp-memory-service/blob/238e609e7804874a772d6711b8acbd06526c7c55/README.md) Doobidoo. *MCP Memory Service*. SQLite/ONNX, optional secondary sync, capture/consolidation/quality features, bootstrap scope, and launcher claims. [Pinned README](https://github.com/doobidoo/mcp-memory-service/blob/238e609e7804874a772d6711b8acbd06526c7c55/README.md)

[105](https://github.com/volcengine/OpenViking/blob/c361405712d052cfd80440373c6e092aadf0714c/README.md) Volcano Engine. *OpenViking*. Virtual context filesystem, directory-scoped retrieval, L0/L1/L2 disclosure, semantic processing/server prerequisites, and component-specific licenses. [Pinned README](https://github.com/volcengine/OpenViking/blob/c361405712d052cfd80440373c6e092aadf0714c/README.md)

[106](https://github.com/one-bit/mnemoria/blob/7776a85a4e0437d2b0d5bbee16063430204b26dc/README.md) One-bit. *Mnemoria*. Binary append-only storage, local search/model, advisory locking, rebuild-on-open index, Git/LFS suggestions, and qualified local benchmarks. [Pinned README](https://github.com/one-bit/mnemoria/blob/7776a85a4e0437d2b0d5bbee16063430204b26dc/README.md)

</details>

<details>
<summary><strong>CAO service contracts and knowledge-query APIs (14 sources)</strong></summary>

The following checked-out sources were read November 9, 2026, Pacific time, at revision `fd69c356b8cf5a3e7a9243570e194e6805606306`. They document existing canonical/operational boundaries, not an already implemented agent-knowledge service.

[107](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md) CAO. *Architecture*. Provider-neutral canonical reader ownership and intentional adapter limitations; hosted canonical versus operational storage split; existing operational backend roles. Checked locations: `ARCHITECTURE.md:190-204,817-837,944-955,2035-2042`. [Canonical contract](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L817-L837), [storage split](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L944-L955)

[108](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/server-operational-storage.md) CAO. *Server operational storage*. Focused service consumption, application-owned bounded maintenance, capability validation, atomicity/CAS/leases/cancellation, explicit provider limitations, and shared conformance. Checked location: `specs/server-operational-storage.md:1-41`. [Contract](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/server-operational-storage.md#L1-L41)

Consulted November 9, 2026. These sources establish documented language/API semantics, not verified CAO adapter implementations. ISO GQL's public abstract/catalog was read; its full paid standard was not.

[109](https://www.postgresql.org/docs/current/sql-select.html) PostgreSQL. *SELECT*. Selection/joins/aggregation/projection/order/limits, recursive queries, and privileges. [Documentation](https://www.postgresql.org/docs/current/sql-select.html)

[110](https://www.w3.org/TR/sparql11-query/) W3C. *SPARQL 1.1 Query Language*. RDF graph patterns, property paths, grouping, datasets and result forms. [Recommendation](https://www.w3.org/TR/sparql11-query/)

[111](https://www.iso.org/standard/76120.html) ISO. *ISO/IEC 39075:2024 — Database languages — GQL*. Public publication record and property-graph language abstract. [Catalog](https://www.iso.org/standard/76120.html)

[112](https://neo4j.com/docs/cypher-manual/current/introduction/) Neo4j. *Cypher Manual — Introduction*. Declarative property-graph language and version/dialect distinctions. [Documentation](https://neo4j.com/docs/cypher-manual/current/introduction/)

[113](https://tinkerpop.apache.org/docs/3.8.2/tutorials/getting-started/) Apache TinkerPop. *Getting Started*, 3.8.2. Gremlin traversal sources and graph-provider abstraction. [Tutorial](https://tinkerpop.apache.org/docs/3.8.2/tutorials/getting-started/), [host-language embedding](https://tinkerpop.apache.org/gremlin.html)

[114](https://graphql.org/learn/queries/) GraphQL. *Queries*. API field selection, arguments, nested data, operation names and variables. [Guide](https://graphql.org/learn/queries/)

[115](https://api.qdrant.tech/api-reference/search/query-points) Qdrant. *Query points*, documentation default v1.20.x. Named vectors, filters, prefetch, limits, payloads, read consistency and approximation parameters. [API](https://api.qdrant.tech/api-reference/search/query-points), [search guide](https://qdrant.tech/documentation/search/search/)

[116](https://api.qdrant.tech/api-reference/points/scroll-points) Qdrant. *Scroll points*, documentation default v1.20.x. Enumeration, filtering/order and continuation offset. [API](https://api.qdrant.tech/api-reference/points/scroll-points)

[117](https://docs.mem0.ai/core-concepts/memory-operations/search) Mem0. *Search Memory*. Current Platform/OSS filter distinctions, v3 identity placement, semantic search and optional reranking/explanation. [Guide](https://docs.mem0.ai/core-concepts/memory-operations/search)

[118](https://hindsight.vectorize.io/developer/api/recall) Hindsight. *Recall Memories*. Bank-scoped recall, types, token/search budgets, temporal ranking rather than filtering, tag defaults and supplementary context limits. [API guide](https://hindsight.vectorize.io/developer/api/recall), [retrieval architecture](https://hindsight.vectorize.io/developer/retrieval)

[119](https://docs.langchain.com/oss/python/langgraph/stores.md) LangGraph. *Stores*. Namespaced arbitrary data, prefix enumeration, limits/offset, backend-specific order, and optional semantic indexing. [Guide](https://docs.langchain.com/oss/python/langgraph/stores.md)

[120](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/docs/dashboard-language-specification.md) CAO. *Dashboard Language Specification* at the checked revision. Closed query grammar, typed parameters/named-query transport, fail-closed bounds, provenance and data-layer execution; checked sections 5.5 and 5.5.5. [Query grammar](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/docs/dashboard-language-specification.md#L586-L660), [normative query contract](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/docs/dashboard-language-specification.md#L825-L858), [canonical data query boundary](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/dashboard-data.md#L3884-L3920)

</details>

## Part III: Suggested implementation

<details open>
<summary><strong>7. Core campaign model and ownership</strong></summary>

| Logical component | Minimum meaning |
|---|---|
| Identity/head | Trusted owner scope + semantic key/ID; current revision; serving block |
| Revision | Immutable bounded payload/schema/type, writer/operation identity, lifecycle/assertion, effective/recorded/expiry times |
| Evidence | Exact source/version/locator, support/contradiction, reverse cleanup locators |
| Relationships | Version-specific provenance/dependencies plus separately typed campaign semantic edges |
| Derived spaces | Compatible representation profile, exact source revision, projection/index checkpoint |

Campaigns can retain summaries, findings, package/dependency relationships, clusters, artifacts and tested lessons—not only prose facts. Schemas and record/edge types are campaign-owned; policy grants are not payload fields. Binary/large artifacts use approved bounded references and their own retention/access contract.

Resolve scope from **tenant + campaign/version + namespace + repository/shared pack + family/experiment arm** where applicable. Shared libraries need explicit disclosure/promotion review. Same key/schema/vector does not imply sharing. `depends_on` must preserve eligibility and acyclic provenance constraints; domain relationship graphs may contain cycles and use bounded traversal.

Keep focused knowledge, graph/embedding, evidence, policy, maintenance and dispatch contracts. Consumers receive packets and propose bounded mutations; provider SDKs, SQL, Git, graph drivers and credentials stay in adapters/composition. One physical backend may serve separate owned namespaces without becoming a universal service object.

Required semantics: scoped bounded access, immutable identities, expected-version no-lost-edit publication, idempotency, explicit conflicts/errors, current revocation/deletion barriers, declared durability, cancellation and resource limits. Validate actual capabilities before enabling a feature; reject unsupported operations rather than emulate success.

One scope has one authoritative composition. Immutable blobs plus a conditional manifest and transactional control registry can be valid, but separate successful object/queue/policy writes are not one transaction. Define durable publication/reconciliation for cross-resource boundaries. No dual masters, silent backend switches, or stale-cache fallback.

The four SQL tables below encode the memory-only subset. Generic campaign schemas, namespaces, semantic edges and representation descriptors require explicit implementation/migration/conformance; earlier DDL checks did not validate those extensions.

</details>

<details>
<summary><strong>8. Lazy activation: minimal server work</strong></summary>

| State | Meaning |
|---|---|
| Permitted | Current reviewed scope/mode/capability allows work |
| Active | Actual authorized consumer or explicit bounded maintenance request holds an activation window |
| Dirty | Eligible versions differ from completed checkpoint |

Routine maintenance requires **permitted + active + dirty + budget-admitted**. Stored records with `state='active'`, installed workflows, existing data or a new release do not activate maintenance.

The unit is the demanded campaign/family, repository/shared namespace, arm and space—not every repository in that family. Shared-pack ownership maintenance does not fan out to all consumers.

On use: resolve authority/release/barriers; fetch a bounded packet; coalesce activation renewal; compare source/head/profile generations; enqueue one capped existing durable job or run a bounded step in the current worker. An operational record needs scope key, `active_until`, wanted/completed generation, job marker and lease/fence—not a knowledge revision or Git commit per read.

Expiry is checked on access/job admission, without per-scope timers or deactivation commits. Expired jobs defer without loading content/models; restarted/fenced workers cannot publish stale work. The next use checks exact relevant source/ref versions and accumulates changes rather than replaying every event. Explicit source failure is unknown/unavailable, not unchanged.

Dirty inactive scopes do not wake. Catch lost events on next use; any required periodic memory reconciliation reads bounded indexed unexpired active units. Separately approved mission/inventory sweeps remain control-plane work.

| Path | Budget principle |
|---|---|
| Startup | Register adapters/config; no family enumeration, fleet clones, model loading or index replay |
| No active work | Zero routine memory provider reads, inference, scans, fetches/pushes or family timers |
| Unchanged eligible read | Bounded immutable cache after current barriers; no per-fact write |
| Cold scope | Fetch only required scoped records/pack; exact reads need no embedder/index |
| Changed active scope | One coalesced bounded batch in existing workers |
| Idle resources | Bounded cache/model retention and eviction; no indefinite warmed family |

No new event bus, global memory scheduler or HTTP-resident classifier is required. Without durable infrastructure, begin with bounded mission-worker maintenance, not fire-and-forget callbacks.

Inactive data may become stale: omit/report incomplete or budget an explicit check, not silently serve it as verified. Safety cleanup is independent: immediately block revocation/erase targets and queued writes; run required targeted purge to deadline, including inactive indexes/caches/backups. Missing dependency locations require fail-closed partition handling and incomplete cleanup status.

Zero routine idle work is not zero baseline hosting/provider cost. Measure actual active scopes, cold latency, working bytes and renewal/job overhead; do not scan all knowledge rows to discover activity.

</details>

<details>
<summary><strong>9. Proprietary embeddings and isolated representation spaces</strong></summary>

Standardize the contract, not the algorithm. Reviewed campaign workers may compute proprietary representations from permitted code/weights/providers. Private artifacts stay in approved execution storage; credentials stay in owned secret services, never records. The HTTP memory service does not download/run unreviewed code or retain every model.

Separate an approved **algorithm profile** from an immutable **space ID**. Compatibility covers model/algorithm release, query/document encoders, input schema, chunking/features/task conditioning, dimensions or sparse vocabulary, encoding, metric and normalization. Use opaque identifiers where naming/fingerprints would disclose private details.

Two same-sized vectors can be incompatible. Do not mix spaces, pad/hash/reshape to pretend capability, or infer an encoder from dimensions. Dense, sparse, multi-vector and custom metrics require explicit supported adapters. A specialized engine returns bounded domain results, not arbitrary campaign callbacks in the server.

**Illustrative space descriptor**

```json
{
  "space_id": "space-7d31",
  "owner_namespace": "campaign-a:repo-analysis",
  "algorithm_release": "private-embedding-release-3",
  "input_schema": "repository-summary-v2",
  "preprocessing_release": "features-v4",
  "representation": {
    "kind": "dense",
    "dimensions": 768,
    "encoding": "float32"
  },
  "distance": "cosine",
  "normalization": "unit-l2",
  "query_encoder_release": "private-query-release-3"
}
```

This is a proposal, not an existing CAO API. The service validates registration/ownership and resolves the compatible encoders from reviewed configuration.

Publish space ID, exact record/revision/content identity, producer/operation provenance and vector/approved artifact reference. Validate shape, finite values where relevant, size, encoding, source eligibility and scope. Embeddings/indexes/query vectors can disclose source information and inherit its retention/access rules.

Semantic queries select a space and compatible query representation or approved query encoder explicitly. Failure cannot switch to a public provider/default space or lexical results. Only a declared multi-channel request may combine labeled partial results; cross-space distances are not inherently comparable.

Exact records and graph traversal activate no embedder. Update canonical data by marking relevant spaces dirty; project only activated inputs. Missing/stale vectors report unavailable/partial under the requested contract and cannot serve a newer head. Model changes create side-by-side spaces, staged through isolated experiments—not fleet-wide immediate re-embedding.

Erase/revoke invalidates records, edges, vector hits and known copies immediately; reverse record/source-to-space locators drive required purge independently of space activity. Migration never resurrects erased versions.

</details>

<details>
<summary><strong>10. Extraction and query API</strong></summary>

| Operation | Promise |
|---|---|
| `readRecords` | Exact known IDs/keys/revisions/fields, bounded and authorized; zero encoder work |
| `queryRecords` | Reviewed predicates/projection/aggregation with defined types/nulls/order/schema |
| `traverseGraph` | Explicit seeds, edge types/direction; budgets and visibility at every hop |
| `searchSpace` | Compatible profile and declared ranking/approximation; not exhaustive extraction |
| `exportSelection` | Explicit snapshot/continuation and completion accounting |
| `synthesize` | Optional authorized generated answer with evidence, not canonical completeness |

Use a versioned HTTP/SDK surface and, if useful, thin read-only MCP tools. REST/GraphQL/MCP are transports, not permission or semantics. Mutations stay on the declared trusted safe-output path.

Reuse CAO's closed declarative query semantics for compatible reviewed sources. Named query IDs bind typed inert operands; graph/vector capabilities absent from that grammar require explicit owned extensions/conformance. No new general scripting DSL, unregistered source injection, arbitrary native query credentials, or main-thread/dashboard fallback.

**Illustrative named-query request**

```json
{
  "operation": "queryRecords",
  "namespace": "campaign-a:repo-analysis",
  "query_id": "campaign-a-active-findings-v1",
  "parameters": {
    "repository_id": "12345",
    "minimum_severity": 2
  },
  "consistency": "snapshot",
  "page": {
    "limit": 100,
    "cursor": null
  },
  "budget": {
    "max_bytes": 262144,
    "timeout_ms": 500
  }
}
```

Not an implemented route or valid existing Dashboard Language document. Definition/schema/order/scope/capabilities are service-owned; these numerical limits are illustrative.

Responses distinguish **availability**, **exact versus ranked/approximate selection**, **page completion/more pages**, and **evidence coverage**. Include record/revision/source identities, query/profile/index versions, applied scope and freshness. Relevance is not confidence; empty valid selection is not unavailable.

Opaque cursors bind scope, query/operands, stable tie-break order, snapshot/version and expiry. Recheck current authority on every page; a snapshot cannot retain revoked rights. Barrier changes that break export guarantees invalidate continuation. Reject required snapshots on incapable adapters; offsets over mutable data are not a substitute.

Large exports use explicitly requested bounded existing jobs/streams and version/count manifests, not whole-fleet HTTP buffering. Runtime limits bound input rows, joins, graph expansion, candidates, encoder/context calls, emitted bytes and time. Estimates/`limit` do not replace enforcement.

Keep Dashboard Language's excess/cancellation fail-closed behavior. Partial graph/export progress exists only under a separately accepted explicit contract; errors cannot become empty success. Do not use client filtering to enforce namespace scope or return a different query mode when requested search fails.

</details>

<details>
<summary><strong>11. Admission, concurrency, corrections and erasure</strong></summary>

**Pipeline:** authorize scope/source and activation → minimize/sanitize → exact dedup/cheap rules → optional utility gate → evidence-preserving candidate → capped eligible-neighbor checks → write only needed supported changes → rescan → trusted expected-version publication → bounded active maintenance.

Trusted correction/revocation/erase pathways do not depend on salience. Distinguish reject, hold, no-op, attach evidence, needs-writer and propose-conflict. A hold cannot justify indefinite prohibited raw retention. Required-control failure halts persistence; optional quality fallback must be equally controlled and budgeted.

Model calls run outside short locks/transactions. Publication rechecks head/version, current policy/source/namespace/arm, serving block, lease/fence and deletion epoch. Append immutable revision/evidence/links, conditionally advance head, and durably compose or reconcile projection jobs. Concurrent canonical-key creation reconciles the winner; stale proposals re-plan. At-least-once work plus idempotency is not end-to-end exactly-once.

| Situation | Rule |
|---|---|
| Same source/version/locator/claim | Duplicate, not independent corroboration |
| Same supported value, independent evidence | Corroborate through a new revision |
| Authoritative correction/effective-time change | Revise the scoped slot |
| Compatible contextual values | Preserve separate/applicable records |
| Incompatible evidence without winner | Conflict, not last-arrival-wins |
| Failed tool/build episode | Bounded evidence/caution, not universal procedure |
| Rollback | New approved revision, never restoration of erased/revoked/unsupported content |

Preserve negation, speaker, entity/event identity and effective time. Similar repeated events must not collapse; contextual preferences are not universal reversals. A later ingested old document cannot revert a newer authoritative source.

Separate prompt omission, priority decay, archival/invalidation and actual erasure. Block serving immediately; invalidate dependent summaries/procedures/spaces; purge accepted payloads, evidence copies, caches, logs, jobs and replicas/backups per the declared contract; prevent stale job/overlay resurrection. `derived_from` records historical provenance; `depends_on` requires parent eligibility/currentness.

Source/dependency reverse lookup and tombstone/epoch barriers matter more than ANN sophistication. Pins/popularity/confidence cannot override erase or current policy. When required coverage is unavailable, report incomplete rather than infer safety.

</details>

<details>
<summary><strong>12. Backing choices and the optional Git adapter</strong></summary>

| Backing | Use when | Non-negotiable limitation |
|---|---|---|
| Relational SQL | Narrow transactions, corrections, reverse indexes, structured queries | Deliberate row/history/index costs; local SQLite is not distributed coordination |
| KV/document | Scope/key locality, conditional updates, sparse overlays | Actual CAS/multi-item transaction bounds, reverse locators and typed errors |
| Object storage + conditional manifests | Immutable packs/cold payloads and cheap reuse | Trusted heads, bounded direct lookups, durable cross-resource reconciliation |
| Log + snapshots | Traceable evolution/replay | Bounded checkpoints; current eligibility cannot rely on stale projections; retained-event erase contract |
| Graph | Measured relationship-heavy reads | Version/scope/cleanup requirements still apply; domain cycles need bounds |
| Git | Reviewable low-churn sanitized bundles/releases | Repository-level disclosure, publication contention, history/copy erase difficulty |
| Search/vector | Derived candidate selection | Authoritative only in a composition supplying missing publication/evidence/cleanup guarantees |
| Process maps | Bounded cache/test/explicit ephemeral state | Not durable fleet knowledge or a fallback for failed persistence |

Prefer a suitable existing deployment resource, then measure cold/idle costs and capability gaps. Keep one authoritative composition per scope. “Same interface” cannot promise equal transactions, exact search, latency, or erase guarantees.

**Git mode:** bounded schema-versioned bundles and pinned shared manifests, not four million individual files or a branch/repo per 100k scope. Resolve trusted direct paths; reject unsupported symlinks/submodules/modes/schema/size. Avoid startup clones and global tree walks. Contents directory reads cap at 1,000 entries; recursive trees can truncate at 100k entries/7 MB. [92](https://docs.github.com/en/rest/repos/contents), [94](https://docs.github.com/en/rest/git/trees), [95](https://docs.github.com/en/rest/git/commits)

GitHub recommends 10 GB compressed repo size, 3,000 entries/directory, 5,000 branches, 6 pushes/minute and 15 Git reads/second/repository; 2 GB pushes and 100 MB objects are enforced ceilings. Account/org repository cap is 100k. These are not SLA promises. GitHub also advises generated data outside Git and user-owned integration data: reserve Git for an explicitly suitable curated workload. [90](https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits)

A principal with backing-repository read access can obtain other paths/history: folders, sparse checkout and orphan branches are not ACLs. Align disclosure groups or use an explicitly trusted service; do not give agents broad tokens. Target-local refs remain an explicitly authorized topology, not control-policy authority. [96](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/managing-teams-and-people-with-access-to-your-repository)

**Publish:** validated expected records → tree from observed base → commit parented at observed head → controlled non-force ref advance → conflict reread/revalidation → idempotent receipt/reconciliation. One ref gives an atomic tree snapshot within one repository, not across repositories/queues/policy. Ref PATCH has no general expected-head argument; restrict rewrites and out-of-band writers, fence publication in trusted infrastructure. Unattached objects are not accepted publication. [91](https://docs.github.com/en/rest/git/refs), [94](https://docs.github.com/en/rest/git/trees), [95](https://docs.github.com/en/rest/git/commits)

Deleting a current file/tombstoning is not removal from old commits, refs, clones, forks, PR views or caches. History rewrite changes hashes and risks recontamination; support-assisted cleanup has limits. Sanitize before creating objects and reject Git where the required historical purge cannot be met. SQL also needs backup/source cleanup. [93](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)

Batching matters: 80k one-change commits/day means ~55.6 centralized ref updates/minute; 16 evenly loaded shards reduce only the average to ~3.47. A 100-change batch gives ~800 commits and at least 2,400 tree/commit/ref write requests, plus reads/retries/blobs—before byte, shard, review and quota splits. Rewriting 40 KiB per changed record can add 3.2768 GB/day uncompressed snapshot input versus 81.92 MB of changed payload; deltas/coalescing require measurement. No shard/token creation is authorized by this arithmetic.

</details>

<details>
<summary><strong>13. Fleet releases, experiments and capacity</strong></summary>

Use immutable shared releases, compact scoped facts and sparse arm overlays. Pin prompt/tools/reader/writer/model/profile policy and consumed versions; include semantic configuration, relevant source/evidence freshness and authority in reuse identity. Formatting-only changes may be excluded only under an explicit definition.

Release/experiment/attempt/publication/memory-operation identities serve different purposes. Reusable success needs exact relevant identities and current authority; failed/ambiguous attempts need bounded reconciliation, not successful cache hits. Events dirty scopes; they do not activate them.

Experiments keep baseline packs pinned, collect canonical facts independently of treatment, and isolate evolving arm stores. Sticky assignment survives repeats. Overlay tombstones suppress inherited keys before filtering deleted records; they are not physical erasure. Interacting missions require repository/resource serialization or cluster randomization. Promotion is a new disclosure decision with held-out evidence and review.

Freeze hypothesis, population/unit, release/space versions, primary outcome/window, guardrails, power/precision target, missingness and stopping rules. Report unique repositories and correlated repeats separately; use clustered uncertainty and intention-to-treat alongside executed-attempt outcomes. Avoid success-only selection, daily peeking, multiple-variant winner bias and treating rare outcomes as powered merely because 100k repos exist.

**Illustrative all-enrolled mission scenario—not memory cadence:** four families, 100k repositories, daily eligibility checks, 10% expensive runs, 40 current records/repo, 3 retained revisions, 1 KiB payload, 2% churn, optional vectors on 25%.

| Quantity | Implication |
|---|---|
| Checks / expensive runs | 400k / 40k per day |
| Records / revisions | 4 million / 12 million |
| Revision payload / optional 768-float32 coordinates | 12.288 GB / 3.072 GB |
| New revisions / payload | 80k / 81.92 MB per day |
| 30-day check + run receipts | 17.0304 GB payload; separate from knowledge |
| Two-minute runs, 70% utilization | 80 slots over 24h; 318 over 6h |
| Full 400k expensive runs | 794 slots over 24h; 3,175 over 6h |
| Four literal worker slots | Only 2,016 runs/day |
| 6,000 tokens per expensive run | 240M/day, excluding memory maintenance |

The fixed three-revision average requires retention: 80k/day adds 12M revisions in 150 days otherwise. Provision row/index/evidence/WAL/replica/backup/source overhead from measurement. Content budgets are not permission to discard required constraints.

For one hypothetical 12,500 Core/hour pool with 2,500 reserve, usable capacity is 240k/day. Assumed 1,000 inventory/day plus 9.01 expected requests/run gives **361,400/day** for the baseline; hard-bound 22/run gives **881,000/day**; full execution gives **3,605,000/day**. Smoothing cannot fix sustained excess. Actual installation/account routing, skew, secondary writes/points, Search/GraphQL, dispatch/output pools and grants are separate. More workers do not create quota. [52](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/ARCHITECTURE.md#L1047-L1091), [53](https://github.com/githubnext/gh-cao/blob/fd69c356b8cf5a3e7a9243570e194e6805606306/specs/github-api-capacity.md), [55](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), [56](https://docs.github.com/en/actions/reference/limits)

Memory gates use **candidate events**, not enrolled repos. A separate illustrative 10 candidates/run, 90% cheap resolution, 2k input tokens/gate, 20% writer routing at 3k tokens gives 40k decision calls/80M gate tokens and 8k writers/24M writer tokens. At the Hindsight snapshot price $0.02/M input tokens, the gate component alone is $1.60/day—not total cost or measured selectivity. Compare with a deterministic baseline, not “generate everything.” [31](https://hindsight.vectorize.io/blog/2026/11/03/hindsight-models), [76](https://hindsight.vectorize.io/developer/decision-models)

**Reproducible original fleet assumptions**

```json
{
  "scenario": "four-families-100k-repositories",
  "repositories": 100000,
  "agent_families": 4,
  "checks_per_family_per_day": 1,
  "expensive_fraction": 0.10,
  "general_records_per_repository": 20,
  "family_records_per_repository": 5,
  "average_retained_revisions": 3,
  "revision_payload_bytes": 1024,
  "daily_current_record_change_fraction": 0.02,
  "optional_embedding_fraction": 0.25,
  "embedding_dimensions": 768,
  "embedding_bytes_per_dimension": 4,
  "check_receipt_bytes": 600,
  "expensive_receipt_bytes": 8192,
  "receipt_retention_days": 30,
  "expensive_minutes": 2,
  "worker_utilization": 0.70,
  "inference_tokens_per_expensive_run": 6000,
  "experiment_treatment_fraction": 0.02,
  "inventory_core_requests_per_day": 1000,
  "expected_evidence_core_requests_per_run": 8,
  "hard_bound_evidence_core_requests_per_run": 20,
  "dispatch_core_requests_per_run": 1,
  "expected_output_core_requests_per_run": 0.01,
  "hard_bound_output_core_requests_per_run": 1,
  "hypothetical_pool_hourly_limit": 12500,
  "hypothetical_pool_hourly_reserve": 2500
}
```

These remain planning inputs, not observed demand or rollout authority. Lazy maintenance substitutes actual demanded scopes/changes.

Optimize valid unchanged-work reuse, permitted acquisition reuse, evidence requests, fair quota-window scheduling, then reviewed scope/cadence/capacity. Cold-start new releases and experiments must fit budgets; no global consolidation lock or midnight 100k fan-out.

</details>

<details>
<summary><strong>14. Implementation and validation gates</strong></summary>

| Phase | Required evidence before expansion |
|---|---|
| Core records | Suitable selected adapter; exact reads, schema/ownership, conflicts/no-op writes, inspection, inactive-fleet behavior |
| Trustworthy lifecycle | Durable/reconciled publication, revocation/erase, idempotency, cancellation and fencing across every serving path |
| Better retrieval | Held-out value exceeds full index/encoder/maintenance cost; no scope, head or completeness regression |
| Learning/promotion | Accepted outcomes and controlled isolated comparisons justify policy/procedure changes |

Test real adapters through owned services: concurrent same-key mutation, next-read visibility, crash after publish, replay, stale/fenced jobs, malformed/schema/size failures, unsupported capability, current source denial, reverse cleanup and old-version resurrection.

Test lazy behavior: no inactive content/model/provider work; one activated scope/space causes no siblings; unchanged reads produce no revisions; dirty bursts coalesce; expired work loads no model; lost events are detected on next use; cache/model limits hold; inactive erase still completes.

Test extraction/embeddings: exact fixture IDs/fields/paths, null/missing, prefix boundaries, tie-stable paging, incapable snapshots, deletion between pages, graph cycles and input budgets, same-sized incompatible spaces, query/document encoder mismatch, stale hits and declared partial channels. Missing/skipped backends are incomplete—not conformance.

Separate admission precision/recall and crucial-correction loss; false merges/supersessions and unsupported claims; secret/PII/injection coverage and transformed/log/index leakage; calibrated outcomes/drift; semantic recall; answer correctness; accepted task value per total dollar. Finite tests do not prove zero risk.

**Already verified, not reimplemented here:** earlier core PostgreSQL integrity checks and fleet scope/tombstone extension ran on disposable PostgreSQL 18.6; sizing arithmetic and reference composition passed; synthetic sticky assignment over 100k IDs gave 2,057 treatment/97,943 control. These are not production authorization/isolation/throughput or causal-power evidence. Compaction preserves DDL/assumptions and validates document structure, not the proposed generic service.



**Bottom line:** implement small owned campaign contracts over a conforming backing structure. Keep evidence/version semantics stable, resources cold until demanded, and authority/erase enforced independently. Do not start with a universal model, general scripting language, per-repository brain, whole-fleet dreaming, or a storage choice dictated by the latest framework.

</details>

<details>
<summary><strong>Appendix. Memory-only PostgreSQL DDL (unchanged)</strong></summary>

```sql
CREATE TABLE memory (
    tenant_id uuid NOT NULL,
    memory_id uuid NOT NULL,
    scope_kind text NOT NULL
        CHECK (scope_kind IN ('user', 'repository', 'team', 'agent')),
    scope_id text NOT NULL CHECK (octet_length(scope_id) BETWEEN 1 AND 256),
    memory_key text NOT NULL CHECK (octet_length(memory_key) BETWEEN 1 AND 512),
    head_revision bigint NOT NULL CHECK (head_revision > 0),
    blocked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (tenant_id, memory_id),
    UNIQUE (tenant_id, scope_kind, scope_id, memory_key)
);

CREATE TABLE memory_revision (
    tenant_id uuid NOT NULL,
    memory_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    operation_id uuid NOT NULL,
    kind text NOT NULL
        CHECK (kind IN ('fact', 'preference', 'episode', 'procedure', 'summary')),
    state text NOT NULL
        CHECK (state IN ('candidate', 'active', 'conflicted', 'archived', 'deleted')),
    assertion text NOT NULL
        CHECK (assertion IN ('explicit', 'observed', 'inferred')),
    content text NOT NULL CHECK (octet_length(content) <= 16384),
    value jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(value) = 'object' AND octet_length(value::text) <= 16384),
    effective_at timestamptz,
    recorded_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    expires_at timestamptz,
    pinned boolean NOT NULL DEFAULT false,
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 256),
    reason_code text NOT NULL CHECK (octet_length(reason_code) BETWEEN 1 AND 128),
    writer_config text NOT NULL CHECK (octet_length(writer_config) BETWEEN 1 AND 256),
    PRIMARY KEY (tenant_id, memory_id, revision),
    UNIQUE (tenant_id, memory_id, operation_id),
    FOREIGN KEY (tenant_id, memory_id)
        REFERENCES memory (tenant_id, memory_id) ON DELETE CASCADE,
    CHECK (expires_at IS NULL OR effective_at IS NULL OR expires_at > effective_at),
    CHECK (state <> 'deleted' OR (content = '' AND value = '{}'::jsonb))
);

ALTER TABLE memory ADD CONSTRAINT memory_head_fk
    FOREIGN KEY (tenant_id, memory_id, head_revision)
    REFERENCES memory_revision (tenant_id, memory_id, revision)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE memory_evidence (
    tenant_id uuid NOT NULL,
    memory_id uuid NOT NULL,
    revision bigint NOT NULL,
    source_kind text NOT NULL
        CHECK (source_kind IN
            ('user_message', 'tool_result', 'document', 'human_review', 'test_result')),
    source_id text NOT NULL CHECK (octet_length(source_id) BETWEEN 1 AND 256),
    source_version text NOT NULL CHECK (octet_length(source_version) BETWEEN 1 AND 128),
    locator text NOT NULL DEFAULT '' CHECK (octet_length(locator) <= 256),
    relation text NOT NULL CHECK (relation IN ('supports', 'contradicts')),
    PRIMARY KEY (
        tenant_id, memory_id, revision,
        source_kind, source_id, source_version, locator, relation
    ),
    FOREIGN KEY (tenant_id, memory_id, revision)
        REFERENCES memory_revision (tenant_id, memory_id, revision) ON DELETE CASCADE
);

CREATE TABLE memory_link (
    tenant_id uuid NOT NULL,
    child_memory_id uuid NOT NULL,
    child_revision bigint NOT NULL,
    parent_memory_id uuid NOT NULL,
    parent_revision bigint NOT NULL,
    relation text NOT NULL CHECK (relation IN ('derived_from', 'depends_on')),
    PRIMARY KEY (
        tenant_id, child_memory_id, child_revision,
        parent_memory_id, parent_revision, relation
    ),
    FOREIGN KEY (tenant_id, child_memory_id, child_revision)
        REFERENCES memory_revision (tenant_id, memory_id, revision) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, parent_memory_id, parent_revision)
        REFERENCES memory_revision (tenant_id, memory_id, revision) ON DELETE CASCADE,
    CHECK (child_memory_id <> parent_memory_id OR child_revision <> parent_revision)
);

CREATE INDEX memory_scope_idx
    ON memory (tenant_id, scope_kind, scope_id, memory_id);

CREATE INDEX memory_revision_history_idx
    ON memory_revision (tenant_id, memory_id, recorded_at DESC, revision DESC);

CREATE INDEX memory_evidence_source_idx
    ON memory_evidence (tenant_id, source_kind, source_id, source_version);

CREATE INDEX memory_link_parent_idx
    ON memory_link (tenant_id, parent_memory_id, parent_revision);
```

The original enum does not encode arbitrary campaign schemas/namespaces, semantic edges or embedding spaces. Those require a reviewed implementation/migration and new tests. The fleet extension adds cohort/experiment/repository-variant scopes; the service resolves their identities rather than trusting client strings.

Checks/FKs do not enforce access, evidence validity, DAG bounds, immutable revisions, atomic outbox behavior or erase coverage. `writer_config` remains a bounded text release ID, not arbitrary JSON; deleted snapshots are empty. RLS requires separate owner/BYPASSRLS review, and partitioned constraints must include partition keys. [33](https://www.postgresql.org/docs/current/ddl-partitioning.html), [34](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)

</details>
