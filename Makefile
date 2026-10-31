.PHONY: build build-web test-web smoke-web smoke-work smoke-work-tasktopia smoke-settings smoke-settings-tasktopia smoke-credentials test vet integration smoke smoke-context smoke-agent-context smoke-directory smoke-identity smoke-forge smoke-tasktopia smoke-opencode test-agents verify verify-linux clean
.PHONY: smoke-cli-consent smoke-cli-auth test-native-secret-store-linux
.PHONY: build-image smoke-deployment smoke-recovery
.PHONY: test-egress-linux smoke-egress
.PHONY: smoke-native-history
.PHONY: smoke-compatibility
.PHONY: build-identity-image smoke-keycloak smoke-keycloak-native
.PHONY: build-identity-root-image
.PHONY: smoke-identity-proxy
.PHONY: smoke-cli-downloads smoke-cli-release test-cli-packaging
.PHONY: smoke-onboarding smoke-forge-subpath smoke-gitlab-profile smoke-browser-compat verify-release

build-web:
	npm run build:web

test-web:
	npm run test:web

build: build-web
	go build -trimpath -o bin/workgraphd ./cmd/workgraphd
	go build -trimpath -o bin/workgraph ./cmd/workgraph
	ln -sf workgraphd bin/gitmorad
	ln -sf workgraph bin/gitmora

.PHONY: build-postgres-image
build-postgres-image:
	docker build --file deploy/postgres/Dockerfile --tag gitmora-postgres:18.6-security1 .

smoke smoke-opencode smoke-codex smoke-native-history smoke-compatibility smoke-forge \
smoke-tasktopia smoke-context smoke-agent-context smoke-directory smoke-identity \
smoke-web smoke-settings smoke-settings-tasktopia smoke-credentials smoke-cli-consent \
smoke-cli-auth smoke-work smoke-work-tasktopia smoke-cli-release: build-postgres-image

build-image: build-postgres-image
	docker build --file deploy/Dockerfile --tag gitmora:local .

build-identity-image: build-identity-theme build-postgres-image
	docker build --file deploy/identity/Dockerfile --tag gitmora-identity:26.8.1-auth .

build-identity-root-image: build-identity-theme build-postgres-image
	docker build --file deploy/identity/Dockerfile --build-arg IDENTITY_HTTP_PREFIX=/ --tag gitmora-identity:26.8.1-root .

smoke-keycloak: build build-image build-identity-image
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-keycloak.py

smoke-keycloak-native: build build-image build-identity-image
	docker compose -f compose.test.yaml up -d --wait
	GITMORA_KEYCLOAK_NATIVE=1 python3 scripts/smoke-keycloak.py

smoke-identity-proxy: build-postgres-image
	python3 scripts/smoke-identity-proxy.py

smoke-cli-downloads:
	python3 scripts/smoke-cli-downloads.py

test-cli-packaging:
	python3 -m unittest discover -s scripts/tests -p test_release_cli.py

smoke-cli-release: build test-cli-packaging
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-cli-release.py

smoke-deployment: build build-image
	python3 scripts/smoke-deployment.py

smoke-recovery: build build-image
	python3 scripts/smoke-recovery.py

test-egress-linux: build-postgres-image
	bash scripts/test-egress-linux.sh

smoke-egress: build build-image test-egress-linux
	python3 scripts/smoke-egress-compose.py

test:
	go test ./...

vet:
	go vet ./...

integration: build-postgres-image
	bash scripts/test-integration.sh

smoke: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-api.py

test-agents:
	npm run test:agents

smoke-opencode: build test-agents
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-opencode.py

.PHONY: smoke-codex
smoke-codex: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-codex.py

smoke-native-history: build test-agents
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-native-history.py

smoke-compatibility: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-compatibility.py

smoke-forge: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-forge.py

smoke-forge-subpath: build build-postgres-image
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-forge.py --subpath

smoke-onboarding: build test-web
	python3 scripts/smoke-onboarding.py

smoke-gitlab-profile:
	python3 scripts/smoke-gitlab-profile.py

smoke-tasktopia: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-tasktopia.py

smoke-context: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-context.py

smoke-agent-context: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-agent-context.py

