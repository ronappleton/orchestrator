# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build

WORKDIR /src/orchestrator

COPY orchestrator/go.mod orchestrator/go.sum ./
COPY approval-service/go.mod ../approval-service/go.mod
COPY policy-service/go.mod ../policy-service/go.mod
COPY event-bus/go.mod ../event-bus/go.mod
COPY event-bus ../event-bus

RUN --mount=type=ssh /bin/sh -c "apk add --no-cache git openssh ca-certificates && \
    mkdir -p -m 0700 /root/.ssh && \
    ssh-keyscan github.com >> /root/.ssh/known_hosts && \
    git config --global url.\"ssh://git@github.com/\".insteadOf \"https://github.com/\" && \
    git config --global url.\"git@github.com:\".insteadOf \"https://github.com/\" && \
    go mod download"

COPY orchestrator/ ./
COPY approval-service/ ../approval-service/
COPY policy-service/ ../policy-service/

RUN go build -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/orchestrator /app/orchestrator
COPY orchestrator/config.yaml /app/config.yaml
EXPOSE 8100 9114
ENTRYPOINT ["/app/orchestrator", "--config", "/app/config.yaml"]
