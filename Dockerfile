# Used by GoReleaser. Built binaries are dropped into the build
# context as `astro_<version>_<os>_<arch>/astro`. GoReleaser
# substitutes COPY paths via build args.

FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETOS
ARG TARGETARCH

LABEL org.opencontainers.image.title="astro"
LABEL org.opencontainers.image.description="Astrolift CLI"
LABEL org.opencontainers.image.source="https://github.com/calliopeai/astrolift-cli"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY astro /usr/local/bin/astro

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/astro"]
