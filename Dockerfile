# syntax=docker/dockerfile:1

ARG COMMIT_HASH=unknown
ARG COMMIT_TIME=
ARG COMMIT_SUBJECT_B64=
ARG BUILD_TIME=
ARG VERSION=dev

# Frontend build stage
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
# The web build imports @stockbot/shared, which resolves to /src/shared next
# to this WORKDIR. Copy it before the production bundle.
COPY shared/ /src/shared/
COPY frontend/ ./
RUN npm run build

# Go build stage
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/frontend/dist/ ./internal/webui/assets/
RUN mkdir -p /data
# Redeclared so this stage inherits the global build-args. Docker exports
# those args into this shell, which expands ${COMMIT_*}, ${BUILD_TIME}, and
# ${VERSION}. $$COMMIT_HASH must not be used: $$ is the shell's PID, so the
# binary is stamped with 1COMMIT_HASH. The subject arrives base64-encoded so
# quotes and spaces never enter this line.
ARG COMMIT_HASH
ARG COMMIT_TIME
ARG COMMIT_SUBJECT_B64
ARG BUILD_TIME
ARG VERSION
RUN case "${COMMIT_HASH}" in *COMMIT_HASH*) echo "COMMIT_HASH was not substituted" >&2; exit 1 ;; esac; \
    case "${COMMIT_TIME}" in *COMMIT_TIME*) echo "COMMIT_TIME was not substituted" >&2; exit 1 ;; esac; \
    case "${BUILD_TIME}" in *BUILD_TIME*) echo "BUILD_TIME was not substituted" >&2; exit 1 ;; esac; \
    case "${VERSION}" in *VERSION*) echo "VERSION was not substituted" >&2; exit 1 ;; esac; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X github.com/Rhionin/stockbot/internal/buildinfo.Commit=${COMMIT_HASH} -X github.com/Rhionin/stockbot/internal/buildinfo.CommittedAt=${COMMIT_TIME} -X github.com/Rhionin/stockbot/internal/buildinfo.BuiltAt=${BUILD_TIME} -X github.com/Rhionin/stockbot/internal/buildinfo.Version=${VERSION} -X github.com/Rhionin/stockbot/internal/buildinfo.subjectStamp=${COMMIT_SUBJECT_B64}" \
    -o /out/stockbot-server ./cmd/server && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X github.com/Rhionin/stockbot/internal/buildinfo.Commit=${COMMIT_HASH} -X github.com/Rhionin/stockbot/internal/buildinfo.CommittedAt=${COMMIT_TIME} -X github.com/Rhionin/stockbot/internal/buildinfo.BuiltAt=${BUILD_TIME} -X github.com/Rhionin/stockbot/internal/buildinfo.Version=${VERSION} -X github.com/Rhionin/stockbot/internal/buildinfo.subjectStamp=${COMMIT_SUBJECT_B64}" \
    -o /out/stockbot-backup ./cmd/backup

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
ARG COMMIT_HASH
LABEL org.opencontainers.image.revision="${COMMIT_HASH}"
LABEL com.rhionin.stockbot.commit="${COMMIT_HASH}"
COPY --from=build /out/stockbot-server /usr/local/bin/stockbot-server
COPY --from=build /out/stockbot-backup /usr/local/bin/stockbot-backup
COPY --from=build --chown=nonroot:nonroot /data /data
ENV ADDR=":8080" DB_PATH="/data/pantry.db"
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/stockbot-server"]