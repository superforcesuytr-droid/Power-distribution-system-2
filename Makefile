# A local build identifies itself by commit, so "which build am I running?" is
# answerable from the app's About tab. Releases override it: make windows VERSION=1.8.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)

.PHONY: run dev test windows macos linux winres clean

run:            ## Run locally in browser mode (set DATABASE_URL or use the setup screen)
	go run ./cmd/pds --browser

dev:            ## Dev loop on a Mac: fixed port, UI served from web/ so edits need only a refresh
	go run ./cmd/pds --dev web --browser --port 8080

test:
	go vet ./... && go test ./...

windows:        ## Cross-compile the Windows executable into dist/
	mkdir -p dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o dist/PowerDistributionSystem.exe ./cmd/pds

macos:          ## Build the macOS .app bundle (Apple Silicon + Intel) into dist/
	build/build-macos.sh $(VERSION)

linux:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/pds ./cmd/pds

winres:         ## Regenerate the Windows icon/version resources (cmd/pds/*.syso)
	cd build/winres && go run github.com/tc-hib/go-winres@v0.3.3 make --in winres.json --out ../../cmd/pds/rsrc

clean:
	rm -rf dist
