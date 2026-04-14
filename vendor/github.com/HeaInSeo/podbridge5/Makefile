GO ?= go

PODBRIDGE5_VM_NAME ?= podbridge5-dev
PODBRIDGE5_VM_CPUS ?= 2
PODBRIDGE5_VM_MEMORY ?= 4G
PODBRIDGE5_VM_DISK ?= 20G
PODBRIDGE5_VM_REPO ?= /home/ubuntu/work/src/github.com/HeaInSeo/podbridge5
PODBRIDGE5_LOCAL_REPO ?= $(CURDIR)

REMOTE_HOST ?= 100.123.80.48
REMOTE_USER ?= seoy
REMOTE_PORT ?= 22
REMOTE_PASS ?=

ARTIFACTS_DIR ?= $(CURDIR)/artifacts
VM_TEST_RUNTIME_LOG ?= $(ARTIFACTS_DIR)/vm-test-runtime.log
VM_TEST_RUNTIME_INTEGRATION_LOG ?= $(ARTIFACTS_DIR)/vm-test-runtime-integration.log

REMOTE_VM_RUN = \
	REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
	PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_CPUS='$(PODBRIDGE5_VM_CPUS)' PODBRIDGE5_VM_MEMORY='$(PODBRIDGE5_VM_MEMORY)' PODBRIDGE5_VM_DISK='$(PODBRIDGE5_VM_DISK)' \
	PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)' PODBRIDGE5_LOCAL_REPO='$(PODBRIDGE5_LOCAL_REPO)' \
	cd /opt/go/src/github.com/HeaInSeo/podbridge5/hack/remotevm && $(GO) run .

.PHONY: test test-runtime test-runtime-integration runtime-env-check \
	check-remote-pass vm-create-runtime vm-prepare-runtime vm-sync-runtime \
	vm-run-runtime vm-run-runtime-integration vm-delete-runtime \
	vm-test-runtime vm-test-runtime-integration

# Legacy alias kept for compatibility.
test: test-runtime

test-runtime:
	$(GO) test -v -race -cover ./...

# Runtime-sensitive integration tests.
test-runtime-integration:
	@echo "Running integration tests with unshare..."
	@unshare -r -m $(GO) test -v -tags=integration ./...

runtime-env-check:
	@command -v buildah >/dev/null 2>&1 || { echo "missing: buildah" >&2; exit 1; }
	@command -v fuse-overlayfs >/dev/null 2>&1 || { echo "missing: fuse-overlayfs" >&2; exit 1; }
	@command -v pkg-config >/dev/null 2>&1 || { echo "missing: pkg-config" >&2; exit 1; }
	@pkg-config --exists gpgme || { echo "missing pkg-config entry: gpgme" >&2; exit 1; }
	@test -f /usr/include/btrfs/version.h || { echo "missing header: /usr/include/btrfs/version.h" >&2; exit 1; }
	@echo "runtime environment looks ready"

check-remote-pass:
	@test -n "$(REMOTE_PASS)" || { echo "set REMOTE_PASS for remote VM automation" >&2; exit 1; }

vm-create-runtime: check-remote-pass
	@$(REMOTE_VM_RUN) create

vm-prepare-runtime: check-remote-pass
	@$(REMOTE_VM_RUN) prepare

vm-sync-runtime: check-remote-pass
	@$(REMOTE_VM_RUN) sync

vm-run-runtime: check-remote-pass
	@$(REMOTE_VM_RUN) run

vm-run-runtime-integration: check-remote-pass
	@$(REMOTE_VM_RUN) run-integration

vm-delete-runtime: check-remote-pass
	@$(REMOTE_VM_RUN) delete

vm-test-runtime:
	@mkdir -p '$(ARTIFACTS_DIR)'
	@set -euo pipefail; \
	log_file='$(VM_TEST_RUNTIME_LOG)'; \
	cleanup() { \
		$(MAKE) --no-print-directory vm-delete-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' >/dev/null; \
	}; \
	trap cleanup EXIT INT TERM; \
	{ \
		echo "[vm-test-runtime] log file: $$log_file"; \
		echo "[vm-test-runtime] local repo: $(PODBRIDGE5_LOCAL_REPO)"; \
		$(MAKE) --no-print-directory vm-create-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_CPUS='$(PODBRIDGE5_VM_CPUS)' PODBRIDGE5_VM_MEMORY='$(PODBRIDGE5_VM_MEMORY)' PODBRIDGE5_VM_DISK='$(PODBRIDGE5_VM_DISK)'; \
		$(MAKE) --no-print-directory vm-prepare-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)'; \
		$(MAKE) --no-print-directory vm-sync-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)' PODBRIDGE5_LOCAL_REPO='$(PODBRIDGE5_LOCAL_REPO)'; \
		$(MAKE) --no-print-directory vm-run-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)'; \
	} 2>&1 | tee "$$log_file"; \
	status=$${PIPESTATUS[0]}; \
	exit $$status

vm-test-runtime-integration:
	@mkdir -p '$(ARTIFACTS_DIR)'
	@set -euo pipefail; \
	log_file='$(VM_TEST_RUNTIME_INTEGRATION_LOG)'; \
	cleanup() { \
		$(MAKE) --no-print-directory vm-delete-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' >/dev/null; \
	}; \
	trap cleanup EXIT INT TERM; \
	{ \
		echo "[vm-test-runtime-integration] log file: $$log_file"; \
		echo "[vm-test-runtime-integration] local repo: $(PODBRIDGE5_LOCAL_REPO)"; \
		$(MAKE) --no-print-directory vm-create-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_CPUS='$(PODBRIDGE5_VM_CPUS)' PODBRIDGE5_VM_MEMORY='$(PODBRIDGE5_VM_MEMORY)' PODBRIDGE5_VM_DISK='$(PODBRIDGE5_VM_DISK)'; \
		$(MAKE) --no-print-directory vm-prepare-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)'; \
		$(MAKE) --no-print-directory vm-sync-runtime \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)' PODBRIDGE5_LOCAL_REPO='$(PODBRIDGE5_LOCAL_REPO)'; \
		$(MAKE) --no-print-directory vm-run-runtime-integration \
			REMOTE_HOST='$(REMOTE_HOST)' REMOTE_USER='$(REMOTE_USER)' REMOTE_PORT='$(REMOTE_PORT)' REMOTE_PASS='$(REMOTE_PASS)' \
			PODBRIDGE5_VM_NAME='$(PODBRIDGE5_VM_NAME)' PODBRIDGE5_VM_REPO='$(PODBRIDGE5_VM_REPO)'; \
	} 2>&1 | tee "$$log_file"; \
	status=$${PIPESTATUS[0]}; \
	exit $$status
