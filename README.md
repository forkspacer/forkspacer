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