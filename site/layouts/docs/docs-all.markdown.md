{{ $docsRoot := .Site.GetPage "/docs" -}}
# {{ .Title }}

{{ range $docsRoot.Pages -}}
{{ if and (not .IsSection) (ne .Params.layout "docs-all") -}}
{{ .RawContent }}

---

{{ end -}}
{{ end -}}
{{ range $docsRoot.Sections -}}
{{ range .Pages -}}
{{ .RawContent }}

---

{{ end -}}
{{ end -}}
