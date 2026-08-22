# The default image an Astrolift pipeline job runs in.
#
# Bundled with the CLI on purpose: the job image's whole reason to exist is
# to carry `astro`, so building it from a different pipeline would let the
# two versions drift. Both images come out of one build here, tagged with
# the same version, so `astrolift-builder:X` always carries `astro` X.
#
# Two paths feed this file and neither may fork it (a second, near-identical
# Dockerfile is how toolchain layers silently diverge):
#
#   main branch      -> ASTRO_STAGE=astro-src, astro compiled from this tree
#   tagged release   -> ASTRO_STAGE=astro-dist, goreleaser's prebuilt binary
#                       lands in the build context as ./astro
#
# The consumer's constraints, from the Job spec Astrolift renders
# (astrolift_workflows/activities/pipeline_job_spawn.py):
#
#   command: ["/bin/sh", "-c", script]   -> a shell is mandatory, which is
#                                           why the distroless CLI image
#                                           cannot serve as a job image
#   runAsNonRoot: true                   -> the image must declare a
#                                           non-root USER or the kubelet
#                                           refuses to start the pod
#   allowPrivilegeEscalation: false,
#   privileged: false, no docker socket  -> see "no docker" below
#
# No docker: `docker build` needs a daemon or a privileged builder, and the
# Job spec grants neither. Shipping the docker CLI would look like support
# and deliver an error at runtime, so it is deliberately absent, and
# astrolift/docker-build is unusable in a cluster-run job until the platform
# offers a rootless builder. git, kubectl and astro all work unprivileged.

ARG ASTRO_STAGE=astro-src

FROM golang:1.23-alpine AS astro-src

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build \
        -ldflags="-s -w -X github.com/calliopeai/astrolift-cli/cmd.Version=${VERSION}" \
        -o /out/astro \
        ./main.go

# Release path: goreleaser drops the already-built binary in the context.
FROM scratch AS astro-dist
COPY astro /out/astro

# Resolves to one of the two above.
FROM ${ASTRO_STAGE} AS astro

FROM debian:12-slim

# Debian rather than Alpine: this image runs the operator's own build steps,
# and a musl base turns any prebuilt glibc wheel or node binary they fetch
# into a confusing runtime error.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        openssh-client \
        tar \
        unzip \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

# kubectl, pinned and checksum-verified against the sidecar file upstream
# publishes for exactly this purpose. Track the cluster fleet's minor
# version; skew of +/-1 minor is supported.
ARG KUBECTL_VERSION=v1.31.4
ARG TARGETARCH
RUN set -eu; \
    arch="${TARGETARCH:-amd64}"; \
    base="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}"; \
    curl -fsSL -o /tmp/kubectl "${base}/kubectl"; \
    curl -fsSL -o /tmp/kubectl.sha256 "${base}/kubectl.sha256"; \
    echo "$(cat /tmp/kubectl.sha256)  /tmp/kubectl" | sha256sum -c -; \
    install -m 0755 /tmp/kubectl /usr/local/bin/kubectl; \
    rm -f /tmp/kubectl /tmp/kubectl.sha256

COPY --from=astro /out/astro /usr/local/bin/astro

# 65532 matches the distroless "nonroot" uid the CLI image already uses, so
# a volume written by one image is readable by the other. HOME must exist
# and be writable: git refuses to run without a usable HOME, and the job
# script's first action is usually a clone.
RUN groupadd --gid 65532 nonroot \
    && useradd --uid 65532 --gid 65532 --create-home --home-dir /home/nonroot nonroot \
    && mkdir -p /workspace \
    && chown 65532:65532 /workspace

LABEL org.opencontainers.image.title="astrolift-builder"
LABEL org.opencontainers.image.description="Default image for Astrolift pipeline jobs: shell, git, kubectl and astro"
LABEL org.opencontainers.image.source="https://github.com/calliopeai/astrolift-cli"
LABEL org.opencontainers.image.licenses="MIT"

ENV HOME=/home/nonroot
WORKDIR /workspace
USER 65532:65532

# No ENTRYPOINT: Astrolift sets the container command to
# ["/bin/sh", "-c", <the job's rendered step script>]. An entrypoint here
# would prepend to that and break every job.
CMD ["/bin/sh"]
