# syntax=docker/dockerfile:1.7
################################################################################
# Stage 1: build
################################################################################
FROM docker.io/library/golang:1.22.8-alpine3.20 AS builder

# Alpine's build-base pulls in gcc/musl-dev; we don't need a C toolchain since
# CGO is disabled below, so keep the builder image itself minimal.
RUN apk add --no-cache ca-certificates=20240705-r0

WORKDIR /src

# Cache module downloads in their own layer, independent of source changes.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY app ./app
COPY cmd ./cmd
COPY ui ./ui

# CGO_ENABLED=0 -> static binary, no dynamic linker needed in the final image.
# -trimpath strips local filesystem paths from the binary.
# -ldflags="-s -w" strips debug symbols and the DWARF table (smaller binary,
# nothing for a decompiler to lean on).
# GOFLAGS=-mod=readonly fails the build instead of silently touching go.sum.
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOFLAGS=-mod=readonly \
    GOTOOLCHAIN=local
RUN go build -trimpath -ldflags="-s -w" -o /out/bin/coralline ./cmd/coralline

# Pre-create the runtime data directory owned by uid/gid 65532 so it can be
# copied verbatim into the distroless stage, which has no shell/mkdir/chown
# to do this itself. Coralline's own config defaults are all relative
# "./data/..." paths (blobs, index.db, admin credential, TLS cert/key) — so
# WORKDIR below is /coralline, letting those defaults resolve to a single
# /coralline/data instead of a doubled-up /data/data. The binary itself is
# built to /out/bin above and lands at /usr/local/bin/coralline at runtime,
# not /coralline, so it doesn't collide with the /coralline directory.
RUN mkdir -p /out/data && chown -R 65532:65532 /out/data

# distroless/static's uid 65532 is named "nonroot" in its baked-in /etc/passwd.
# Replace it with a "coralline" account at the same uid/gid so `USER coralline`
# works and `podman exec`/logs read cleanly, without changing the actual
# privilege level (still uid 65532, still no shell, still can't self-elevate).
RUN printf 'coralline:x:65532:65532:coralline:/coralline:/sbin/nologin\n' > /out/passwd && \
    printf 'coralline:x:65532:\n' > /out/group

################################################################################
# Stage 2: runtime
################################################################################
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# OCI image metadata (org.opencontainers.image.* — see https://github.com/opencontainers/image-spec/blob/main/annotations.md)
LABEL org.opencontainers.image.title="coralline" \
      org.opencontainers.image.description="Self-hosted Terraform/OpenTofu module registry" \
      org.opencontainers.image.source="https://github.com/Zehnyth/coralline" \
      org.opencontainers.image.licenses="LICENSE"

WORKDIR /coralline

COPY --from=builder /out/passwd /etc/passwd
COPY --from=builder /out/group /etc/group
COPY --from=builder --chown=65532:65532 /out/bin/coralline /usr/local/bin/coralline
COPY --from=builder --chown=65532:65532 /out/data /coralline/data

# uid/gid 65532 — same non-root identity distroless ships as "nonroot", just
# renamed via the /etc/passwd swap above.
USER coralline:coralline

# Public registry listener only. The admin API binds 127.0.0.1:8444 by design
# (see AGENTS.md) and must never be published — deliberately not EXPOSEd here.
EXPOSE 8443


ENTRYPOINT ["/usr/local/bin/coralline"]
CMD ["serve"]
