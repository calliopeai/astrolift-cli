# astrolift-prereqs

Cluster prerequisites for installing Astrolift on a vanilla Kubernetes cluster.

This is an umbrella chart: each prerequisite component is an optional subchart
toggled by `.Values.<component>.enabled`. Operators install only the pieces
their cluster needs.

## What this chart manages

**Glue** (this chart's own templates):
- StorageClass tier mapping per spec 22 — `astrolift-standard`,
  `astrolift-balanced` (default), `astrolift-high_iops`, `astrolift-extreme`,
  `astrolift-rwx`. Provisioner defaults to Longhorn; switch via values.
- ClusterIssuer for cert-manager (ACME / Let's Encrypt by default).

**Subcharts** (toggleable; added in follow-up commits):

| Component | Subchart | Default |
|---|---|---|
| cert-manager | `jetstack/cert-manager` | off |
| external-dns | `bitnami/external-dns` | off |
| nginx ingress | `ingress-nginx/ingress-nginx` | off |
| MetalLB (bare metal LB) | `metallb/metallb` | off |
| Longhorn (block + RWX) | `longhorn/longhorn` | off |
| Rook Ceph (block + filesystem) | `rook-release/rook-ceph` | off |
| CloudNativePG (Postgres operator) | `cnpg/cloudnative-pg` | off |
| Strimzi Kafka | `strimzi/strimzi-kafka-operator` | off |
| Bitnami Redis Operator | `bitnami/redis-operator` | off |
| HashiCorp Vault | `hashicorp/vault` | off |
| Velero (cluster backup) | `vmware-tanzu/velero` | off |
| OpenTelemetry collector | `open-telemetry/opentelemetry-collector` | off |
| kube-prometheus-stack | `prometheus-community/kube-prometheus-stack` | off |
| Loki | `grafana/loki` | off |
| Tempo | `grafana/tempo` | off |

## Usage

```bash
# Update vendored chart deps (subcharts) — no-op until deps are added in
# follow-up commits.
helm dependency update ./helm/astrolift-prereqs

# Install with the operator's per-cluster values.
helm install astrolift-prereqs ./helm/astrolift-prereqs -f my-values.yaml

# Then install the platform itself on top.
helm install astrolift ./helm/astrolift -f ./helm/astrolift/values.k8s.yaml
```

## Configuration

See `values.yaml` for the full toggle matrix and per-prereq config sections.

For local kind / dev, prefer `kubernetes/base/install.sh` (script-driven path
optimised for sub-5-minute spin-up). The Helm chart is for production +
customer-operator installs where reproducibility and GitOps matter.
