.PHONY: build test bench bench-image bench-run bench-clean

bin/gocacheprog: $(shell find cmd internal -name '*.go')
	go build -o $@ ./cmd/gocacheprog

build: bin/gocacheprog

test:
	go test ./...

bench:
	go test -bench=. -benchmem -count=1 ./internal/protocol/ ./internal/reapi/ ./internal/cache/ ./internal/handler/

bench-image:
	docker compose -f bench/docker-compose.yml build

bench-run: bench-image
	mkdir -p bench/results
	docker compose -f bench/docker-compose.yml up --abort-on-container-exit builder
	@echo ""
	@echo "Results in bench/results/summary.txt"

bench-clean:
	docker compose -f bench/docker-compose.yml down -v
	rm -rf bench/results
