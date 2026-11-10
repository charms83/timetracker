# Self-contained Image für Docker Compose (Kontext = Repository-Wurzel).
# OpenShift nutzt weiter server-go/Containerfile (UBI, ohne Frontend-Build).
ARG GO_IMAGE=docker.io/library/golang:1.27-bookworm
ARG NODE_IMAGE=docker.io/library/node:22-bookworm-slim

FROM ${NODE_IMAGE} AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
# Ausgabe nach /out/web (Vite base ./ für Server-Auslieferung)
RUN npm run build && mkdir -p /out && cp -R dist /out/web

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY server-go/go.mod server-go/go.sum ./
RUN go mod download
COPY server-go/cmd ./cmd
COPY server-go/internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/server ./cmd/server

FROM docker.io/library/debian:bookworm-slim
RUN useradd --system --uid 1001 --no-create-home --shell /usr/sbin/nologin zeitprotokoll \
 && mkdir -p /usr/share/zeitprotokoll/web \
 && chown -R zeitprotokoll:zeitprotokoll /usr/share/zeitprotokoll
COPY --from=build /out/server /usr/local/bin/zeitprotokoll-server
COPY --from=web /out/web /usr/share/zeitprotokoll/web
USER 1001
ENV ZP_ADDR=:8080 ZP_PRODUCTION=1 ZP_STATIC_DIR=/usr/share/zeitprotokoll/web
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zeitprotokoll-server"]
CMD ["serve"]
