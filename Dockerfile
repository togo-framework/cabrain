# CaBrain — single-binary deploy (API + built SPA), for stack_stacknet.
# Build context = repo root (monorepo; the plugins/ replace dirs must be present).

# --- web build ---------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci || npm install
COPY web/ ./
RUN npm run build            # → /web/dist

# --- go build ----------------------------------------------------------------
FROM golang:1.26 AS api
WORKDIR /src
# Copy the whole monorepo so require+replace (./plugins/brain, ./plugins/brain-tei) resolve.
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-mod=mod go build -o /out/cabrain ./cmd/api

# --- runtime -----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=api /out/cabrain /app/cabrain
COPY --from=web /web/dist   /app/web/dist
ENV ADDR=:8080 WEB_DIST=/app/web/dist
EXPOSE 8080
# DATABASE_URL / TEI_* / COGNEE_* / COLD_STORE_* injected at runtime from the stack .env.
ENTRYPOINT ["/app/cabrain"]
