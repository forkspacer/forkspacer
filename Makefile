# Version info
VERSION ?= v0.1.5
GIT_COMMIT ?= $(shell git rev-parse HEAD)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Linker flags
LDFLAGS := -ldflags "-X github.com/forkspacer/forkspacer/pkg/version.Version=$(VERSION) -X github.com/forkspacer/forkspacer/pkg/version.GitCommit=$(GIT_COMMIT) -X github.com/forkspacer/forkspacer/pkg/version.BuildDate=$(BUILD_DATE)"

# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/forkspacer/forkspacer:$(VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

GITHUB_OUTPUT ?= /dev/null
GITHUB_STEP_SUMMARY ?= /dev/null

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: print-img
print-img: ## Print the current IMG variable value.
	@echo $(IMG)

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $(LDFLAGS) $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER_TEST ?= operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_TEST)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_TEST)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_TEST)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_TEST) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	KIND=$(KIND) KIND_CLUSTER_TEST=$(KIND_CLUSTER_TEST) go test $(LDFLAGS) -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER_TEST)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build $(LDFLAGS) -o bin/manager cmd/main.go

.PHONY: build-plugin
build-plugin: ## Build a plugin using Docker. Usage: make build-plugin PLUGIN=test
	@if [ -z "$(PLUGIN)" ]; then \
		echo "Error: PLUGIN variable not set. Usage: make build-plugin PLUGIN=<plugin_name>"; \
		exit 1; \
	fi
	@if [ ! -f "plugins/$(PLUGIN)/main.go" ]; then \
		echo "Error: Plugin source file plugins/$(PLUGIN)/main.go not found"; \
		exit 1; \
	fi
	@echo "Building plugin '$(PLUGIN)' in Docker container..."
	$(CONTAINER_TOOL) build -f plugins/Dockerfile --build-arg PLUGIN=$(PLUGIN) -t plugin-builder-$(PLUGIN) .
	@container_id=$$($(CONTAINER_TOOL) create plugin-builder-$(PLUGIN)); \
	$(CONTAINER_TOOL) cp "$$container_id:/output/plugin.so" plugins/$(PLUGIN)/plugin.so; \
	$(CONTAINER_TOOL) rm "$$container_id"
	@echo "Plugin built successfully: plugins/$(PLUGIN)/plugin.so"
	@echo "Plugin size: $$(du -h plugins/$(PLUGIN)/plugin.so | cut -f1)"

.PHONY: dev-host
dev-host: manifests generate fmt vet ## Run a controller from your host.
	ENABLE_WEBHOOKS=false go run $(LDFLAGS) ./cmd/main.go

KIND_CLUSTER_DEV ?= operator-dev

.PHONY: dev-kind
dev-kind: manifests generate fmt vet ## Run a controller in a kind cluster.
	$(KIND) delete cluster -n $(KIND_CLUSTER_DEV)
	$(KIND) create cluster -n $(KIND_CLUSTER_DEV)
	$(MAKE) install docker-build
	$(KUBECTL) apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml
	$(KUBECTL) wait --for=condition=available --timeout=300s deployment/cert-manager -n cert-manager
	$(KUBECTL) wait --for=condition=available --timeout=300s deployment/cert-manager-cainjector -n cert-manager
	$(KUBECTL) wait --for=condition=available --timeout=300s deployment/cert-manager-webhook -n cert-manager
	$(KIND) load docker-image $(IMG) -n $(KIND_CLUSTER_DEV)
	$(MAKE) deploy

