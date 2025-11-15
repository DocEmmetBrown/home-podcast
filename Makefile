BINARY ?= home-podcast
BUILD_DIR ?= bin
REMOTE_DIR ?= /opt/home-podcast
REMOTE_SYSTEMD_DIR ?= /etc/systemd/system
REMOTE_ENV_PATH ?= /etc/home-podcast.env
REMOTE_TMP_DIR ?= /tmp/home-podcast-deploy
REMOTE_TOKEN_FILE ?=
DEPLOY_USER ?= $(USER)
DEPLOY_HOST ?= 

.PHONY: build build-local test coverage clean deploy first-deploy check-deploy

build:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY) ./cmd/home-podcast

build-local:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/home-podcast

test:
	go test ./... -coverprofile=coverage.out

coverage: test
	go tool cover -func=coverage.out

clean:
	rm -rf $(BUILD_DIR) coverage.out

check-deploy:
	@if [ -z "$(DEPLOY_HOST)" ]; then echo "DEPLOY_HOST must be specified"; exit 1; fi

deploy: check-deploy build
	ssh $(DEPLOY_USER)@$(DEPLOY_HOST) "mkdir -p $(REMOTE_TMP_DIR)"
	scp $(BUILD_DIR)/$(BINARY) $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_TMP_DIR)/$(BINARY)
	ssh $(DEPLOY_USER)@$(DEPLOY_HOST) 'set -euo pipefail; tmpdir=$(REMOTE_TMP_DIR); sudo install -d -m 755 $(REMOTE_DIR); sudo install -m 755 $$tmpdir/$(BINARY) $(REMOTE_DIR)/$(BINARY); sudo systemctl restart home-podcast.service'

first-deploy: check-deploy build
	ssh $(DEPLOY_USER)@$(DEPLOY_HOST) "mkdir -p $(REMOTE_TMP_DIR)"
	scp $(BUILD_DIR)/$(BINARY) $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_TMP_DIR)/$(BINARY)
	scp deploy/home-podcast.service $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_TMP_DIR)/home-podcast.service
	scp deploy/home-podcast.env.example $(DEPLOY_USER)@$(DEPLOY_HOST):$(REMOTE_TMP_DIR)/home-podcast.env.example
	ssh $(DEPLOY_USER)@$(DEPLOY_HOST) 'set -euo pipefail; tmpdir=$(REMOTE_TMP_DIR); sudo install -d -m 755 $(REMOTE_DIR); sudo install -m 755 $$tmpdir/$(BINARY) $(REMOTE_DIR)/$(BINARY); sudo install -d -m 755 $(REMOTE_SYSTEMD_DIR); sudo install -m 644 $$tmpdir/home-podcast.service $(REMOTE_SYSTEMD_DIR)/home-podcast.service; sudo install -D -m 640 $$tmpdir/home-podcast.env.example $(REMOTE_ENV_PATH); if [ -n "$(REMOTE_TOKEN_FILE)" ]; then sudo install -d -o home-podcast -g home-podcast -m 750 "$(dir $(REMOTE_TOKEN_FILE))"; if [ ! -f "$(REMOTE_TOKEN_FILE)" ]; then sudo install -o home-podcast -g home-podcast -m 600 /dev/null "$(REMOTE_TOKEN_FILE)"; fi; fi; sudo systemctl daemon-reload; sudo systemctl enable --now home-podcast.service'