smoke-directory: build
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-directory.py

smoke-identity: build
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-identity.py

smoke-web: build test-web
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-web.py

smoke-settings: build test-web
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-settings.py

smoke-settings-tasktopia: build
	docker compose -f compose.test.yaml up -d --wait
	WORKGRAPH_TASKTOPIA_BROWSER=1 python3 scripts/smoke-tasktopia.py

smoke-credentials: build test-web
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-credentials.py

smoke-cli-consent: build test-web
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-cli-auth.py

smoke-cli-auth: build
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	WORKGRAPH_NATIVE_AUTH=1 python3 scripts/smoke-cli-auth.py

test-native-secret-store-linux: build-postgres-image
	bash scripts/test-native-secret-store-linux.sh

smoke-work: build test-web
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-web.py --work

smoke-work-tasktopia: build
	docker compose -f compose.test.yaml up -d --wait
	WORKGRAPH_WORK_BROWSER=1 python3 scripts/smoke-tasktopia.py

verify: vet integration smoke smoke-opencode smoke-codex smoke-native-history smoke-compatibility smoke-forge smoke-context smoke-agent-context smoke-directory smoke-identity smoke-web smoke-settings smoke-credentials smoke-cli-consent smoke-work smoke-deployment smoke-recovery smoke-egress smoke-keycloak smoke-registration smoke-identity-proxy smoke-cli-downloads smoke-cli-release

verify-linux: build-web build-postgres-image
	bash scripts/test-linux.sh

clean:
	docker compose -f compose.test.yaml down
	rm -rf bin coverage

.PHONY: build-identity-theme
build-identity-theme:
	node scripts/build-identity-theme.mjs

.PHONY: smoke-registration
smoke-registration: build build-image build-identity-image
	docker compose -f compose.test.yaml up -d --wait
	GITMORA_KEYCLOAK_REGISTRATION=1 python3 scripts/smoke-keycloak.py

.PHONY: smoke-design verify-ui
smoke-design: build test-web build-postgres-image
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	python3 scripts/smoke-settings.py --design

verify-ui:
	GITMORA_UI_AUDIT=1 $(MAKE) smoke-design smoke-web smoke-work smoke-settings smoke-credentials smoke-cli-consent smoke-registration

# Source-backed Tasktopia gates additionally require TASKTOPIA_SOURCE and Node 24+; see development.md.
verify-release: verify verify-linux smoke-cli-auth test-native-secret-store-linux smoke-forge-subpath smoke-gitlab-profile smoke-onboarding smoke-design smoke-browser-compat

smoke-browser-compat: build test-web build-postgres-image
	mkdir -p bin/verification
	go build -trimpath -o bin/verification/oidc-fixture ./internal/identity/oidc/testdata/fixture.go
	docker compose -f compose.test.yaml up -d --wait
	GITMORA_BROWSER=webkit GITMORA_UI_AUDIT_LABEL=safari-desktop python3 scripts/smoke-settings.py --design
	GITMORA_BROWSER=webkit GITMORA_MOBILE_DEVICE=1 GITMORA_UI_AUDIT_LABEL=safari-iphone python3 scripts/smoke-settings.py --design
	GITMORA_BROWSER=chromium GITMORA_MOBILE_DEVICE=1 GITMORA_UI_AUDIT_LABEL=chrome-android python3 scripts/smoke-settings.py --design
	GITMORA_BROWSER=firefox GITMORA_UI_AUDIT_LABEL=firefox-desktop python3 scripts/smoke-settings.py --design

	GITMORA_UI_AUDIT=0 GITMORA_BROWSER=webkit GITMORA_MOBILE_DEVICE=1 python3 scripts/smoke-web.py
	GITMORA_UI_AUDIT=0 GITMORA_BROWSER=webkit GITMORA_MOBILE_DEVICE=1 python3 scripts/smoke-web.py --work
	GITMORA_UI_AUDIT=0 GITMORA_BROWSER=chromium GITMORA_MOBILE_DEVICE=1 python3 scripts/smoke-web.py
	GITMORA_UI_AUDIT=0 GITMORA_BROWSER=chromium GITMORA_MOBILE_DEVICE=1 python3 scripts/smoke-web.py --work
