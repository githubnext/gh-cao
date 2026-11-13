# Work Routing

Read [mission](mission.md), [governance](governance.md) and
[process](process.md) before any route. The table resolves actual expertise;
it never authorizes tools, commands, mutations or repository reach.

## Routing Table

| Work Type | Route To | Examples |
|-----------|----------|----------|
| assessor | `assessor` | Bounded phase-scoped expertise: Read-only static estate inventory and evidence gaps. No recommendations, fabricated metrics/estimates, mandatory build or execution. |
| classifier | `classifier` | Bounded phase-scoped expertise: Six-R comparison, feasibility/trade-offs and sourced cost assumptions. User selects; does not execute or author the selected plan. |
| planner | `planner` | Bounded phase-scoped expertise: Selected-path plan, cross-repository dependencies, measurable phase criteria, integrated data/cutover/rollback plan and permissions. Does not authorize execution. |
| execution-orchestrator | `execution-orchestrator` | Dependency-gated specialist sequencing inside the single CAO-admitted target only; requires current approved issue/plan/source scope, explicit mode and dependency evidence. No fleet discovery, cross-repository dispatch, publication or merge. |
| reviewer | `reviewer` | Bounded phase-scoped expertise: Independent revision-bound artifact/diff quality verdict; neither implements reviewed work nor grants operational permission. |
| validator | `validator` | Bounded phase-scoped expertise: Pre-implementation acceptance-design/testability/evidence-sufficiency consultation; later independently measured outcomes, characterization/data/cutover/rollback verification under separate authorization. Planned tests are not results; never self-certifies its own contribution or execution. |
| documentation | `documentation` | Bounded phase-scoped expertise: On-demand evidence-linked playbooks, technical writing, runbooks and diagrams. Phase owners retain technical decisions and phase accountability. |
| db-sqlserver | `db-sqlserver` | Bounded phase-scoped expertise: T-SQL, procedures, SQL Agent, CLR, collations, transactions, HA/DR and Azure SQL/managed variants. |
| db-postgresql | `db-postgresql` | Bounded phase-scoped expertise: Extensions, PL/pgSQL, MVCC, indexing/partitioning, logical replication and Azure offering constraints. |
| db-oracle | `db-oracle` | Bounded phase-scoped expertise: PL/SQL, data types, optimizer, tablespaces, RAC/Data Guard, security and licensing constraints. |
| db-mysql | `db-mysql` | Bounded phase-scoped expertise: MySQL/MariaDB differences, storage engines, routines, replication and query semantics. |
| db-mongodb | `db-mongodb` | Bounded phase-scoped expertise: BSON, document models, aggregation, indexing/sharding, change streams and compatibility limits. |
| db-cosmosdb | `db-cosmosdb` | Bounded phase-scoped expertise: API selection, partition keys, RU/cost modeling, consistency, multi-region behavior and SDK contracts. |
| db-db2 | `db-db2` | Bounded phase-scoped expertise: Mainframe/distributed Db2 dialects, packages/host variables, utilities and replication; qualified operators required for actual operations. |
| db-cassandra | `db-cassandra` | Bounded phase-scoped expertise: CQL, partition/clustering design, consistency, repair/compaction and compatible-target limitations. |
| db-dynamodb | `db-dynamodb` | Bounded phase-scoped expertise: Key/index design, single-table patterns, conditional writes, Streams/TTL and transition from an AWS source. |
| db-redis | `db-redis` | Bounded phase-scoped expertise: Authoritative primary-store structures, persistence, transactions/Lua, clustering and Streams; cache-only concerns belong to Cache. |
| db-couchbase | `db-couchbase` | Bounded phase-scoped expertise: Buckets/scopes/collections, KV/sub-documents, N1QL, indexing, eventing and XDCR. |
| db-sybase | `db-sybase` | Bounded phase-scoped expertise: SAP ASE dialect, procedures/tempdb, locking, replication and migration compatibility. |
| expert-auth-identity | `expert-auth-identity` | Bounded phase-scoped expertise: LDAP/AD/Entra, OAuth/OIDC/SAML, federation, claims, user/service identity and authorization flows; not credential lifecycle ownership. |
| expert-hosting | `expert-hosting` | Bounded phase-scoped expertise: IIS, Windows Services, Tomcat/JBoss/WebLogic, VMs, Docker/Kubernetes/AKS, topology and runtime lifecycle/coexistence. Consumes Azure platform guardrails; does not own pipelines or select targets by preference. |
| expert-cicd | `expert-cicd` | Bounded phase-scoped expertise: GitHub Actions, Azure DevOps, Jenkins, artifact provenance, promotion, release gates and delivery/rollback automation. Does not own platform policy, runtime topology or grant provisioning/publication/cutover permission. |
| expert-secrets | `expert-secrets` | Bounded phase-scoped expertise: Credential/certificate inventory without disclosure, rotation, secret lifecycle, managed identity integration and Key Vault; collaborates with Identity on trust boundaries. |
| expert-messaging | `expert-messaging` | Bounded phase-scoped expertise: JMS/ActiveMQ/RabbitMQ/Kafka/MSMQ/SQS/Service Bus/Event Hubs, delivery/order/retry/idempotency and event contracts. |
| expert-storage | `expert-storage` | Bounded phase-scoped expertise: File/NAS/object/blob/S3, mounted volumes, retention, access and file semantics; not database engine behavior. |
| expert-cache | `expert-cache` | Bounded phase-scoped expertise: Redis/Memcached/NCache caching, invalidation, session state, eviction and cache transition; authoritative Redis data stays with `db-redis`. |
| expert-email | `expert-email` | Bounded phase-scoped expertise: SMTP/service delivery, templates, authentication and deliverability; distinct message acceptance/delivery evidence. |
| expert-search | `expert-search` | Bounded phase-scoped expertise: Solr/Lucene/Elasticsearch/Azure AI Search, analyzers, indexing/ranking/query contracts and rebuild/reconciliation. |
| expert-serverless | `expert-serverless` | Bounded phase-scoped expertise: Functions/Lambda source contracts, event triggers, cold starts, state and scaling; Azure Platform/Hosting provide service and topology constraints. |
| expert-realtime | `expert-realtime` | Bounded phase-scoped expertise: SignalR/WebSockets/SSE/long polling, connection state, reconnect, fan-out and client compatibility. |
| expert-reporting | `expert-reporting` | Bounded phase-scoped expertise: Crystal/SSRS/Telerik rendering, extraction, layouts and report interactions; Scheduling owns job timing and desktop experts own application flows. |
| expert-scheduling | `expert-scheduling` | Bounded phase-scoped expertise: Hangfire/Quartz/Windows Scheduler/cron/durable jobs, calendars/timezones, retry/misfire and batch timing; not reporting semantics. |
| expert-frontend-legacy | `expert-frontend-legacy` | Bounded phase-scoped expertise: jQuery/Knockout/AngularJS, DOM-driven UI, state and progressive/coexistence seams; modern framework experts own their respective implementations. |
| expert-struts | `expert-struts` | Bounded phase-scoped expertise: Actions/form beans/interceptors/validation, request flow and JSP integration. |
| expert-jsf | `expert-jsf` | Bounded phase-scoped expertise: Facelets, component lifecycle, managed beans, navigation and server-side state. |
| expert-jsp-servlet | `expert-jsp-servlet` | Bounded phase-scoped expertise: Servlet/filter lifecycle, JSP/taglibs, sessions and rendering contracts. |
| expert-jvm-builds | `expert-jvm-builds` | Bounded phase-scoped expertise: Ant/Maven/Gradle, wrappers, packaging/plugins and dependency/build graphs; not release pipeline ownership. |
| expert-platform-java | `expert-platform-java` | Bounded phase-scoped expertise: JVM generations, runtime compatibility, javax/jakarta and EE; conditional Spring/Quarkus/Micronaut platform questions. Does not absorb framework/build/ORM/testing experts. |
| expert-orm-java | `expert-orm-java` | Bounded phase-scoped expertise: Hibernate/JPA/JDBC/Spring Data/MyBatis mapping, generated queries and transaction behavior; consult engine owners. |
| expert-testing-java | `expert-testing-java` | Bounded phase-scoped expertise: JUnit/TestNG/Mockito/AssertJ, fixtures and characterization/integration test design; Validator independently judges outcomes. |
| expert-di-java | `expert-di-java` | Bounded phase-scoped expertise: Spring/Guice/CDI/Dagger scopes, lifecycle and composition roots; separate from external configuration. |
| expert-config-java | `expert-config-java` | Bounded phase-scoped expertise: XML/annotations/properties/Spring configuration, precedence, overlays and flags; separate from object lifetimes. |
| expert-security-java | `expert-security-java` | Bounded phase-scoped expertise: Spring Security/JAAS/Shiro/filter/method security and framework enforcement; Identity owns cross-system identity contracts. |
| expert-logging-java | `expert-logging-java` | Bounded phase-scoped expertise: Log4j/SLF4J/Logback/JUL appenders, structured logs and implementation compatibility; Reliability owns cross-stack SLOs and acceptance. |
| expert-webforms | `expert-webforms` | Bounded phase-scoped expertise: Page lifecycle, ViewState, controls/postbacks, session and code-behind. |
| expert-wcf | `expert-wcf` | Bounded phase-scoped expertise: WCF bindings/behaviors/contracts, hosting/security and supported coexistence. |
| expert-asmx | `expert-asmx` | Bounded phase-scoped expertise: SOAP/WSDL, XML serialization, handlers and service references. |
| expert-remoting | `expert-remoting` | Bounded phase-scoped expertise: Channels/sinks, MarshalByRefObject, lifetimes and binary/distributed-object contracts. |
| expert-aspnet-mvc | `expert-aspnet-mvc` | Bounded phase-scoped expertise: Classic ASP.NET MVC/Razor/Areas/filters/model binding/routing; collaborates with ASP.NET Core/API rather than silently equating frameworks. |
| expert-winforms | `expert-winforms` | Bounded phase-scoped expertise: Designer/events/bindings/threading, Windows/VB.NET desktop flows, printing, offline behavior and deployment/update. |
| expert-wpf | `expert-wpf` | Bounded phase-scoped expertise: XAML/visual tree/MVVM/bindings/commands, Windows/VB.NET desktop flows, accessibility and OS integration. |
| expert-silverlight | `expert-silverlight` | Bounded phase-scoped expertise: Browser-hosted constraints/XAML/RIA services and client-service coupling; no assumed web rewrite. |
| expert-dotnet-builds | `expert-dotnet-builds` | Bounded phase-scoped expertise: MSBuild/NuGet/project systems, packages.config, SDK-style transitions and compiler/build compatibility; CI/CD remains separate. |
| expert-platform-dotnet | `expert-platform-dotnet` | Bounded phase-scoped expertise: .NET Framework and modern .NET, C#/VB.NET runtime/compiler compatibility and in-place upgrades; not a replacement for framework specialists. |
| expert-orm-dotnet | `expert-orm-dotnet` | Bounded phase-scoped expertise: EF6/EF Core/NHibernate/Dapper/ADO.NET/LINQ to SQL mapping, query and transaction behavior; consult engine owners. |
| expert-testing-dotnet | `expert-testing-dotnet` | Bounded phase-scoped expertise: MSTest/xUnit/NUnit/Moq/assertions/fixtures and characterization/integration tests; Validator retains independent outcome judgment. |
| expert-di-dotnet | `expert-di-dotnet` | Bounded phase-scoped expertise: Unity/Ninject/Autofac/built-in DI, service scopes/lifetimes and composition roots. |
| expert-config-dotnet | `expert-config-dotnet` | Bounded phase-scoped expertise: web.config/app.config/appsettings, transforms, Options, precedence and runtime overlays. |
| expert-logging-dotnet | `expert-logging-dotnet` | Bounded phase-scoped expertise: NLog/Serilog/log4net/ILogger/OpenTelemetry sinks, structured logs and implementation continuity; Reliability owns cross-stack acceptance. |
| expert-serialization-dotnet | `expert-serialization-dotnet` | Bounded phase-scoped expertise: Binary/XML/Newtonsoft/System.Text.Json, stored payload/wire compatibility, defaults and converters; consult each service/framework owner. |
| expert-cobol | `expert-cobol` | Bounded phase-scoped expertise: COBOL, copybooks, JCL, CICS, VSAM, batch/online transactions and mainframe integration. Remains a distinct accountable expert, not a .NET legacy-services skill; actual operations require qualified operators and authorization. |
| expert-classic-asp | `expert-classic-asp` | Bounded phase-scoped expertise: Classic ASP/VBScript/includes/session/application state and IIS/COM dependencies; separate from ASP.NET. |
| expert-com-interop | `expert-com-interop` | Bounded phase-scoped expertise: Registration/interop assemblies/apartments/COM+/DCOM/binary contracts; collaborates with desktop, hosting and service experts. |
| expert-react | `expert-react` | Bounded phase-scoped expertise: React/Next.js components, routing/rendering/data flows and progressive transitions. |
| expert-blazor | `expert-blazor` | Bounded phase-scoped expertise: Blazor UI/render modes, component/interaction lifecycle and client/server contracts; consult .NET runtime experts. |
| expert-vue | `expert-vue` | Bounded phase-scoped expertise: Vue/Nuxt components, state, routing/rendering and compatibility. |
| expert-angular | `expert-angular` | Bounded phase-scoped expertise: Angular components/templates/DI, reactive flows, tooling and upgrade seams; distinct from AngularJS. |
| expert-svelte | `expert-svelte` | Bounded phase-scoped expertise: Svelte/SvelteKit reactivity, rendering and client/server contracts. |
| expert-astro | `expert-astro` | Bounded phase-scoped expertise: Static/hybrid rendering, islands and integration boundaries. |
| expert-nodejs-typescript | `expert-nodejs-typescript` | Bounded phase-scoped expertise: Typed APIs, Node runtime/module/package/build and dependency compatibility. |
| expert-bun | `expert-bun` | Bounded phase-scoped expertise: Bun runtime/package/test/tooling differences and Node compatibility proof; kept distinct, not assumed interchangeable. |
| expert-go | `expert-go` | Bounded phase-scoped expertise: Go services, module/build/runtime, goroutines/concurrency and interop constraints. |
| expert-rust | `expert-rust` | Bounded phase-scoped expertise: Rust services, ownership/async/build/FFI and native dependencies; distinct from Go. |
| expert-python | `expert-python` | Bounded phase-scoped expertise: Broadened Python: FastAPI/Django/Flask, WSGI/ASGI, packaging/runtime, workers/batch and concurrency. Retains FastAPI expertise without mandating it. |
| expert-aspire | `expert-aspire` | Bounded phase-scoped expertise: Aspire distributed-application modeling, orchestration, service integration and telemetry for **polyglot applications, not .NET-only applications**. Collaborates with each language/runtime expert; not automatic adoption or cloud authorization. Only aspire.dev and the official Aspire repository are authoritative. |
| expert-dapr | `expert-dapr` | Bounded phase-scoped expertise: Sidecars/components, service invocation, state/pub-sub contracts, resilience and hosting integration; separate from Aspire orchestration and mandatory adoption. |
| expert-react-native | `expert-react-native` | Bounded phase-scoped expertise: React Native device/platform interaction, native bridges, deployment and client/API contracts; distinct mobile expert, not a conditional web skill. |
| expert-flutter | `expert-flutter` | Bounded phase-scoped expertise: Dart/Flutter device/web interactions, widgets/state, platform channels and backend contracts; distinct from React Native and web frameworks. |
| product-analyst | `product-analyst` | Bounded phase-scoped expertise: Engagement framing, stakeholder discovery, problem/outcomes, business requirements and integrated PRD. Human customer owns decisions/acceptance; Documentation assists presentation; Planner owns technical delivery plan. |
| security-privacy | `security-privacy` | Bounded phase-scoped expertise: Qualified security/privacy assurance, threat/control/data-privacy evidence and planning risk. Distinct from Identity, Secrets and Rai; customer legal/compliance obligations and waivers require authorized human owners, never invented certification. |
| expert-aspnet-core-api | `expert-aspnet-core-api` | Bounded phase-scoped expertise: Modern ASP.NET Core/API endpoints, middleware, routing, binding, hosting integration and API compatibility. Distinct from classic ASP.NET MVC and general .NET runtime knowledge. |
| azure-platform | `azure-platform` | Bounded phase-scoped expertise: Azure landing zones, subscription/management boundaries, policy, governance, region/service fit, network/DNS, quotas, resource organization and pricing/sizing assumptions. Not Hosting's application topology or CI/CD's delivery automation. |
| reliability | `reliability` | Bounded phase-scoped expertise: Cross-stack reliability, performance, observability, load/capacity, availability/recovery, SLOs and measurement methods. Language-specific logging experts own implementation; Validator independently verifies outcomes. |
| accessibility-ux | `accessibility-ux` | Bounded phase-scoped expertise: Web, desktop and mobile accessibility/UX assurance: keyboard/focus, assistive technology, interaction flows and regression criteria. Framework/client experts implement changes; does not duplicate their ownership. |
| data-transition | `data-transition` | Bounded phase-scoped expertise: Cross-engine reconciliation, authoritative write ownership, CDC/backfill, rehearsal, cutover and rollback/reverse-data assurance. Planner owns plan integration; DB/application experts supply semantics; Validator independently verifies. |

