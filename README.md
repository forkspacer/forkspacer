## Overview
Forkspacer is an `open-source` tool that lets you create, `fork`, and hibernate `entire Kubernetes` or VM-based environments.

Developers can `clone full setups`, test changes in isolation, and automatically hibernate idle workspaces to save resources—all declaratively, with GitOps-style reproducibility. 

***Perfect for spinning dev, test, pre-prod, prod environments and teams where each developer needs a personal, forked environment from a shared baseline.***

## Installation

```bash
# Install prerequisites (cert-manager)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-cainjector -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-webhook -n cert-manager

# Add Forkspacer Helm repository
helm repo add forkspacer https://forkspacer.github.io/forkspacer
helm repo update forkspacer

# Install operator only (minimal)
helm install forkspacer forkspacer/forkspacer \
  --set operator-ui.enabled=true \
  --set api-server.enabled=true \
  --namespace forkspacer-system \
  --create-namespace
```

## Accessing the Web Interface
```bash
kubectl port-forward svc/operator-ui 3000:80 -n forkspacer-system
```
Visit: http://localhost:3000

## Basic Usage

#### Creating a Workspace

Create your first baseline workspace

CLI:
```bash
forkspacer workspace create baseline
```

YAML:
```yaml
apiVersion: batch.forkspacer.com/v1
kind: Workspace
metadata:
  name: baseline
  namespace: default
spec:
  type: kubernetes
  connection:
    type: in-cluster
```

#### Adopt existing helm chart

This is an example where we suppose to have existing Redis cluster that is already installed in your Kubernetes. Forkspacer supports two methods: 1) Adopting existing deployment OR 2) installing a deployment direcly into workspace without pre-existing helm-release or app. For option 2 and more you should look into our detailed documentation @ https://forkspacer.com

CLI:
```bash
forkspacer import
```

YAML:
```yaml
apiVersion: batch.forkspacer.com/v1
kind: Module
metadata:
  name: redis
  namespace: default

spec:
  helm:
    chart:
      repo:
        url: https://charts.bitnami.com/bitnami
        chart: redis
        version: "21.2.9"

    existingRelease:
      name: my-redis    # name of the installed redis helm release 
      namespace: redis  # namespace where redis is deployed

    namespace: default

  workspace:
    name: baseline
```

#### Creating a Fork

Create forked workspace from a baseline workspace (simple fork)

CLI:
```bash
forkspacer workspace create my-first-fork --from=baseline
```

YAML:
```yaml
apiVersion: batch.forkspacer.com/v1
kind: Workspace
metadata:
  name: my-first-fork
  namespace: default
spec:
  from:
    name: baseline
    namespace: default
```

## Development

Get more information about usage of the forkspacer at [https://forkspacer.com/guides/quick-start/](https://forkspacer.com/guides/quick-start/)
You can find Development instructions of the forkspacer at [https://forkspacer.com/development/overview](https://forkspacer.com/development/overview)

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Community

- **Documentation**: [forkspacer.com](https://forkspacer.com)
- **Issues**: [GitHub Issues](https://github.com/forkspacer/forkspacer/issues)
