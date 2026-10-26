package references

import "testing"

// 自 app.go 迁移的视觉内联 URL 归一化与模板匹配覆盖：
// localPath 拒绝跨源/查询/穿越，matchRoute 按段精确匹配。
func TestLocalPathAcceptsSiteRoutesOnly(t *testing.T) {
	cases := []struct {
		name          string
		value         string
		publicBaseURL string
		want          string
		wantOK        bool
	}{
		{name: "relative asset", value: "/api/v1/assets/abc", want: "/api/v1/assets/abc", wantOK: true},
		{name: "absolute same origin", value: "https://art.example.com/api/v1/assets/abc", publicBaseURL: "https://art.example.com", want: "/api/v1/assets/abc", wantOK: true},
		{name: "absolute cross origin rejected", value: "https://evil.example.com/api/v1/assets/abc", publicBaseURL: "https://art.example.com", wantOK: false},
		{name: "query rejected", value: "/api/v1/assets/abc?download=1", wantOK: false},
		{name: "traversal rejected", value: "/api/v1/assets/../secret", wantOK: false},
		{name: "empty rejected", value: "   ", wantOK: false},
	}
	resolver := &Resolver{}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resolver.publicBaseURL = testCase.publicBaseURL
			got, err := resolver.localPath(testCase.value)
			if ok := err == nil; ok != testCase.wantOK {
				t.Fatalf("ok = %v, want %v (err=%v)", ok, testCase.wantOK, err)
			}
			if ok := err == nil; ok && got != testCase.want {
				t.Fatalf("path = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestMatchRouteMatchesBySegments(t *testing.T) {
	values, ok := matchRoute(
		"/api/v1/tasks/{taskID}/assets/{assetID}",
		"/api/v1/tasks/t1/assets/a1",
	)
	if !ok || len(values) != 2 || values[0] != "t1" || values[1] != "a1" {
		t.Fatalf("values = %#v, ok = %v", values, ok)
	}
	if _, ok := matchRoute("/api/v1/assets/{assetID}", "/api/v1/tasks/t1/assets/a1"); ok {
		t.Fatal("segment count mismatch should not match")
	}
	if _, ok := matchRoute("/api/v1/assets/{assetID}", "/api/v1/assets"); ok {
		t.Fatal("shorter path should not match")
	}
}
