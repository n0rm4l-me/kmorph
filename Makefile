REGISTRY ?= ghcr.io/n0rm4l-me
IMG_NAME ?= kmorph
VERSION ?= 0.3.0
IMG ?= $(REGISTRY)/$(IMG_NAME):$(VERSION)

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.20.1

KUBECTL ?= kubectl

.PHONY: all
all: build

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD and RBAC manifests.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=2026 paths="./..."

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: ## Run unit tests (no envtest required).
	go test ./... -v -count=1 -run "^Test[^C]" -skip "TestControllers"

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run controller locally.
	go run ./cmd/main.go

.PHONY: image-build
image-build: ## Build container image with podman (linux/amd64).
	podman build --platform linux/amd64 -t $(IMG) .

.PHONY: image-push
image-push: ## Push container image with podman.
	podman push $(IMG)

.PHONY: image
image: image-build image-push ## Build and push container image.

##@ Deployment

.PHONY: install-crd
install-crd: manifests ## Install CRD into cluster.
	$(KUBECTL) apply -f config/crd/bases/

.PHONY: uninstall-crd
uninstall-crd: ## Uninstall CRD from cluster.
	$(KUBECTL) delete --ignore-not-found=true -f config/crd/bases/

.PHONY: helm-install
helm-install: ## Install kmorph via Helm.
	helm upgrade --install kmorph charts/kmorph \
		--namespace kmorph-system --create-namespace \
		--set image.tag=$(VERSION)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall kmorph via Helm.
	helm uninstall kmorph --namespace kmorph-system

##@ Dependencies

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
