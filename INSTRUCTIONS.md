<!-- managed-by: commons-control; do not edit distributed copies -->

# AI Commons engineering instructions

This is the operating agreement for managers, key staff, and engineers working
across AI Commons repositories. The canonical file is maintained in
`grasp-labs/commons-control`.

## Direction

- AI Commons is a decentralized platform with declared contracts.
- `commons-control` is the control plane for topology, contracts, shared
  content, documentation, and safe platform-wide operations.
- `daas-service` is the legacy v1 monolith and is being deprecated. Do not add
  features. Only migration, security, and critical fixes are appropriate.
- The declared 12 services, four compute blocks, IdP, GitHub integration, and
  20 tools are defined under `platform/`.

## Source ownership

- Service code and service-specific documentation remain authoritative in the
  owning repository.
- Cross-platform declarations and managed public content are authoritative in
  `commons-control`.
- `library/`, `platform/snapshot/`, generated distributions, plans, and lock
  files are generated evidence. Do not edit them directly.
- Change generated output by changing its source and running the owning `aic`
  command.

## Contracts

- Every HTTP API publishes a machine-readable contract. New APIs use OpenAPI 3.
- Events, tools, payloads, IAM actions, and compute-block boundaries are
  contracts, not implementation details.
- Breaking changes require coordinated producer, consumer, examples, version,
  and migration changes.
- Run `aic platform validate`, `aic platform diff`, and `aic specs check`
  before review.
- Unknown or duplicated contract literals are defects. Move shared facts to
  their declared source.

## Documentation

- Keep architecture, contracts, whitepapers, lifecycle, process-flow, and
  operator documents with the code they describe.
- `documents.yaml` selects reviewed documents into the central `library/`.
- Run `aic docs check` before review and `aic docs collect` when selected source
  documents change.
- Do not commit generated API documentation as human-authored guidance.
- Do not duplicate public Commons book, course, playbook, or skill sources.

## Authentication and secrets

- Sign in with `aic login --env dev|prod`. The proven `ds-fetch-py-sdk` flow
  provides browser SSO, PKCE, password/MFA, refresh, and a secure shared cache.
- Never put tokens, passwords, client secrets, tenant data, or private content
  in plans, logs, examples, generated files, or documentation.
- Public writes require a platform-tenant identity.

## Remote changes

- Remote graph and content changes always follow validate, plan, review, apply.
- Apply must reject changed sources, plan tampering, stale ETags, remote drift,
  expired credentials, and the wrong environment.
- Production requires explicit `--confirm prod`.
- Deletion requires an explicit prune plan and `--confirm prune`.
- Apply journals are resumable records, not a place for credentials or content
  secrets.

## Generated output

- Generation must be deterministic: stable ordering, normalized newlines,
  canonical JSON, and no random identifiers or timestamps in content.
- Generated changes must be visible as ordinary reviewable diffs.
- Run `aic eval`; update `generated.lock.json` only after reviewing the intended
  output change.
- LLM evaluation may advise editorial quality but must not generate normative
  contracts or make release decisions.

## Repository expectations

Every maintained repository must:

1. Keep its build, tests, contracts, and relevant documentation runnable.
2. Avoid changing dirty or unrelated worktrees during automation.
3. Use fast-forward-only workspace updates.
4. Add tests for behavior, failure paths, drift, and safety boundaries.
5. Keep errors concise and avoid printing server-controlled terminal escapes.
6. Treat warnings about missing contracts, unmanaged drift, and legacy
   dependencies as work to resolve.
7. Carry the distributed `INSTRUCTIONS.md` unchanged. Update it from
   `commons-control`, never locally.

## Required checks

For `commons-control`:

```bash
aic doctor
make check
aic docs check
```

For other repositories, run their native formatter, unit tests, contract
checks, and build. A green local check does not authorize production or public
writes.

## Review and management

- Review source and generated diffs together.
- Prefer small coordinated changes over platform-wide hidden coupling.
- Record migrations and deprecations explicitly.
- Managers approve direction and risk; contracts and automated checks enforce
  the approved behavior.
- Security, tenant isolation, data visibility, and rollback are release
  criteria.
