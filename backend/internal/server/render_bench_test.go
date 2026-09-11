package server

import (
	"testing"
)

func BenchmarkRenderIndexTemplate(b *testing.B) {
	template := `<!DOCTYPE html><html><head><title><%= metaTitle %></title><meta name="description" content="<%= metaDescription %>"></head><body><div id="root"><script>window.__PANEL_DATA__ = "<%- panelData %>";</script></div></body></html>`
	title := "Exodus Subscription Portal"
	desc := "Fast, Secure and Private Connection Management"
	panelData := "eyJ1c2VyIjoidGVzdCIsInN1YnNjcmlwdGlvbiI6eyJpZCI6IjEyMzQ1In19"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = renderIndexTemplate(template, title, desc, panelData)
	}
}
