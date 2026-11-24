## Summary

<!-- Briefly describe what changed and why. -->

## Linked Issues

<!-- e.g. Closes #123 (or N/A) -->

## Type of Change

- [ ] Bug fix (`fix`)
- [ ] New feature (`feat`)
- [ ] Refactor / performance (`refactor` / `perf`)
- [ ] Tests (`test`)
- [ ] Documentation / build / CI (`docs` / `build` / `ci` / `chore`)
- [ ] Breaking change (explain impact below)

## Verification

<!-- Summarize the checks/tests you ran locally. -->

- [ ] `npm --prefix frontend run check && npm --prefix frontend run test && npm --prefix frontend run build`
- [ ] `pytest -v worker/tests && diff -u worker/params.py examples/demo_project/utils/params.py`
- [ ] `go vet ./... && go vet -tags server ./... && go test -race -count=1 ./...`
- [ ] `go test -tags e2e -v -count=1 ./tests/e2e/...` *(when touching IPC, server, storage, watcher, security, or packaging)*

## Checklist

- [ ] PR title follows Conventional Commits (`<type>(<scope>): <subject>`)
- [ ] `CHANGELOG.md` updated under `## [Unreleased]` *(if user-visible)*
- [ ] Documentation updated *(if contracts, CLI/env vars, or behavior changed)*
- [ ] No Wails imports added to `internal/server` and no security invariants weakened
