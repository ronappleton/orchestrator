# orchestrator

Workflow engine for multi-step jobs, retries, approvals, and state transitions.

## API

### Create workflow
`POST /v1/workflows`
```json
{
  "name": "demo",
  "description": "Simple demo workflow",
  "steps": [
    {
      "name": "call ai-router",
      "action": "http",
      "input": {
        "method": "POST",
        "url": "http://ai-router:8080/v1/chat/completions",
        "headers": {"Content-Type": "application/json"},
        "body": {
          "model": "llama2",
          "project_id": "default",
          "messages": [{"role": "user", "content": "hello"}]
        }
      }
    }
  ]
}
```

### Start run
`POST /v1/runs`
```json
{
  "workflow_id": "wf_...",
  "context": {"project_id": "default"}
}
```

### Approve / cancel / status
- `POST /v1/runs/{id}/approve`
- `POST /v1/runs/{id}/cancel`
- `GET /v1/runs/{id}`

### Templates
- `GET /v1/templates`
- `POST /v1/templates`

## Step Actions
Currently supported actions (all map to HTTP calls):
- `http`
- `ai_router.chat`
- `web.search`
- `web.extract`
- `memarch.store_fact`
- `memarch.search`
- `scm.call`
- `transform`
- `condition`

## Retries
Each step may include:
```json
"retry": {"max": 2, "backoff_ms": 500}
```

## Validation
HTTP steps require `http/https` URLs. Other schema validation is minimal by design.

## JSON Schema

Default schema lives at `schema/workflow.schema.json`.
To override:
```yaml
policy:
  workflow_schema: "/path/to/your/schema.json"
```

Step schema example:
- `schema/steps.schema.json`

## Template Pack

Generic template pack lives at:
- `templates/templates.json`

## Versioning + Rollback

Endpoints:
- `GET /v1/workflows/{id}/versions`
- `POST /v1/workflows/{id}/rollback`

Payload:
```json
{"version": 1}
```

## Run Logs

- `GET /v1/runs/{id}/logs`
- `POST /v1/runs/{id}/logs` (raw text body)
- `GET /v1/runs/{id}/logs/stream` (SSE)

## Persistence
If `database.dsn` is set, workflows and runs are stored in Postgres.

Example DSN:
```
postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable
```

## Event Hooks
When `memarch.base_url` or `audit_log.base_url` are set, the orchestrator emits
run/step events to those endpoints.

## Running
```bash
go build -o bin/orchestrator ./cmd/orchestrator
./bin/orchestrator -config config.yaml
```
