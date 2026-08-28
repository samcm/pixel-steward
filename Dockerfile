FROM golang:1.25.0-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pixel-steward ./cmd/pixel-steward

FROM node:24.7.0-bookworm-slim
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates openssh-client libvirt-clients \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global opencode-ai@1.18.25 \
    && npm cache clean --force \
    && useradd --create-home --uid 10001 steward \
    && install -d -o steward -g steward /var/lib/pixel-steward
COPY --from=builder /out/pixel-steward /usr/local/bin/pixel-steward
USER steward
WORKDIR /var/lib/pixel-steward
ENTRYPOINT ["pixel-steward"]
CMD ["serve", "--config", "/etc/pixel-steward/config.yaml"]
