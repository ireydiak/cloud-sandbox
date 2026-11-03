AWS_REGION  ?= us-east-1
AWS_PROFILE ?= default
export AWS_REGION AWS_PROFILE
export AWS_DEFAULT_REGION := $(AWS_REGION)

TOFU     := tofu -chdir=infra
TF_VARS  := -var region=$(AWS_REGION) -var profile=$(AWS_PROFILE)
GOARM    := CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w'
IMAGE    := ws-lab-server
B5       ?= 0

.PHONY: preflight build deploy plan test test-a test-b destroy verify-clean logs clean

preflight:
	@for t in tofu go docker aws; do \
	  command -v $$t >/dev/null || { echo "ERROR: '$$t' is required but not installed"; exit 1; }; \
	done
	@aws sts get-caller-identity --profile $(AWS_PROFILE) --region $(AWS_REGION) >/dev/null 2>&1 || \
	  { echo "ERROR: 'aws sts get-caller-identity' failed for profile '$(AWS_PROFILE)' (try: aws sso login --profile $(AWS_PROFILE))"; exit 1; }

# Go binaries only (no docker needed).
build-go:
	@mkdir -p build/lambda-ws build/lambda-push
	$(GOARM) -o build/server ./cmd/server
	$(GOARM) -o build/lambda-ws/bootstrap ./cmd/lambda-ws
	$(GOARM) -o build/lambda-push/bootstrap ./cmd/lambda-push
	go build -o build/client ./cmd/client

build: preflight build-go
	docker build --platform linux/arm64 --provenance=false -t $(IMAGE):latest .

init:
	$(TOFU) init -input=false

# Show what would be created (full plan) without changing anything.
plan: preflight build-go init
	$(TOFU) plan $(TF_VARS) -var image_tag=$$(shasum -a 256 build/server | cut -c1-12)

deploy: preflight build-go init
	$(TOFU) apply -auto-approve -input=false $(TF_VARS) -target=aws_ecr_repository.server
	@$(MAKE) --no-print-directory push-image
	$(TOFU) apply -auto-approve -input=false $(TF_VARS) -var image_tag=$$(shasum -a 256 build/server | cut -c1-12)
	$(TOFU) output -json > build/outputs.json
	@echo "Deployed. Waiting for the ECS service to become stable..."
	@aws ecs wait services-stable --cluster ws-lab --services ws-lab --region $(AWS_REGION)

push-image:
	@tag=$$(shasum -a 256 build/server | cut -c1-12); \
	repo=$$($(TOFU) output -raw ecr_repository_url); \
	docker build --platform linux/arm64 --provenance=false -t $$repo:$$tag . && \
	aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $${repo%%/*} && \
	docker push $$repo:$$tag

test: preflight build-go
	./build/client -stack all $(if $(filter 1,$(B5)),-b5)

test-a: preflight build-go
	./build/client -stack a

test-b: preflight build-go
	./build/client -stack b $(if $(filter 1,$(B5)),-b5)

logs: preflight
	@echo "Tailing ECS + Lambda logs (Ctrl-C to stop)"
	@aws logs tail /ecs/ws-lab /aws/lambda/ws-lab-ws /aws/lambda/ws-lab-push --follow --since 10m --region $(AWS_REGION) --profile $(AWS_PROFILE)

# Lambda zips must exist for the archive_file data sources even when destroying.
destroy: preflight build-go init
	$(TOFU) destroy -auto-approve -input=false $(TF_VARS)
	@$(MAKE) --no-print-directory clean
	@$(MAKE) --no-print-directory verify-clean

clean:
	rm -rf build infra/.terraform infra/terraform.tfstate infra/terraform.tfstate.backup infra/.terraform.lock.hcl

verify-clean: preflight
	@./scripts/verify-clean.sh
