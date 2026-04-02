.PHONY: build bench-image bench-run bench-clean

bin/gocacheprog: $(shell find cmd internal -name '*.go')
	go build -o $@ ./cmd/gocacheprog

build: bin/gocacheprog

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
