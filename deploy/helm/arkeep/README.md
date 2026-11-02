# Arkeep Helm chart

Deploys the [Arkeep](https://arkeep.io) server: the web interface, the REST API and the gRPC endpoint
agents connect to. Agents run on the machines you back up, inside or outside the cluster.

## Install

The chart is published as an OCI artifact on GitHub Container Registry and Docker Hub, with the same
version as the Arkeep release it deploys:

```bash
helm install arkeep oci://ghcr.io/arkeep-io/charts/arkeep --version X.Y.Z -f my-values.yaml
# or: oci://registry-1.docker.io/arkeepio/arkeep
```

A minimal `my-values.yaml`:

```yaml
config:
  baseURL: https://arkeep.example.com

secret:
  secretKey: "<openssl rand -hex 32>"
  agentSecret: "<openssl rand -hex 24>"
```

`secret.secretKey` encrypts every stored credential and must never change once set.

Keep `replicaCount: 1`: the server keeps the registry of connected agents in memory.

## Documentation

Ingress, exposing the gRPC port, your own TLS certificate, PostgreSQL and every value are described in
[Install on Kubernetes with Helm](https://arkeep.io/docs/installation/kubernetes/).
