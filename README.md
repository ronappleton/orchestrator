# orchestrator
[![CI](https://github.com/ronappleton/orchestrator/actions/workflows/ci.yml/badge.svg)](https://github.com/ronappleton/orchestrator/actions/workflows/ci.yml) [![CodeQL](https://github.com/ronappleton/orchestrator/actions/workflows/codeql.yml/badge.svg)](https://github.com/ronappleton/orchestrator/actions/workflows/codeql.yml) [![Scorecard](https://github.com/ronappleton/orchestrator/actions/workflows/scorecard.yml/badge.svg)](https://github.com/ronappleton/orchestrator/actions/workflows/scorecard.yml) ![Coverage](https://raw.githubusercontent.com/ronappleton/orchestrator/main/docs/badges/coverage.svg)

Workflow engine for multi-step jobs, retries, approvals, and state transitions.

## Status
- Skeleton created; API and implementation TBD.

## Running
```bash
go build -o bin/orchestrator ./cmd/orchestrator
./bin/orchestrator -config config.yaml
```
