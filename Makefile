.PHONY: test check-contract web-build build assemble-complete assemble-lightweight

test:
	rtk go test ./...

check-contract:
	rtk ./scripts/check-contract.sh

web-build:
	rtk npm --prefix web run build

build: web-build
	rtk go build ./cmd/eco-guardian

build-production:
	rtk ./scripts/build-production.sh

build-macos:
	rtk go build -o dist/eco-guardian ./cmd/eco-guardian

build-windows:
	rtk env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/eco-guardian.exe ./cmd/eco-guardian

assemble-complete:
	rtk ./scripts/assemble-complete.sh $(ARGS)

assemble-lightweight:
	rtk ./scripts/assemble-lightweight.sh $(ARGS)
