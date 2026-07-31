# Build the site for deployment: Hugo render + Pagefind search index.
# Pagefind indexes the rendered HTML, so it must run after hugo.
build:
	hugo --cleanDestinationDir
	npx -y pagefind --site public

# Local preview with live reload. Note: search shows nothing in dev mode —
# the Pagefind index only exists in a `make build` output. To preview search:
#   make build && cd public && python3 -m http.server 1313
serve:
	hugo server

.PHONY: build serve
