.PHONY: run test bench clean

run:
	@echo "Running Redis Rate Limiter..."
	go run main.go

test:
	@echo "Running tests 10 times with race detector..."
	go test -v -race -count=10

bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem

clean:
	@echo "Cleaning test cache and generated files..."
	go clean -testcache
	rm -f coverage.out coverage.html

