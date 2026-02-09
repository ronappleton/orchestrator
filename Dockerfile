# syntax=docker/dockerfile:1.6
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod .
 RUN --mount=type=ssh go mod download
COPY . .
RUN go build -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/orchestrator /app/orchestrator
COPY config.yaml /app/config.yaml
EXPOSE 8100
ENTRYPOINT ["/app/orchestrator", "-config", "/app/config.yaml"]
