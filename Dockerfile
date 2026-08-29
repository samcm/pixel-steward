FROM node:24.7.0-bookworm-slim AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN rm -rf /internal/api/dist && npm run build

FROM golang:1.25.0-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /internal/api/dist ./internal/api/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pixel-steward ./cmd/pixel-steward

FROM nousresearch/hermes-agent@sha256:d64f4e9aba92884fff3d5020c02a75676066f237622d0776759ca1437b9b0517
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates openssh-client libvirt-clients \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global opencode-ai@1.18.25 \
    && npm cache clean --force \
    && useradd --create-home --uid 10001 steward \
    && install -d -o steward -g steward /var/lib/pixel-steward
COPY --from=builder /out/pixel-steward /usr/local/bin/pixel-steward
COPY --chmod=0755 scripts/pixel-steward-hermes /usr/local/bin/pixel-steward-hermes
USER steward
WORKDIR /var/lib/pixel-steward
ENTRYPOINT ["pixel-steward"]
CMD ["serve", "--config", "/etc/pixel-steward/config.yaml"]
