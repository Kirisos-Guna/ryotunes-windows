package art

import "testing"

func TestSizedSourceURL(t *testing.T) {
	const id = "1cae05aed449b051fceb7a2d"
	for _, test := range []struct {
		url  string
		size int
		want string
	}{
		{"https://i.scdn.co/image/ab67616d00004851" + id, 512, "https://i.scdn.co/image/ab67616d0000b273" + id},
		{"https://i.scdn.co/image/ab67616d00004851" + id, 256, "https://i.scdn.co/image/ab67616d00001e02" + id},
		{"https://i.scdn.co/image/ab67616d0000b273" + id, 48, "https://i.scdn.co/image/ab67616d00004851" + id},
		{"https://i.scdn.co/image/ab67616100005174" + id, 1024, "https://i.scdn.co/image/ab6761610000e5eb" + id},
		{"https://i.scdn.co/image/ab67706f000000023e39", 512, "https://i.scdn.co/image/ab67706f000000023e39"}, // a playlist cover: unknown sizes
		{"https://lh3.googleusercontent.com/abc=w60-h60-l90-rj", 512, "https://lh3.googleusercontent.com/abc=w512-h512-l90-rj"},
		{"https://yt3.ggpht.com/abc=s88-c-k-c0x00ffffff-no-rj", 192, "https://yt3.ggpht.com/abc=s192-c-k-c0x00ffffff-no-rj"},
		{"https://i.ytimg.com/vi/x/hqdefault.jpg", 512, "https://i.ytimg.com/vi/x/hqdefault.jpg"},
	} {
		if got := sizedSourceURL(test.url, test.size); got != test.want {
			t.Errorf("sizedSourceURL(%q, %d) = %q, want %q", test.url, test.size, got, test.want)
		}
	}
}
