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

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)
	
## Tool Binaries
GOCYCLO ?= $(LOCALBIN)/gocyclo
MISSPELL ?= $(LOCALBIN)/misspell

## Tool Versions
GOCYCLO_VERSION ?= latest
MISSPELL_VERSION ?= latest

$(GOCYCLO): $(LOCALBIN)
	test -s $(LOCALBIN)/gocyclo || GOBIN=$(LOCALBIN) go install github.com/fzipp/gocyclo/cmd/gocyclo@$(GOCYCLO_VERSION)

$(MISSPELL): $(LOCALBIN)
	test -s $(LOCALBIN)/misspell || GOBIN=$(LOCALBIN) go install github.com/client9/misspell/cmd/misspell@$(MISSPELL_VERSION)

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

##@ Build

.PHONY: _mkdir_build
_mkdir_build:
	mkdir -p bin

.PHONY: build
build: _mkdir_build ## Build the operator binary for the host platform
	CGO_ENABLED=0 go build $(GOFLAGS) -o bin/simple-cicd-operator ./cmd/operator

.PHONY: build-all
build-all: _mkdir_build ## Cross-compile binaries for all supported platforms
	$(foreach platform,\
		linux/amd64 linux/arm64 linux/ppc64le linux/s390x,\
		$(eval OS   := $(word 1,$(subst /, ,$(platform))))\
		$(eval ARCH := $(word 2,$(subst /, ,$(platform))))\
		CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) \
			go build $(GOFLAGS) \
			-o bin/simple-cicd-operator-$(OS)-$(ARCH) ./cmd/operator ;)

##@ Test

.PHONY: test-cyclo
test-cyclo: $(GOCYCLO) ## Run gocyclo against code.
	$(GOCYCLO) -over 15 .

.PHONY: test-misspell
test-misspell: $(MISSPELL) ## Run misspell against code.
	$(MISSPELL) -error .github cmd docs internal pkg LICENSE Makefile README.md

.PHONY: test-go
test-go: ## Test code.
	go test -race -count=1 -cover ./...

.PHONY: fmt
fmt: ## Run go fmt
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: test-cyclo test-misspell test-go ## Run all tests

.PHONY: check
check: fmt vet test ## Run fmt + vet + test

bin/cover.out: _mkdir_build
	go test \
		-covermode=count \
		-coverprofile "bin/cover.out" \
		$(shell go list ./... | grep -v /vendor/ | tr '\n' ' ')

bin/cover.txt: bin/cover.out
	go tool cover -func="bin/cover.out" -o "bin/cover.txt"

bin/cover.html: bin/cover.out
	go tool cover -html="bin/cover.out" -o "bin/cover.html"

.PHONY: cover
cover: bin/cover.txt bin/cover.html ## Generate coverage reports.
	@echo "total: "$(shell grep "total:" bin/cover.txt | awk '{print $$3}')

##@ Container

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
		-f Dockerfile.operator
		--builder $(BUILDX_BUILDER) \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE) \
		.

.PHONY: docker-push
docker-push: docker-builder-create ## Build and push multi-platform image to the registry
	docker buildx build \
		-f Dockerfile.operator
		--builder $(BUILDX_BUILDER) \
		--platform $(PLATFORMS) \
		--build-arg VERSION=$(VERSION) \
		--tag $(IMAGE) \
		--push \
		.

.PHONY: docker-builder-rm
docker-builder-rm: ## Remove the buildx builder
	docker buildx rm $(BUILDX_BUILDER) 2>/dev/null || true

##@ Helm

CHART_DIR       ?= charts/operator
HELM_RELEASE    ?= operator
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
helm-manifests: _mkdir_build ## Render chart into a single operator.yaml (kubectl apply -f operator.yaml)
	helm template $(HELM_RELEASE) $(CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(VERSION) \
		--include-crds \
		> bin/operator.yaml
	@echo "Generated bin/operator.yaml"

.PHONY: helm-package
helm-package: _mkdir_build ## Package the chart into a .tgz in bin/
	helm package $(CHART_DIR) --destination bin/

##@ Deploy

.PHONY: run
run: ## Run the operator locally using KUBECONFIG
	@echo "NOTE: override K8S_HOST and SA_DIR if running outside the cluster"
	go run ./cmd/operator

.PHONY: namespace
namespace: ## Create the operator namespace (idempotent)
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -

.PHONY: install-crds
install-crds: ## Install (or upgrade) the three CRDs into the cluster
	kubectl apply -f $(CHART_DIR)/crds/workflows.yaml
	kubectl apply -f $(CHART_DIR)/crds/workflowwebhooks.yaml
	kubectl apply -f $(CHART_DIR)/crds/workflowwebhookrequests.yaml
	kubectl wait --for condition=established --timeout=30s \
		crd/workflows.simple-cicd.jlsalvador.online \
		crd/workflowwebhooks.simple-cicd.jlsalvador.online \
		crd/workflowwebhookrequests.simple-cicd.jlsalvador.online

.PHONY: uninstall-crds
uninstall-crds: ## Remove the CRDs
	kubectl delete -f $(CHART_DIR)/crds/ --ignore-not-found

.PHONY: restart
restart: ## Roll out a fresh deployment (pulls the latest image)
	kubectl -n $(NAMESPACE) rollout restart deployment/$(HELM_RELEASE)

.PHONY: status
status: ## Show rollout status
	kubectl -n $(NAMESPACE) rollout status deployment/$(HELM_RELEASE)

.PHONY: logs
logs: ## Tail operator logs
	kubectl -n $(NAMESPACE) logs -l app.kubernetes.io/name=$(HELM_RELEASE) --follow

##@ Cleaning

.PHONY: clean
clean: ## Remove build artefacts
	rm -rf bin/
