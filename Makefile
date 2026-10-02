VERSION ?= 0.6.2-skills
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet dist clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/mangoman ./cmd/mangoman

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

dist:
	@for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=".exe"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/mangoman-$$os-$$arch$$ext ./cmd/mangoman || exit 1; \
		echo built dist/mangoman-$$os-$$arch$$ext; \
	done

clean:
	rm -rf bin dist
