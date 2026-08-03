.PHONY: test check-contract web-build build

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