.PHONY: cleanup-dev-kind
cleanup-dev-kind: ## Tear down the Kind cluster used for dev
	$(KIND) delete cluster -n $(KIND_CLUSTER_DEV)

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: helm-sync
helm-sync: build-installer ## Generate Helm chart via Kubebuilder and move templates into helm folder
	@echo "🔧 Running Kubebuilder Helm plugin..."
	kubebuilder edit --plugins=helm/v1-alpha

	@echo "🚚 Moving templates folder from dist/chart/ to helm/..."
	if [ -d "dist/chart/templates" ]; then \
		rm -rf helm/templates; \
		mv dist/chart/templates helm/; \
	else \
		echo "⚠️  No templates found in dist/chart/"; \
	fi

	@echo "📄 Copying NOTES.txt from .github/templates/ to helm/templates/..."
	if [ -f ".github/templates/NOTES.txt" ]; then \
		cp .github/templates/NOTES.txt helm/templates/NOTES.txt; \
		echo "✅ NOTES.txt copied to helm/templates/"; \
	else \
		echo "⚠️  Warning: .github/templates/NOTES.txt not found, skipping NOTES.txt copy"; \
	fi

	@echo "📄 Copying custom templates from helm/custom-templates/..."
	if [ -d "helm/custom-templates" ] && [ -n "$$(ls -A helm/custom-templates 2>/dev/null)" ]; then \
		cp -r helm/custom-templates/* helm/templates/; \
		echo "✅ Custom templates copied to helm/templates/"; \
	else \
		echo "⚠️  No custom templates found in helm/custom-templates/"; \
	fi

	@echo "🧹 Subchart templates preserved (static files)"

	@echo "✅ Helm chart templates moved to ./helm/templates/"


.PHONY: helm-package
helm-package: helm-sync ## Package Helm chart with current manifests
	@echo "📦 Packaging Helm chart..."
	helm package helm
	@echo "✅ Helm chart packaged"

.PHONY: helm-package-ci
helm-package-ci: helm-sync ## Package Helm chart from helm directory
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	echo "📦 Packaging Helm chart version $$CHART_VERSION..."; \
	helm package helm/
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHART_FILE="forkspacer-$$CHART_VERSION.tgz"; \
	if [ ! -f "$$CHART_FILE" ]; then \
		echo "❌ Error: Expected chart file not found: $$CHART_FILE"; \
		echo "Available files:"; \
		ls -la *.tgz 2>&1 || echo "No .tgz files found"; \
		exit 1; \
	fi; \
	echo "chart_file=$$CHART_FILE" >> $(GITHUB_OUTPUT); \
	echo "✅ Packaged: $$CHART_FILE ($$(du -h $$CHART_FILE | cut -f1))"

.PHONY: build-charts-site
build-charts-site: helm-package-ci ## Create charts directory for GitHub Pages and update index
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHART_FILE="forkspacer-$$CHART_VERSION.tgz"; \
	CHARTS_DIR="charts-site"; \
	echo "📦 Preparing charts site..."; \
	mkdir -p "$$CHARTS_DIR"
	@echo "📥 Fetching existing charts from GitHub Pages..."
	@if curl -fsSL https://forkspacer.github.io/forkspacer/index.yaml -o /tmp/current-index.yaml 2>/dev/null; then \
		echo "✅ Found existing Helm repository"; \
	else \
		echo "ℹ️  No existing charts found (first deployment)"; \
	fi
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHART_FILE="forkspacer-$$CHART_VERSION.tgz"; \
	CHARTS_DIR="charts-site"; \
	if [ -f /tmp/current-index.yaml ]; then \
		grep -oP 'https://forkspacer\.github\.io/forkspacer/forkspacer-[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?\.tgz' /tmp/current-index.yaml | sort -u | while read url; do \
			filename=$$(basename "$$url"); \
			if [ "$$filename" = "$$CHART_FILE" ]; then \
				echo "  ⏭️  Skipping $$filename (will be replaced)"; \
				continue; \
			fi; \
			echo "  📥 Downloading $$filename..."; \
			if curl -fsSL "$$url" -o "$$CHARTS_DIR/$$filename"; then \
				echo "  ✅ Downloaded $$filename"; \
			else \
				echo "  ⚠️  Failed to download $$filename"; \
			fi; \
		done; \
	fi
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHART_FILE="forkspacer-$$CHART_VERSION.tgz"; \
	CHARTS_DIR="charts-site"; \
	echo "✅ Downloaded $$(ls $$CHARTS_DIR/forkspacer-*.tgz 2>/dev/null | wc -l) existing chart(s)"; \
	cp "$$CHART_FILE" "$$CHARTS_DIR/"; \
	echo "✅ Added new chart: $$CHART_FILE"
	@CHARTS_DIR="charts-site"; \
	echo "📄 Generating Helm repo index..."; \
	helm repo index "$$CHARTS_DIR" --url https://forkspacer.github.io/forkspacer
	@CHARTS_DIR="charts-site"; \
	if [ -f ".github/templates/helm-page.html" ]; then \
		cp .github/templates/helm-page.html "$$CHARTS_DIR/index.html"; \
	else \
		echo "⚠️  Warning: .github/templates/helm-page.html not found, skipping HTML generation"; \
	fi
	@CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	APP_VERSION=$$(grep '^appVersion:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHARTS_DIR="charts-site"; \
	if [ -f "$$CHARTS_DIR/index.html" ]; then \
		sed -i "s/{{VERSION_TAG}}/$$APP_VERSION/g" "$$CHARTS_DIR/index.html"; \
		sed -i "s/{{VERSION_NUMBER}}/$$CHART_VERSION/g" "$$CHARTS_DIR/index.html"; \
		echo "✅ Generated index.html from template"; \
	fi
	@CHARTS_DIR="charts-site"; \
	echo "✅ Charts site ready with $$(ls $$CHARTS_DIR/forkspacer-*.tgz 2>/dev/null | wc -l) chart version(s)"; \
	echo ""; \
	echo "📦 Available versions:"; \
	ls -lh $$CHARTS_DIR/forkspacer-*.tgz 2>/dev/null || echo "No charts found"

.PHONY: helm-summary
helm-summary: ## Generate GitHub Actions summary for Helm deployment
	@SUMMARY_FILE=$${GITHUB_STEP_SUMMARY:-/tmp/summary.md}; \
	CHART_VERSION=$$(grep '^version:' helm/Chart.yaml | awk '{print $$2}' | tr -d '"' | tr -d "'"); \
	CHART_FILE="forkspacer-$${CHART_VERSION}.tgz"; \
	echo "## 🎉 Helm Charts Deployed" >> $$SUMMARY_FILE; \
	echo "- **Version**: $$CHART_VERSION" >> $$SUMMARY_FILE; \
	echo "- **Chart**: $$CHART_FILE" >> $$SUMMARY_FILE; \
	echo "- **Repository**: https://forkspacer.github.io/forkspacer" >> $$SUMMARY_FILE; \
	echo "" >> $$SUMMARY_FILE; \
	echo "### 📦 Installation" >> $$SUMMARY_FILE; \
	echo '```bash' >> $$SUMMARY_FILE; \
	echo "helm repo add forkspacer https://forkspacer.github.io/forkspacer" >> $$SUMMARY_FILE; \
	echo "helm repo update" >> $$SUMMARY_FILE; \
	echo "helm install forkspacer forkspacer/forkspacer" >> $$SUMMARY_FILE; \
	echo '```' >> $$SUMMARY_FILE; \
	echo "✅ Summary written to $$SUMMARY_FILE"

##@ Version Management

.PHONY: update-operator-ui-version
update-operator-ui-version: ## Update operator-ui version. Usage: make update-operator-ui-version VERSION=v0.1.2
	@if [ -z "$(VERSION)" ]; then \
		echo "❌ Error: VERSION not specified. Usage: make update-operator-ui-version VERSION=v0.1.2"; \
		exit 1; \
	fi
	@echo "🔄 Updating operator-ui to $(VERSION)..."
	@sed -i.bak "/operatorUI:/,/pullPolicy:/ s/tag: v[0-9]\+\.[0-9]\+\.[0-9]\+/tag: $(VERSION)/" helm/values.yaml
	@CHART_VERSION=$${VERSION#v}; \
	sed -i.bak "/name: operator-ui/,/condition:/ s/version: \"[0-9]\+\.[0-9]\+\.[0-9]\+\"/version: \"$$CHART_VERSION\"/" helm/Chart.yaml; \
	sed -i.bak "s/^version: .*/version: $$CHART_VERSION/" helm/charts/operator-ui/Chart.yaml
	@sed -i.bak "s/^appVersion: \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"/appVersion: \"$(VERSION)\"/" helm/charts/operator-ui/Chart.yaml
	@rm -f helm/values.yaml.bak helm/Chart.yaml.bak helm/charts/operator-ui/Chart.yaml.bak
	@echo "✅ Updated operator-ui to $(VERSION)"

