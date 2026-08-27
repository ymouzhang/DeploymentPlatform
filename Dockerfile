# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:24-alpine AS web-builder

WORKDIR /src/web
RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack pnpm install --frozen-lockfile

COPY web/ ./
RUN corepack pnpm exec vitest run \
    && corepack pnpm build

FROM --platform=$BUILDPLATFORM golang:1.26.1-alpine AS backend-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-builder /src/webui/dist ./webui/dist

RUN go test ./... \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/dp \
        ./cmd/dp

FROM --platform=$TARGETPLATFORM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 dp \
    && adduser -S -D -H -u 10001 -G dp dp

WORKDIR /app
COPY --from=backend-builder /out/dp /app/dp

RUN mkdir -p /app/data \
    && chown -R dp:dp /app

USER dp
EXPOSE 30199
VOLUME ["/app/data"]

ENTRYPOINT ["/app/dp"]
