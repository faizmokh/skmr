.PHONY: build test vet snapshot release-check cross clean

build:
	go build -o bin/skmr ./cmd/skmr

test:
	go test -race ./...

vet:
	go vet ./...

release-check:
	@report=$$(mktemp); \
	status=0; goreleaser check >"$$report" 2>&1 || status=$$?; \
	cat "$$report"; \
	if [ "$$status" -ne 0 ] && ! grep -q "configuration is valid, but uses deprecated properties" "$$report"; then \
		rm -f "$$report"; \
		exit "$$status"; \
	fi; \
	rm -f "$$report"

snapshot:
	goreleaser release --snapshot --clean

cross:
	@mkdir -p dist
	@for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		GOOS=$${target%/*} GOARCH=$${target#*/} CGO_ENABLED=0 go build -o dist/skmr-$${target%/*}-$${target#*/} ./cmd/skmr || exit 1; \
	done

clean:
	rm -rf bin dist
