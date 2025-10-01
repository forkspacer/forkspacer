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
- cert-manager for webhook certificates
- kubectl CLI tool

### Installation

Deploy the operator and its dependencies:

```bash
# Install cert-manager
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml

# Wait for cert-manager to be ready
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-cainjector -n cert-manager
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-webhook -n cert-manager

# Deploy Forkspacer
kubectl apply -f https://raw.githubusercontent.com/forkspacer/forkspacer/main/dist/install.yaml
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
    wakeSchedule: "0 8 * * *"   # Wake at 8 AM daily
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
  source:
    raw:
      kind: Helm
      metadata:
        name: redis
        version: "1.0.0"
        supportedOperatorVersion: ">= 0.0.0, < 1.0.0"
        author: "platform-team"
        description: "Redis in-memory data store"
        category: "database"

      config:
        - type: option
          name: "Redis Version"
          alias: "version"
          spec:
            editable: true
            required: false
            default: "21.2.9"
            values:
              - "21.2.9"
              - "21.2.7"
              - "21.2.6"

      spec:
        namespace: default
        repo: https://charts.bitnami.com/bitnami
        chartName: redis
        version: "{{.config.version}}"

        values:
          - file: https://ftp.aykhans.me/web/client/pubshares/hB6VSdCnBCr8gFPeiMuCji/browse?path=/values.yaml
          - raw:
              replica:
                replicaCount: "{{.config.replicaCount}}"

        outputs:
          - name: "Redis Hostname"
            value: "redis-master.default"
          - name: "Redis Username"
            value: "default"
          - name: "Redis Password"
            valueFrom:
              secret:
                name: "{{.releaseName}}"
                key: redis-password
                namespace: default
          - name: "Redis Port"
            value: 6379

        cleanup:
          removeNamespace: false
          removePVCs: true

  workspace:
    name: default
    namespace: default

  config:
    version: 21.2.7

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

# Fork an existing workspace
kubectl get workspace production-env -o yaml | \
  sed 's/name: production-env/name: testing-env/' | \
  kubectl apply -f -
```

## Use Cases

### Pull Request Environments
Create isolated environments for each pull request with automatic cleanup:

```yaml
apiVersion: batch.forkspacer.com/v1
kind: Workspace
metadata:
  name: pr-1234
  labels:
    forkspacer.com/type: "ephemeral"
    forkspacer.com/pr: "1234"
spec:
  type: kubernetes
  autoHibernation:
    enabled: true
    ttl: "7d"  # Auto-delete after 7 days
```

### Developer Workspaces
Provide consistent, on-demand development environments:

```yaml
apiVersion: batch.forkspacer.com/v1
kind: Workspace
metadata:
  name: dev-alice
  labels:
    forkspacer.com/owner: "alice"
    forkspacer.com/type: "development"
spec:
  type: kubernetes
  connection:
    type: in-cluster
  autoHibernation:
    enabled: true
    schedule: "0 17 * * FRI"    # Hibernate weekends
    wakeSchedule: "0 9 * * MON" # Wake Monday morning
```

### Environment Forking
Clone production-like environments for testing:

```bash
# Fork production environment for load testing
kubectl get workspace production -o yaml | \
  yq eval '.metadata.name = "load-test" | .spec.resourceQuota.cpu = "8" | .spec.resourceQuota.memory = "16Gi"' | \
  kubectl apply -f -
```

## Configuration

### Workspace Types
- **kubernetes**: Native Kubernetes environment management

### Connection Types
- **in-cluster**: Use the operator's cluster context
- **local**: Use local kubectl configuration
- **kubeconfig**: Connect to external clusters via stored credentials

### Module Sources
- **Raw**: Inline YAML definitions
- **HTTP**: Remote resource definitions
- **Git**: Version-controlled configurations (planned)
- **OCI**: Container registry stored configurations (planned)

### Hibernation Scheduling
Configure automated sleep/wake cycles using cron expressions:

```yaml
spec:
  autoHibernation:
    enabled: true
    schedule: "0 18 * * MON-FRI"    # Weekday evenings
    wakeSchedule: "0 8 * * MON-FRI" # Weekday mornings
    timezone: "America/New_York"
    gracePeriod: "300s"             # 5-minute drain period
```

## Development

### Local Development
```bash
git clone https://github.com/forkspacer/forkspacer.git
cd forkspacer

# Install CRDs and run locally
make install
make run
```

### Testing
```bash
make test           # Unit tests
make test-e2e       # End-to-end tests
make lint           # Code quality checks
make verify         # Verify generated code
```

### Building
```bash
# Build container image
make docker-build IMG=your-registry/forkspacer:v1.0.0

# Push to registry
make docker-push IMG=your-registry/forkspacer:v1.0.0
```

## Monitoring and Observability

Forkspacer exposes Prometheus metrics for monitoring workspace lifecycle events:

```yaml
# workspace_total - Total number of workspaces
# workspace_hibernated_total - Number of hibernated workspaces
# workspace_wake_duration_seconds - Time to wake workspaces
# module_deployments_total - Total module deployments
```

## Security Considerations

- **RBAC**: Fine-grained permissions for workspace and module operations
- **Network Policies**: Automatic network isolation between workspaces
- **Pod Security Standards**: Enforced security contexts for all workloads
- **Secret Management**: Secure handling of credentials and configuration

## Contributing

We welcome contributions to the Forkspacer project.

### Development Workflow
1. Fork the repository
2. Create a feature branch
3. Implement changes with appropriate tests
4. Submit a pull request with detailed description

### Code Standards
- Follow Go best practices and project conventions
- Include unit tests for new functionality
- Update documentation for user-facing changes
- Ensure compatibility with supported Kubernetes versions

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Community

- **Documentation**: [docs.forkspacer.io](https://docs.forkspacer.io)
- **Issues**: [GitHub Issues](https://github.com/forkspacer/forkspacer/issues)
- **Discussions**: [GitHub Discussions](https://github.com/forkspacer/forkspacer/discussions)