## Phase routing and admission

| Admitted phase | Accountable route | Required boundary |
|---|---|---|
| Static assessment | `assessor` | Facts and gaps; no scripts, builds or option selection |
| Requirements and human decisions | `product-analyst` | Confirmed vs assumed requirements and named decision owners |
| Six-R comparison | `classifier` | Evidence-backed options; human selects destination/option |
| Central coordinated proposal | `planner` | Shared contracts, dependency DAG, approved per-repository scope |
| Approved issue preparation | `planner` with `documentation` | Inert payload only; central CAO declared safe output publishes |
| One-target implementation | `execution-orchestrator` then exactly one matching domain owner | CAO admission, current approved issue/plan, dependency proof, explicit scope/mode |
| Independent artifact/diff review | `reviewer` | Exact inputs/revision; no author/executor self-review |
| Acceptance and cross-repository verification | `validator` | Independently observed proof within admitted scope; no new target discovery |
| Evidence-linked documentation | `documentation` | Approved artifact paths and no independent-acceptance claim |

Select one accountable specialist for each task using the exact domain charter.
Pass task/charter digests, source/artifact revisions, approved paths and commands,
dependency evidence, invariants, expected outputs and stop conditions.
Bounded consultations do not authorize a full-team fan-out.

The worker's execution-orchestrator route is enabled only for the already
admitted target; it never discovers repositories, creates issues or dispatches
additional workers. Unsatisfied dependencies or stale approvals block that route,
not trigger fallback execution in another repository.

Support identities are not specialist routing-table destinations. Scribe merges
accepted decisions only through permitted runtime state tools; Ralph reports
supplied status only on explicit bounded request; Rai and Fact Checker provide
requested consultation or a selected gate. No automatic support gates,
ceremonies, scans, watch loops, issue claiming, retrospective dispatch or merge.

Stop after returning the requested artifacts/evidence and declared draft-PR
safe-output payload. No direct Git publication, generic Issue Source, PRD Mode,
cross-Squad dispatch or auto-init. Report unsupported runtime conflicts.
