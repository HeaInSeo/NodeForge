# NodeForge — Claude Code Guidelines

## 1. Responsibility boundary (immutable)

**NodeForge owns**: BuildRequest reception (gRPC server), builder Job orchestration,
DockGuard policy bundle management (`PolicyService`: .rego → opa build → .wasm → gRPC),
internal registry integration, `RegisteredToolDefinition` creation and CAS storage,
L3 kind dry-run (`ValidateService`), L4 smoke run (`ValidateService`),
and `ToolRegistryService`.

**NodeKit owns**: authoring UX, L1 static validation, `WasmPolicyChecker` execution,
`BuildRequest` generation, `AdminToolList` display, and all admin-side UI semantics.

**builder workload owns**: actual image building inside the cluster Job.
NodeForge orchestrates the Job but does not build images directly.

Do not implement authoring UI or L1 validation in NodeForge.
Do not build images directly in NodeForge — delegate to builder Job.

## 2. Key term boundaries (immutable)

| Term | Meaning |
|------|---------|
| `BuildRequest` | What NodeKit sends over gRPC. Input to NodeForge. |
| `RegisteredToolDefinition` | Post-L4 confirmed object. CAS-stored by NodeForge. SHA256 hash = filename. |
| `builder workload` | Cluster-internal Job that runs the actual image build. Not part of NodeForge binary. |
| `AdminToolList` | NodeKit's admin view — NodeForge does NOT own or render this. |

Do not create `ToolDefinition` objects in NodeForge — that is NodeKit's draft model.
`RegisteredToolDefinition` is the only post-registration object NodeForge produces.

## 3. Package structure

```
cmd/controlplane   — gRPC server entrypoint
pkg/policy         — PolicyService: .rego management, opa build, GetPolicyBundle() RPC
pkg/build          — BuildService: builder Job orchestration, status watch, log collection
pkg/registry       — internal registry integration (push verification, digest acquisition)
pkg/validate       — ValidateService: kind dry-run (L3), smoke run (L4)
pkg/catalog        — ToolRegistryService: RegisteredToolDefinition CAS storage
```

Do not cross package boundaries in the wrong direction (e.g., `catalog` importing `build`).

## 4. Orchestration loop — the critical path

L2's core challenge is the orchestration loop reliability, not the builder technology choice:

```
Job 생성 → Running → 이미지 빌드 → 내부 레지스트리 push → Succeeded → digest 확보 → 후속 단계
```

**Phase 2 gate**: builder Job happy-path must succeed once end-to-end before implementing
`RegisteredToolDefinition` creation, manifest generation, dry-run, or any UI work.
Do not proceed to detail work if the loop has not closed.

## 5. kubeconfig / K8s API access

NodeForge accesses K8s via local kubeconfig (`~/.kube/config`). No Ingress or service mesh
in the sprint scope. Do not design for in-cluster service account auth yet — that is roadmap.

## 6. Decision checklist before every change

- Does it add builder Job image-build logic into the NodeForge binary? **Block — delegate to builder workload.**
- Does it add authoring UI or L1 validation logic? **Block — that is NodeKit.**
- Does it touch the gRPC proto contract? **Requires coordination with NodeKit.**
- Does it add `ToolDefinition` (NodeKit draft model) to NodeForge? **Block.**
- Does it proceed to RegisteredToolDefinition/manifest/dry-run before the orchestration loop gate passes? **Block.**

## 7. Small diffs; no unrelated refactors

Each commit must have a single, stated purpose. Do not clean up surrounding code,
add comments to unchanged lines, or refactor while fixing a bug.

## 8. Warning policy

Maintain a **zero-warning baseline** with golangci-lint (`make lint`).
Run `make lint` after every change. Do not introduce new lint warnings.
Generated `.pb.go` files are excluded from lint (see `.golangci.yml`).

## 9. Validation responsibility

| Change type | Expected validation |
|---|---|
| New gRPC RPC | Unit test for handler + integration test with NodeKit |
| BuildService change | Orchestration loop test (Job create → status → push → digest) |
| PolicyService change | .rego load + `opa build` + `GetPolicyBundle()` RPC test |
| ValidateService change | kind dry-run / smoke run with local kubeconfig |
| CAS storage change | Hash consistency test (same content → same hash) |
| Refactor | Existing tests green; add tests if coverage was absent |

## 10. Completion reporting

A task is not complete until the following are stated explicitly:

- **What changed**: files and logic affected
- **Validation run**: which tests, lint checks, or manual verifications were performed
- **Results**: pass/fail counts, lint result, any regressions
- **Remaining risks**: known unknowns, deferred items, or assumptions not verified

## 11. Hidden failure mode review

Before marking a change complete, explicitly check for:

- builder Job created but status watch not started (fire-and-forget without loop closing)
- Job Succeeded but digest extraction from registry response fails silently
- `opa build` subprocess fails without error propagation to gRPC response
- CAS file written with wrong hash (content mismatch after read-back)
- dry-run returns success on schema error due to misparse of API server response
- K8s watch connection drops mid-Job without reconnect logic