.PHONY: update-api-server-version
update-api-server-version: ## Update api-server version. Usage: make update-api-server-version VERSION=v0.1.1
	@if [ -z "$(VERSION)" ]; then \
		echo "❌ Error: VERSION not specified. Usage: make update-api-server-version VERSION=v0.1.1"; \
		exit 1; \
	fi
	@echo "🔄 Updating api-server to $(VERSION)..."
	@sed -i.bak "/apiServer:/,/pullPolicy:/ s/tag: [v]*[0-9]\+\.[0-9]\+\.[0-9]\+/tag: $(VERSION)/" helm/values.yaml
	@CHART_VERSION=$${VERSION#v}; \
	sed -i.bak "/name: api-server/,/condition:/ s/version: \"[0-9]\+\.[0-9]\+\.[0-9]\+\"/version: \"$$CHART_VERSION\"/" helm/Chart.yaml; \
	sed -i.bak "s/^version: .*/version: $$CHART_VERSION/" helm/charts/api-server/Chart.yaml
	@sed -i.bak "s/^appVersion: \"[v]*[0-9]\+\.[0-9]\+\.[0-9]\+\"/appVersion: \"$(VERSION)\"/" helm/charts/api-server/Chart.yaml
	@rm -f helm/values.yaml.bak helm/Chart.yaml.bak helm/charts/api-server/Chart.yaml.bak
	@echo "✅ Updated api-server to $(VERSION)"

.PHONY: update-forkspacer-version
update-forkspacer-version: ## Update forkspacer operator version. Usage: make update-forkspacer-version VERSION=v0.1.6
	@if [ -z "$(VERSION)" ]; then \
		echo "❌ Error: VERSION not specified. Usage: make update-forkspacer-version VERSION=v0.1.6"; \
		exit 1; \
	fi
	@echo "🔄 Updating forkspacer operator to $(VERSION)..."
	@sed -i.bak "/controllerManager:/,/imagePullPolicy:/ s/tag: v[0-9]\+\.[0-9]\+\.[0-9]\+/tag: $(VERSION)/" helm/values.yaml
	@CHART_VERSION=$${VERSION#v}; \
	sed -i.bak "s/^version: .*/version: $$CHART_VERSION/" helm/Chart.yaml
	@sed -i.bak "s/^appVersion: \"v[0-9]\+\.[0-9]\+\.[0-9]\+\"/appVersion: \"$(VERSION)\"/" helm/Chart.yaml
	@sed -i.bak "s/^VERSION ?= .*/VERSION ?= $(VERSION)/" Makefile
	@rm -f helm/values.yaml.bak helm/Chart.yaml.bak Makefile.bak
	@echo "✅ Updated forkspacer operator to $(VERSION)"

.PHONY: update-versions
update-versions: ## Update multiple versions. Usage: make update-versions FORKSPACER_VERSION=v0.1.6 UI_VERSION=v0.1.2 API_VERSION=v0.1.1
	@if [ -n "$(FORKSPACER_VERSION)" ]; then \
		$(MAKE) update-forkspacer-version VERSION=$(FORKSPACER_VERSION); \
	fi
	@if [ -n "$(UI_VERSION)" ]; then \
		$(MAKE) update-operator-ui-version VERSION=$(UI_VERSION); \
	fi
	@if [ -n "$(API_VERSION)" ]; then \
		$(MAKE) update-api-server-version VERSION=$(API_VERSION); \
	fi
	@if [ -z "$(FORKSPACER_VERSION)" ] && [ -z "$(UI_VERSION)" ] && [ -z "$(API_VERSION)" ]; then \
		echo "❌ Error: No versions specified. Usage: make update-versions FORKSPACER_VERSION=v0.1.6 UI_VERSION=v0.1.2 API_VERSION=v0.1.1"; \
		exit 1; \
	fi
	@echo "🎉 Version update complete!"

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name operator-builder
	$(CONTAINER_TOOL) buildx use operator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm operator-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.6.0
CONTROLLER_TOOLS_VERSION ?= v0.18.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.3.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $$(realpath $(1)-$(3)) $(1)
endef
