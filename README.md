# Forkspacer

A cloud-native Kubernetes operator for dynamic workspace lifecycle management, enabling teams to create, fork, hibernate, and manage ephemeral development environments at scale.

## Overview

Forkspacer provides a declarative approach to managing isolated, multi-application environments within Kubernetes clusters. It addresses the challenges of resource optimization, environment reproducibility, and cost management in modern development workflows.

### Core Capabilities

**Dynamic Environment Provisioning**
- Create isolated workspaces with complete application stacks
- Fork existing environments for parallel development workflows
- Provision dedicated clusters for pull request validation
- Support for both ephemeral and persistent workspace patterns

**Intelligent Resource Management**
- Automated hibernation and wake-up cycles based on usage patterns
- Scheduled sleep/wake operations for cost optimization
- Resource scaling and right-sizing based on workload demands
- Multi-tenant resource isolation and governance

**Reproducible Development Environments**
- Infrastructure-as-Code approach to environment definition
- Version-controlled workspace templates and configurations
- Consistent environments across development, staging, and production
- Support for complex, multi-service application topologies

## Architecture

The operator introduces two primary Custom Resource Definitions (CRDs):

- **Workspace**: Defines an isolated environment boundary with lifecycle policies
- **Module**: Represents deployable application components within workspaces

## Getting Started

### Prerequisites

- Kubernetes cluster (v1.20+)
- kubectl CLI tool

### Installation

```bash
# Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-cainjector -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-webhook -n cert-manager

# Install Forkspacer using Helm
kubectl apply -f https://raw.githubusercontent.com/forkspacer/forkspacer/$(curl -s https://api.github.com/repos/forkspacer/forkspacer/tags | grep -m 1 '"name"' | cut -d'"' -f4)/dist/install.yaml
```

### Helm Chart Installation

Forkspacer can also be installed using Helm charts for more flexible deployments:

```bash
# Add Forkspacer Helm repository
helm repo add forkspacer https://forkspacer.github.io/forkspacer
helm repo update

# Install operator only (minimal)
helm install forkspacer forkspacer/forkspacer \
  --namespace forkspacer-system \
  --create-namespace

# Install with web UI and API server enabled
helm install forkspacer forkspacer/forkspacer \
  --set operatorUI.enabled=true \
  --set apiServer.enabled=true \
  --namespace forkspacer-system \
  --create-namespace
```

#### Accessing the Web Interface

```bash
# Port-forward to access the web UI locally
kubectl port-forward svc/forkspacer-operator-ui 3000:80 -n forkspacer-system

# Access API
kubectl port-forward svc/forkspacer-api-server 8421:8080 -n forkspacer-system

# Visit: http://localhost:3000
```

#### Chart Components

The Forkspacer Helm chart includes:

- **Operator**: Core Kubernetes controller (always enabled)
- **API Server**: REST API for programmatic access (optional, auto-enabled with UI)
- **Operator UI**: Web interface for managing workspaces (optional)

For detailed deployment guides and configuration options, see [Helm Usage Guide](helm/HELM_USAGE.md).

## Version Management

Forkspacer provides automated tools to update component versions easily. This is useful when new versions of operator-ui or api-server are released.

### Update Component Versions

**Update operator-ui version:**
```bash
make update-operator-ui-version VERSION=v0.1.2
```

**Update api-server version:**
```bash
make update-api-server-version VERSION=v0.1.1
```

**Update forkspacer operator version:**
```bash
make update-forkspacer-version VERSION=v0.1.6
```

**Update multiple components:**
```bash
# Update all components at once
make update-versions FORKSPACER_VERSION=v0.1.6 UI_VERSION=v0.1.2 API_VERSION=v0.1.1

# Update specific components
make update-versions UI_VERSION=v0.1.2 API_VERSION=v0.1.1
```

### What Gets Updated

These commands automatically update:
- **Global image tags** in `helm/values.yaml`
- **Dependency versions** in `helm/Chart.yaml`
- **Subchart versions** in `helm/charts/*/Chart.yaml`
- **Main chart version** (for forkspacer operator updates)
- **Makefile VERSION variable** (for forkspacer operator updates)

This ensures all component versions stay in sync across the Helm chart.

### Release Workflow

1. **Component repositories** release new versions (e.g., operator-ui v0.1.2, api-server v0.1.1)
2. **Update main chart** using the commands above:
   ```bash
   # Update all components to latest versions
   make update-versions FORKSPACER_VERSION=v0.1.6 UI_VERSION=v0.1.2 API_VERSION=v0.1.1
   ```
3. **Test locally**: `helm template ./helm --set operatorUI.enabled=true`
4. **Deploy updated chart**: `helm upgrade forkspacer ./helm`

### Basic Usage

#### Creating a Workspace

Define a workspace with hibernation policies:

```yaml
apiVersion: batch.forkspacer.com/v1
kind: Workspace
metadata:
  name: feature-branch-env
  namespace: development
spec:
  type: kubernetes
  connection:
    type: in-cluster
  autoHibernation:
    enabled: true
    schedule: "0 18 * * *"      # Sleep at 6 PM daily
    wakeSchedule: "0 8 * * *"    # Wake at 8 AM daily
```

#### Deploying Applications

Deploy services within the workspace:

```yaml
apiVersion: batch.forkspacer.com/v1
kind: Module
metadata:
  name: redis
  namespace: default
spec:
  workspace:
    name: feature-branch-env
    namespace: development

  source:
    raw:
      kind: Helm
      metadata:
        name: redis
        version: "1.0.0"
        supportedOperatorVersion: ">= 0.0.0, < 1.0.0"

      spec:
        namespace: default
        repo: https://charts.bitnami.com/bitnami
        chartName: redis
        version: "21.2.9"
        values:
          - raw:
              replica:
                replicaCount: 0
              image:
                repository: bitnamilegacy/redis
              global:
                security:
                  allowInsecureImages: true
```

#### Managing Workspace Lifecycle

```bash
# List all workspaces
kubectl get workspaces -A

# Check workspace status
kubectl describe workspace feature-branch-env -n development

# Manual hibernation
kubectl patch workspace feature-branch-env -n development \
  -p '{"spec":{"hibernated":true}}' --type=merge

# Wake up workspace
kubectl patch workspace feature-branch-env -n development \
  -p '{"spec":{"hibernated":false}}' --type=merge
```

Get more information about usage of the forkspacer at [https://forkspacer.com/guides/quick-start/](https://forkspacer.com/guides/quick-start/)

## Development

You can find Development instructions of the forkspacer at [https://forkspacer.com/development/overview](https://forkspacer.com/development/overview)

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Community

- **Documentation**: [forkspacer.com](https://forkspacer.com)
- **Issues**: [GitHub Issues](https://github.com/forkspacer/forkspacer/issues)