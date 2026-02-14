FROM golang:1.25-alpine AS build

WORKDIR /src/orchestrator

COPY orchestrator/go.mod orchestrator/go.sum ./
COPY approval-service/go.mod ../approval-service/go.mod
COPY policy-service/go.mod ../policy-service/go.mod
COPY pkg/natsbus/go.mod ../pkg/natsbus/go.mod
COPY pkg/servicediscovery/go.mod ../pkg/servicediscovery/go.mod
COPY audit-log/go.mod ../audit-log/go.mod
COPY memarch/go.mod ../memarch/go.mod
COPY notification-service/go.mod ../notification-service/go.mod
COPY workspace-service/go.mod ../workspace-service/go.mod
COPY pkg/natsbus/ ../pkg/natsbus/
COPY pkg/servicediscovery/ ../pkg/servicediscovery/

RUN apk add --no-cache git openssh ca-certificates
RUN mkdir -p -m 0700 /root/.ssh
RUN ssh-keyscan github.com >> /root/.ssh/known_hosts
RUN git config --global url."ssh://git@github.com/".insteadOf "https://github.com/"
RUN --mount=type=ssh go mod download

COPY orchestrator/ ./
COPY approval-service/ ../approval-service/
COPY policy-service/ ../policy-service/
COPY audit-log/ ../audit-log/
COPY memarch/ ../memarch/
COPY notification-service/ ../notification-service/
COPY workspace-service/ ../workspace-service/

RUN go build -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/orchestrator /app/orchestrator
COPY orchestrator/config.yaml /app/config.yaml
EXPOSE 8100 9114
ENTRYPOINT ["/app/orchestrator", "--config", "/app/config.yaml"]
