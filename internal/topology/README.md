# Topology solver

This Go package resolves CAO deployment requirements offline and generates
review-only Azure deployment bundles. It also supplies the dependency engine
and embedded provider contract used by native Azure preflight, planning and
guarded development apply.

The solver uses **Puccini** to validate and resolve a bounded, generated
**TOSCA Simple Profile 1.3** template, then **Gonum** to produce a deterministic
dependency-first order and reject cycles. It is not a general-purpose TOSCA
orchestrator, an ARM scheduler, or a source of deployment authority.

For operator instructions and the requirements schema, see the
[deployment bundle guide](../../docs/deployment-topology.md). The
[architecture](../../ARCHITECTURE.md) describes the surrounding CLI and
deployment boundaries.

## CLI usage

```sh
gh cao deploy init --deployment azure-functions --out /private/operator/requirements.yaml
# Edit the sample's identities, feature choices and Key Vault references.
gh cao deploy generate --config /private/operator/requirements.yaml \
  --out /private/operator/azure-plan
gh cao deploy check /private/operator/azure-plan
```

The output's parent directory must already exist. Initialization and generation
refuse to overwrite existing destinations. Generated files are owner-only.
Samples contain synthetic identities and documentation-only networks; replace
them before any separately approved deployment.

Generation requires Microsoft's standalone `bicep` executable on `PATH`.
`BicepCLI` compiles every emitted Bicep template with
`bicep build --no-restore --stdout`, surfacing compiler and linter diagnostics.
There is no automatic tool download or module restore. Initialization and
offline bundle checking do not require Bicep, Node.js or Azure access.

## Supported profiles

Only the `azure` backend and requirements version `1` are implemented.

| Deployment | Hosting and database | Feature scope |
| --- | --- | --- |
| `single-vm` | Hosted Go and local PostgreSQL on one persistent VM | Public OAuth and Actions snapshots |
| `minimal-azure` | Hosted Go VM and private Azure PostgreSQL | Public OAuth and Actions snapshots |
| `azure-functions` | Functions baseline and private Azure PostgreSQL | Actions snapshots or collector; public or private ingress; optional ADX, OTLP, session data lake and webhook gateway |
| `azure-container-apps` | Container Apps Consumption and private Azure PostgreSQL | Public OAuth and Actions snapshots; staged runtime activation |

`full-azure` is an initialization sample selecting `azure-functions` with
additional features, not another `features.deployment` value. Unsupported
combinations fail rather than silently adding services or changing profiles.

## Resolution pipeline and code map

1. **Parse requirements** (`model.go`): `Parse` applies feature defaults and
   rejects unknown fields, invalid types, nulls, aliases, multiple YAML documents
   and input larger than 1 MiB.
2. **Validate the provider contract** (`solve.go`, `compact.go`,
   `container_apps.go`, `azure.go`): `Solve` checks feature compatibility,
   parameters, OAuth allowlists, explicit retention and Key Vault references
   against the selected embedded ARM parameter definitions.
3. **Resolve dependencies** (`tosca.go`, `graph.go`): generate typed nodes and
   `DependsOn` requirements, validate/compile/resolve/coerce them through
   Puccini, project the resolved relationships into `Topology`, then order
   and verify the exact component, dependency and node-kind contract.
4. **Review declared architecture** (`rules.go`): `ReviewRules` produces strict
   errors and advisory warnings. Bundle generation rejects strict errors;
   findings describe declared configuration, not observed readiness.
5. **Build and check the bundle** (`bundle.go`, `bicep.go`, `diagrams.go`):
   render embedded templates and runbooks, validate Bicep in a private staging
   directory, bind outputs with SHA-256 hashes, and check the resulting bundle.

The main package entry points are:

| API | Purpose |
| --- | --- |
| `ExampleFor`, `InitExample` | Read or exclusively write a shipped requirements sample |
| `Parse`, `Solve` | Convert YAML requirements into validated topology, TOSCA and Azure parameters |
| `Generate` | Generate a checked bundle; requires a non-nil `BicepValidator` |
| `Check` | Re-resolve requirements and verify bundle contents, hashes and dependency order; reject drift, orphaned outputs and symlinks |
| `CheckTopology` | Validate a topology projection against its selected feature contract |
| `ResolveAzure`, `AzureTemplate` | Share dependency resolution and the embedded Functions ARM baseline with native deployment |

Bundles include `requirements.yaml`, `parameters.json`, `topology.yaml`,
`topology.json`, `manifest.json`, architecture diagrams, rule reports, Bicep
templates, operator runbooks and validation/what-if scripts. Optional feature
infrastructure is included only when selected.

## Safety boundaries

Requirements contain non-secret parameters and existing Key Vault secret
**names/references**, never credential values. The package does not acquire
credentials, contact Azure, deploy resources or grant access.

Only generated, import-free templates enter Puccini. Arbitrary TOSCA imports,
CSARs, lifecycle operations and user scriptlets are not supported. External
prerequisites remain explicitly unverified: dependency order does not prove
vault access, network connectivity, successful provisioning, runtime activation,
backups or compliance.

YAML bundles are not apply plan files. Native `deploy azure` remains limited to
public Functions with Actions snapshots and optional OTLP; other generated
profiles do not widen its apply support or bypass its approval and drift gates.

## Maintenance and validation

Run from the repository root:

```sh
go generate ./internal/topology
go test ./internal/topology
```

Regenerate assets after changing the authoritative Azure baseline under
`server/azure/` or the compact/Container Apps template contracts. The generator
copies the baseline into `assets/azure-baseline.generated/` and compiles the
compact and Container Apps ARM parameter contracts. Do not hand-edit these
generated snapshots; regeneration requires the official Bicep CLI on `PATH`.

Tests cover engine constraints, dependency contracts, fail-closed requirements,
architecture rules, deterministic bundles, drift/symlink rejection, embedded
baseline parity and compiler behavior. `TestDeploymentSamples` compiles all five
shipped samples with the actual Bicep compiler; missing Bicep is a failure, not
passing evidence.
