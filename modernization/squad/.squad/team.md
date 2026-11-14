# Squad Team

> CAO modernization: 85 actual specialists + four canonical support members,
> imported from pinned `octodemo/modernization-squad`; see [provenance](../SOURCE.md).

## Coordinator

| Name | Role | Notes |
|------|------|-------|
| Squad | Native Coordinator | [.github/agents/squad.agent.md](../.github/agents/squad.agent.md), invoked with [CAO mission](mission.md), [governance](governance.md) and [runtime limitations](framework-compatibility.md). No generic PRD automation, monitoring, publication or merge. |

## Members

| Name | Role | Charter | Status |
|------|------|---------|--------|
| assessor | Read-only static estate inventory and evidence gaps. No recommendations, fabricated metrics/estimates, mandatory build or execution. | [charter](agents/assessor/charter.md) | Active |
| classifier | Six-R comparison, feasibility/trade-offs and sourced cost assumptions. User selects; does not execute or author the selected plan. | [charter](agents/classifier/charter.md) | Active |
| planner | Selected-path plan, cross-repository dependencies, measurable phase criteria, integrated data/cutover/rollback plan and permissions. Does not authorize execution. | [charter](agents/planner/charter.md) | Active |
| execution-orchestrator | Bounded authorized execution through qualified experts; dependency order, scope and evidence. Separate from Squad routing, review, validation and human authorization. | [charter](agents/execution-orchestrator/charter.md) | Active |
| reviewer | Independent revision-bound artifact/diff quality verdict; neither implements reviewed work nor grants operational permission. | [charter](agents/reviewer/charter.md) | Active |
| validator | Pre-implementation acceptance-design/testability/evidence-sufficiency consultation; later independently measured outcomes, characterization/data/cutover/rollback verification under separate authorization. Planned tests are not results; never self-certifies its own contribution or execution. | [charter](agents/validator/charter.md) | Active |
| documentation | On-demand evidence-linked playbooks, technical writing, runbooks and diagrams. Phase owners retain technical decisions and phase accountability. | [charter](agents/documentation/charter.md) | Active |
| db-sqlserver | T-SQL, procedures, SQL Agent, CLR, collations, transactions, HA/DR and Azure SQL/managed variants. | [charter](agents/db-sqlserver/charter.md) | Active |
| db-postgresql | Extensions, PL/pgSQL, MVCC, indexing/partitioning, logical replication and Azure offering constraints. | [charter](agents/db-postgresql/charter.md) | Active |
| db-oracle | PL/SQL, data types, optimizer, tablespaces, RAC/Data Guard, security and licensing constraints. | [charter](agents/db-oracle/charter.md) | Active |
| db-mysql | MySQL/MariaDB differences, storage engines, routines, replication and query semantics. | [charter](agents/db-mysql/charter.md) | Active |
| db-mongodb | BSON, document models, aggregation, indexing/sharding, change streams and compatibility limits. | [charter](agents/db-mongodb/charter.md) | Active |
| db-cosmosdb | API selection, partition keys, RU/cost modeling, consistency, multi-region behavior and SDK contracts. | [charter](agents/db-cosmosdb/charter.md) | Active |
| db-db2 | Mainframe/distributed Db2 dialects, packages/host variables, utilities and replication; qualified operators required for actual operations. | [charter](agents/db-db2/charter.md) | Active |
| db-cassandra | CQL, partition/clustering design, consistency, repair/compaction and compatible-target limitations. | [charter](agents/db-cassandra/charter.md) | Active |
| db-dynamodb | Key/index design, single-table patterns, conditional writes, Streams/TTL and transition from an AWS source. | [charter](agents/db-dynamodb/charter.md) | Active |
| db-redis | Authoritative primary-store structures, persistence, transactions/Lua, clustering and Streams; cache-only concerns belong to Cache. | [charter](agents/db-redis/charter.md) | Active |
| db-couchbase | Buckets/scopes/collections, KV/sub-documents, N1QL, indexing, eventing and XDCR. | [charter](agents/db-couchbase/charter.md) | Active |
| db-sybase | SAP ASE dialect, procedures/tempdb, locking, replication and migration compatibility. | [charter](agents/db-sybase/charter.md) | Active |
| expert-auth-identity | LDAP/AD/Entra, OAuth/OIDC/SAML, federation, claims, user/service identity and authorization flows; not credential lifecycle ownership. | [charter](agents/expert-auth-identity/charter.md) | Active |
| expert-hosting | IIS, Windows Services, Tomcat/JBoss/WebLogic, VMs, Docker/Kubernetes/AKS, topology and runtime lifecycle/coexistence. Consumes Azure platform guardrails; does not own pipelines or select targets by preference. | [charter](agents/expert-hosting/charter.md) | Active |
| expert-cicd | GitHub Actions, Azure DevOps, Jenkins, artifact provenance, promotion, release gates and delivery/rollback automation. Does not own platform policy, runtime topology or grant provisioning/publication/cutover permission. | [charter](agents/expert-cicd/charter.md) | Active |
| expert-secrets | Credential/certificate inventory without disclosure, rotation, secret lifecycle, managed identity integration and Key Vault; collaborates with Identity on trust boundaries. | [charter](agents/expert-secrets/charter.md) | Active |
| expert-messaging | JMS/ActiveMQ/RabbitMQ/Kafka/MSMQ/SQS/Service Bus/Event Hubs, delivery/order/retry/idempotency and event contracts. | [charter](agents/expert-messaging/charter.md) | Active |
| expert-storage | File/NAS/object/blob/S3, mounted volumes, retention, access and file semantics; not database engine behavior. | [charter](agents/expert-storage/charter.md) | Active |
| expert-cache | Redis/Memcached/NCache caching, invalidation, session state, eviction and cache transition; authoritative Redis data stays with `db-redis`. | [charter](agents/expert-cache/charter.md) | Active |
| expert-email | SMTP/service delivery, templates, authentication and deliverability; distinct message acceptance/delivery evidence. | [charter](agents/expert-email/charter.md) | Active |
| expert-search | Solr/Lucene/Elasticsearch/Azure AI Search, analyzers, indexing/ranking/query contracts and rebuild/reconciliation. | [charter](agents/expert-search/charter.md) | Active |
| expert-serverless | Functions/Lambda source contracts, event triggers, cold starts, state and scaling; Azure Platform/Hosting provide service and topology constraints. | [charter](agents/expert-serverless/charter.md) | Active |
| expert-realtime | SignalR/WebSockets/SSE/long polling, connection state, reconnect, fan-out and client compatibility. | [charter](agents/expert-realtime/charter.md) | Active |
| expert-reporting | Crystal/SSRS/Telerik rendering, extraction, layouts and report interactions; Scheduling owns job timing and desktop experts own application flows. | [charter](agents/expert-reporting/charter.md) | Active |
| expert-scheduling | Hangfire/Quartz/Windows Scheduler/cron/durable jobs, calendars/timezones, retry/misfire and batch timing; not reporting semantics. | [charter](agents/expert-scheduling/charter.md) | Active |
| expert-frontend-legacy | jQuery/Knockout/AngularJS, DOM-driven UI, state and progressive/coexistence seams; modern framework experts own their respective implementations. | [charter](agents/expert-frontend-legacy/charter.md) | Active |
| expert-struts | Actions/form beans/interceptors/validation, request flow and JSP integration. | [charter](agents/expert-struts/charter.md) | Active |
| expert-jsf | Facelets, component lifecycle, managed beans, navigation and server-side state. | [charter](agents/expert-jsf/charter.md) | Active |
| expert-jsp-servlet | Servlet/filter lifecycle, JSP/taglibs, sessions and rendering contracts. | [charter](agents/expert-jsp-servlet/charter.md) | Active |
| expert-jvm-builds | Ant/Maven/Gradle, wrappers, packaging/plugins and dependency/build graphs; not release pipeline ownership. | [charter](agents/expert-jvm-builds/charter.md) | Active |
| expert-platform-java | JVM generations, runtime compatibility, javax/jakarta and EE; conditional Spring/Quarkus/Micronaut platform questions. Does not absorb framework/build/ORM/testing experts. | [charter](agents/expert-platform-java/charter.md) | Active |
| expert-orm-java | Hibernate/JPA/JDBC/Spring Data/MyBatis mapping, generated queries and transaction behavior; consult engine owners. | [charter](agents/expert-orm-java/charter.md) | Active |
| expert-testing-java | JUnit/TestNG/Mockito/AssertJ, fixtures and characterization/integration test design; Validator independently judges outcomes. | [charter](agents/expert-testing-java/charter.md) | Active |
| expert-di-java | Spring/Guice/CDI/Dagger scopes, lifecycle and composition roots; separate from external configuration. | [charter](agents/expert-di-java/charter.md) | Active |
| expert-config-java | XML/annotations/properties/Spring configuration, precedence, overlays and flags; separate from object lifetimes. | [charter](agents/expert-config-java/charter.md) | Active |
| expert-security-java | Spring Security/JAAS/Shiro/filter/method security and framework enforcement; Identity owns cross-system identity contracts. | [charter](agents/expert-security-java/charter.md) | Active |
| expert-logging-java | Log4j/SLF4J/Logback/JUL appenders, structured logs and implementation compatibility; Reliability owns cross-stack SLOs and acceptance. | [charter](agents/expert-logging-java/charter.md) | Active |
| expert-webforms | Page lifecycle, ViewState, controls/postbacks, session and code-behind. | [charter](agents/expert-webforms/charter.md) | Active |
| expert-wcf | WCF bindings/behaviors/contracts, hosting/security and supported coexistence. | [charter](agents/expert-wcf/charter.md) | Active |
| expert-asmx | SOAP/WSDL, XML serialization, handlers and service references. | [charter](agents/expert-asmx/charter.md) | Active |
| expert-remoting | Channels/sinks, MarshalByRefObject, lifetimes and binary/distributed-object contracts. | [charter](agents/expert-remoting/charter.md) | Active |
| expert-aspnet-mvc | Classic ASP.NET MVC/Razor/Areas/filters/model binding/routing; collaborates with ASP.NET Core/API rather than silently equating frameworks. | [charter](agents/expert-aspnet-mvc/charter.md) | Active |
| expert-winforms | Designer/events/bindings/threading, Windows/VB.NET desktop flows, printing, offline behavior and deployment/update. | [charter](agents/expert-winforms/charter.md) | Active |
| expert-wpf | XAML/visual tree/MVVM/bindings/commands, Windows/VB.NET desktop flows, accessibility and OS integration. | [charter](agents/expert-wpf/charter.md) | Active |
| expert-silverlight | Browser-hosted constraints/XAML/RIA services and client-service coupling; no assumed web rewrite. | [charter](agents/expert-silverlight/charter.md) | Active |
| expert-dotnet-builds | MSBuild/NuGet/project systems, packages.config, SDK-style transitions and compiler/build compatibility; CI/CD remains separate. | [charter](agents/expert-dotnet-builds/charter.md) | Active |
| expert-platform-dotnet | .NET Framework and modern .NET, C#/VB.NET runtime/compiler compatibility and in-place upgrades; not a replacement for framework specialists. | [charter](agents/expert-platform-dotnet/charter.md) | Active |
| expert-orm-dotnet | EF6/EF Core/NHibernate/Dapper/ADO.NET/LINQ to SQL mapping, query and transaction behavior; consult engine owners. | [charter](agents/expert-orm-dotnet/charter.md) | Active |
| expert-testing-dotnet | MSTest/xUnit/NUnit/Moq/assertions/fixtures and characterization/integration tests; Validator retains independent outcome judgment. | [charter](agents/expert-testing-dotnet/charter.md) | Active |
| expert-di-dotnet | Unity/Ninject/Autofac/built-in DI, service scopes/lifetimes and composition roots. | [charter](agents/expert-di-dotnet/charter.md) | Active |
| expert-config-dotnet | web.config/app.config/appsettings, transforms, Options, precedence and runtime overlays. | [charter](agents/expert-config-dotnet/charter.md) | Active |
| expert-logging-dotnet | NLog/Serilog/log4net/ILogger/OpenTelemetry sinks, structured logs and implementation continuity; Reliability owns cross-stack acceptance. | [charter](agents/expert-logging-dotnet/charter.md) | Active |
| expert-serialization-dotnet | Binary/XML/Newtonsoft/System.Text.Json, stored payload/wire compatibility, defaults and converters; consult each service/framework owner. | [charter](agents/expert-serialization-dotnet/charter.md) | Active |
| expert-cobol | COBOL, copybooks, JCL, CICS, VSAM, batch/online transactions and mainframe integration. Remains a distinct accountable expert, not a .NET legacy-services skill; actual operations require qualified operators and authorization. | [charter](agents/expert-cobol/charter.md) | Active |
| expert-classic-asp | Classic ASP/VBScript/includes/session/application state and IIS/COM dependencies; separate from ASP.NET. | [charter](agents/expert-classic-asp/charter.md) | Active |
| expert-com-interop | Registration/interop assemblies/apartments/COM+/DCOM/binary contracts; collaborates with desktop, hosting and service experts. | [charter](agents/expert-com-interop/charter.md) | Active |
| expert-react | React/Next.js components, routing/rendering/data flows and progressive transitions. | [charter](agents/expert-react/charter.md) | Active |
| expert-blazor | Blazor UI/render modes, component/interaction lifecycle and client/server contracts; consult .NET runtime experts. | [charter](agents/expert-blazor/charter.md) | Active |
| expert-vue | Vue/Nuxt components, state, routing/rendering and compatibility. | [charter](agents/expert-vue/charter.md) | Active |
| expert-angular | Angular components/templates/DI, reactive flows, tooling and upgrade seams; distinct from AngularJS. | [charter](agents/expert-angular/charter.md) | Active |
| expert-svelte | Svelte/SvelteKit reactivity, rendering and client/server contracts. | [charter](agents/expert-svelte/charter.md) | Active |
| expert-astro | Static/hybrid rendering, islands and integration boundaries. | [charter](agents/expert-astro/charter.md) | Active |
| expert-nodejs-typescript | Typed APIs, Node runtime/module/package/build and dependency compatibility. | [charter](agents/expert-nodejs-typescript/charter.md) | Active |
| expert-bun | Bun runtime/package/test/tooling differences and Node compatibility proof; kept distinct, not assumed interchangeable. | [charter](agents/expert-bun/charter.md) | Active |
| expert-go | Go services, module/build/runtime, goroutines/concurrency and interop constraints. | [charter](agents/expert-go/charter.md) | Active |
| expert-rust | Rust services, ownership/async/build/FFI and native dependencies; distinct from Go. | [charter](agents/expert-rust/charter.md) | Active |
| expert-python | Broadened Python: FastAPI/Django/Flask, WSGI/ASGI, packaging/runtime, workers/batch and concurrency. Retains FastAPI expertise without mandating it. | [charter](agents/expert-python/charter.md) | Active |
| expert-aspire | Aspire distributed-application modeling, orchestration, service integration and telemetry for **polyglot applications, not .NET-only applications**. Collaborates with each language/runtime expert; not automatic adoption or cloud authorization. Only aspire.dev and the official Aspire repository are authoritative. | [charter](agents/expert-aspire/charter.md) | Active |
| expert-dapr | Sidecars/components, service invocation, state/pub-sub contracts, resilience and hosting integration; separate from Aspire orchestration and mandatory adoption. | [charter](agents/expert-dapr/charter.md) | Active |
| expert-react-native | React Native device/platform interaction, native bridges, deployment and client/API contracts; distinct mobile expert, not a conditional web skill. | [charter](agents/expert-react-native/charter.md) | Active |
| expert-flutter | Dart/Flutter device/web interactions, widgets/state, platform channels and backend contracts; distinct from React Native and web frameworks. | [charter](agents/expert-flutter/charter.md) | Active |
| product-analyst | Engagement framing, stakeholder discovery, problem/outcomes, business requirements and integrated PRD. Human customer owns decisions/acceptance; Documentation assists presentation; Planner owns technical delivery plan. | [charter](agents/product-analyst/charter.md) | Active |
| security-privacy | Qualified security/privacy assurance, threat/control/data-privacy evidence and planning risk. Distinct from Identity, Secrets and Rai; customer legal/compliance obligations and waivers require authorized human owners, never invented certification. | [charter](agents/security-privacy/charter.md) | Active |
| expert-aspnet-core-api | Modern ASP.NET Core/API endpoints, middleware, routing, binding, hosting integration and API compatibility. Distinct from classic ASP.NET MVC and general .NET runtime knowledge. | [charter](agents/expert-aspnet-core-api/charter.md) | Active |
| azure-platform | Azure landing zones, subscription/management boundaries, policy, governance, region/service fit, network/DNS, quotas, resource organization and pricing/sizing assumptions. Not Hosting's application topology or CI/CD's delivery automation. | [charter](agents/azure-platform/charter.md) | Active |
| reliability | Cross-stack reliability, performance, observability, load/capacity, availability/recovery, SLOs and measurement methods. Language-specific logging experts own implementation; Validator independently verifies outcomes. | [charter](agents/reliability/charter.md) | Active |
| accessibility-ux | Web, desktop and mobile accessibility/UX assurance: keyboard/focus, assistive technology, interaction flows and regression criteria. Framework/client experts implement changes; does not duplicate their ownership. | [charter](agents/accessibility-ux/charter.md) | Active |
| data-transition | Cross-engine reconciliation, authoritative write ownership, CDC/backfill, rehearsal, cutover and rollback/reverse-data assurance. Planner owns plan integration; DB/application experts supply semantics; Validator independently verifies. | [charter](agents/data-transition/charter.md) | Active |
| Scribe | Decision Merger | [charter](agents/scribe/charter.md) | Active |
| Ralph | Work Monitor | [charter](agents/ralph/charter.md) | Active |
| Rai | RAI Reviewer | [charter](agents/rai/charter.md) | Active |
| Fact Checker | Devil's Advocate & Verification Agent | [charter](agents/fact-checker/charter.md) | Active |

## Project Context

- All 89 identities and descriptive casting names are retained; source casting
  registry/history/commit marker remain byte-for-byte coherent. Casting history
  is identity provenance only, not engagement or customer history.
- Active membership means resolvable expertise, not worker activation or
  authority. Demand-load only the qualified specialists needed for an admitted
  task, never the full roster.
- Assessment -> central coordinated proposal -> approved per-repository issues
  -> dependency-gated one-target implementation draft PRs -> cross-repository
  verification. See [process](process.md), [roles](roles.md) and
  [contracts](contracts.md).
- Support automation is disabled. Execution Orchestrator sequences expertise
  only inside the admitted target; CAO retains fleet dispatch. Humans retain
  merge and operational decisions.
- [Routing](routing.md), the actual charters and preserved casting files resolve
  capability. The adapted native coordinator reads this actual roster directly;
  its stale source zero-member generated bootstrap summary was removed.
