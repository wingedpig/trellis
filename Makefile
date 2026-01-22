.PHONY: build clean test generate api-docs

# Find all .qtpl files
QTPL_FILES := $(wildcard views/*.qtpl)
QTPL_GO_FILES := $(QTPL_FILES:.qtpl=.qtpl.go)

# Default target
all: build

# Generate .qtpl.go files from .qtpl templates
views/%.qtpl.go: views/%.qtpl
	qtc -file $<

generate: $(QTPL_GO_FILES)

# Build binaries
build: generate
	go build -o trellis ./cmd/trellis
	go build -o trellis-ctl ./cmd/trellis-ctl

# Run tests
test: generate
	go test ./...

# Clean generated files and binaries
clean:
	rm -f trellis trellis-ctl
	rm -f views/*.qtpl.go

# Install dependencies
deps:
	go install github.com/valyala/quicktemplate/qtc@latest

# Run the application
run: build
	./trellis

# Generate API documentation with Trellis theme and navbar
# Base theme uses dark colors for light mode; dark mode overrides in template.hbs
api-docs:
	redocly build-docs api/openapi.yaml -o site/static/api/index.html \
		--template api/template.hbs \
		--theme.openapi.theme.colors.primary.main="#56AB2F" \
		--theme.openapi.theme.colors.success.main="#56AB2F" \
		--theme.openapi.theme.colors.text.primary="#222831" \
		--theme.openapi.theme.colors.http.get="#56AB2F" \
		--theme.openapi.theme.colors.http.post="#007F8B" \
		--theme.openapi.theme.colors.http.put="#ffc107" \
		--theme.openapi.theme.colors.http.delete="#dc3545" \
		--theme.openapi.theme.typography.code.color="#56AB2F" \
		--theme.openapi.theme.typography.headings.fontWeight="600" \
		--theme.openapi.theme.sidebar.backgroundColor="#fafafa" \
		--theme.openapi.theme.sidebar.textColor="#333333" \
		--theme.openapi.theme.sidebar.activeTextColor="#56AB2F" \
		--theme.openapi.theme.rightPanel.backgroundColor="#2c3e50" \
		--theme.openapi.theme.rightPanel.textColor="#ffffff"
