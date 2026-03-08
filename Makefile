# ============================================================================
# simple-cicd Makefile
# ============================================================================

IMAGE_REGISTRY  ?= ghcr.io/jlsalvador
IMAGE_NAME      ?= simple-cicd
# IMAGE_TAG defaults to the git-derived version; override as needed
IMAGE_TAG       ?= $(VERSION)
IMAGE           := $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

NAMESPACE       ?= simple-cicd

# Version is read from the latest git tag (e.g. v1.2.3).
# Override with: make build VERSION=v0.0.1
VERSION         ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
VERSION_PKG     := github.com/jlsalvador/simple-cicd/internal/version
LDFLAGS         := -s -w -X $(VERSION_PKG).Version=$(VERSION)
GOFLAGS         ?= -trimpath -ldflags="$(LDFLAGS)"

PLATFORMS       ?= linux/amd64,linux/arm64,linux/ppc64le,linux/s390x
BUILDX_BUILDER  ?= simple-cicd-builder

# ============================================================================
# Build
# ============================================================================

.PHONY: build
build: ## Build the operator binary for the host platform
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/simple-cicd ./cmd/simple-cicd

.PHONY: build-all
build-all: ## Cross-compile binaries for all supported platforms
	$(foreach platform,\
		linux/amd64 linux/arm64 linux/ppc64le linux/s390x,\
		$(eval OS   := $(word 1,$(subst /, ,$(platform))))\
		$(eval ARCH := $(word 2,$(subst /, ,$(platform))))\
		CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
			go build $(GOFLAGS) \
			-o bin/simple-cicd-$(OS)-$(ARCH) ./cmd/simple-cicd ;)

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run all tests
	go test -race -count=1 ./...

.PHONY: check
check: fmt vet test ## Run fmt + vet + test

# ============================================================================
# Docker - multi-platform via buildx
# ============================================================================

.PHONY: docker-builder-create
docker-builder-create: ## Create (once) the buildx builder with multi-platform support
	docker buildx inspect $(BUILDX_BUILDER) > /dev/null 2>&1 || \
		docker buildx create \
			--name $(BUILDX_BUILDER) \
			--driver docker-container \
			--platform $(PLATFORMS) \
			--bootstrap

.PHONY: docker-build
docker-build: docker-builder-create ## Build multi-platform image (local cache only, no push)
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE) \
		.

.PHONY: docker-push
docker-push: docker-builder-create ## Build and push multi-platform image to the registry
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE) \
		--push \
		.

.PHONY: docker-build-push
docker-build-push: docker-push ## Alias for docker-push (build + push in one step)

.PHONY: docker-builder-rm
docker-builder-rm: ## Remove the buildx builder
	docker buildx rm $(BUILDX_BUILDER) 2>/dev/null || true

# ============================================================================
# Deploy
# ============================================================================

.PHONY: namespace
namespace: ## Create the operator namespace (idempotent)
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -

.PHONY: install-crds
install-crds: ## Install (or upgrade) the three CRDs into the cluster
	kubectl apply -f deploy/crds/workflows.yaml
	kubectl apply -f deploy/crds/workflowwebhooks.yaml
	kubectl apply -f deploy/crds/workflowwebhookrequests.yaml
	kubectl wait --for condition=established --timeout=30s \
		crd/workflows.simple-cicd.jlsalvador.online \
		crd/workflowwebhooks.simple-cicd.jlsalvador.online \
		crd/workflowwebhookrequests.simple-cicd.jlsalvador.online

.PHONY: uninstall-crds
uninstall-crds: ## Remove the three CRDs (WARNING: also deletes ALL custom resources!)
	kubectl delete -f deploy/crds/ --ignore-not-found

.PHONY: deploy
deploy: namespace install-crds ## Apply CRDs + all operator manifests
	kubectl apply -f deploy/serviceaccount.yaml
	kubectl apply -f deploy/clusterrole.yaml
	kubectl apply -f deploy/clusterrolebinding.yaml
	kubectl apply -f deploy/deployment.yaml
	kubectl apply -f deploy/service.yaml

.PHONY: undeploy
undeploy: ## Remove all operator manifests (does NOT delete CRDs)
	kubectl delete -f deploy/service.yaml         --ignore-not-found
	kubectl delete -f deploy/deployment.yaml      --ignore-not-found
	kubectl delete -f deploy/clusterrolebinding.yaml --ignore-not-found
	kubectl delete -f deploy/clusterrole.yaml     --ignore-not-found
	kubectl delete -f deploy/serviceaccount.yaml  --ignore-not-found

.PHONY: restart
restart: ## Roll out a fresh deployment (pulls the latest image)
	kubectl rollout restart deployment/simple-cicd -n $(NAMESPACE)

.PHONY: status
status: ## Show rollout status
	kubectl rollout status deployment/simple-cicd -n $(NAMESPACE)

.PHONY: logs
logs: ## Tail operator logs
	kubectl logs -n $(NAMESPACE) -l app=simple-cicd --follow


# ============================================================================
# Helm
# ============================================================================

CHART_DIR       ?= charts/simple-cicd
HELM_RELEASE    ?= simple-cicd
HELM_NAMESPACE  ?= $(NAMESPACE)

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm lint $(CHART_DIR)

.PHONY: helm-template
helm-template: ## Render chart templates to stdout (dry-run)
	helm template $(HELM_RELEASE) $(CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION)

.PHONY: helm-install
helm-install: ## Install the chart (first time)
	helm install $(HELM_RELEASE) $(CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--create-namespace \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION)

.PHONY: helm-upgrade
helm-upgrade: ## Upgrade an existing chart release
	helm upgrade $(HELM_RELEASE) $(CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the chart release (does NOT delete CRDs)
	helm uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-manifests
helm-manifests: ## Render chart into a single install.yaml (kubectl apply -f install.yaml)
	@mkdir -p bin
	helm template $(HELM_RELEASE) $(CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION) \
		--include-crds \
		> bin/install.yaml
	@echo "Generated bin/install.yaml"

.PHONY: helm-package
helm-package: ## Package the chart into a .tgz in bin/
	@mkdir -p bin
	helm package $(CHART_DIR) --destination bin/

# ============================================================================
# Run locally (outside the cluster - requires a valid kubeconfig)
# ============================================================================

.PHONY: run
run: ## Run the operator locally using KUBECONFIG
	@echo "NOTE: override K8S_HOST and SA_DIR if running outside the cluster"
	go run ./cmd/simple-cicd

# ============================================================================
# Misc
# ============================================================================

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin/

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
