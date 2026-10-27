.PHONY: fmt test testacc generate

fmt:
	gofmt -w .
	terraform fmt -recursive examples

test:
	go vet ./...
	go test -race ./...

# Requires WEKA_ENDPOINT, WEKA_USERNAME, WEKA_PASSWORD (and optionally
# WEKA_ORG, WEKA_INSECURE) for a real cluster. See AGENTS.md.
testacc:
	TF_ACC=1 go test -timeout 15m -v ./internal/provider/

generate:
	go generate ./...
