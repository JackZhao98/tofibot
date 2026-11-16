package webtools

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// A page result lists its main pictures: the declared share image first, then
// content images in order, without icons, SVG files, data URIs or repeats.
func TestPageImagesListTheMainPictures(t *testing.T) {
	base, _ := url.Parse("https://example.com/post/1")
	src := `<html><head><meta property="og:image" content="https://cdn.example.com/share.jpg?a=1&amp;b=2"><meta name="twitter:image" content="/share.jpg"></head>
<body><header><img src="/logo.png" width="40"></header><article>
<p>` + strings.Repeat("A finished model on the shelf. ", 20) + `</p>
<img src="photos/one.jpg" alt="one">
<img src="data:image/gif;base64,R0lGOD" data-src="/lazy/two.jpg">
<img src="/icon.svg">
<img src="https://cdn.example.com/share.jpg?a=1&b=2">
<img src="/pixel.gif" width="1" height="1">
<img src="/three.webp" height="120px">
<img src="/four.jpg"><img src="/five.jpg"><img src="/six.jpg">
</article></body></html>`
	got := ParseHTML(context.Background(), src, base).Images
	want := []string{
		"https://cdn.example.com/share.jpg?a=1&b=2",
		"https://example.com/share.jpg",
		"https://example.com/post/photos/one.jpg",
		"https://example.com/lazy/two.jpg",
		"https://example.com/three.webp",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("images\n got %v\nwant %v", got, want)
	}
}

// Only the first part of a long page carries the image list.
func TestLaterPartsOmitImages(t *testing.T) {
	page := Page{URL: "https://example.com", Images: []string{"https://example.com/a.jpg"}, Content: strings.Repeat("x", 5000)}
	if first := Slice(page, 0, 1000); len(first.Images) != 1 {
		t.Errorf("first part images %v", first.Images)
	}
	if later := Slice(page, 1000, 1000); later.Images != nil {
		t.Errorf("later part images %v", later.Images)
	}
}
