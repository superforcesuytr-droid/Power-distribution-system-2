VERSION ?= 1.0.0
LDFLAGS  = -s -w -X main.version=$(VERSION)

.PHONY: run test windows linux winres clean

run:            ## Run locally in browser mode (set DATABASE_URL or use the setup screen)
	go run ./cmd/pds --browser

test:
	go vet ./... && go test ./...

windows:        ## Cross-compile the Windows executable into dist/
	mkdir -p dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o dist/PowerDistributionSystem.exe ./cmd/pds

linux:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/pds ./cmd/pds

winres:         ## Regenerate the Windows icon/version resources (cmd/pds/*.syso)
	cd build/winres && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres.json --out ../../cmd/pds/rsrc

clean:
	rm -rf dist
