package local

import (
	"context"
	"testing"
)

func TestModelIsolationRejectsProtocolRelativeNetworkAuthorities(t *testing.T) {
	for _, reference := range []string{
		"//192.0.2.10/material.mtl?sig=example",
		"//localhost/material.mtl",
		"//asset-server:8080/material.mtl",
		"//[2001:db8::1]/material.mtl",
		"//[::1]:8080/material.mtl",
		"//xn--bcher-kva.example/material.mtl",
		"//例子.测试/material.mtl",
		"//asset_server/material.mtl",
		"//user:password@192.0.2.10/material.mtl",
		"//localhost",
		"//3232235777/material.mtl",
		"%2F%2F192%2E0%2E2%2E10%2Fmaterial.mtl",
		`\/\/localhost\/material.mtl`,
		`\u002f\u002flocalhost/material.mtl`,
	} {
		t.Run(reference, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			_, err = store.Put(context.Background(), modelWrite([]byte("mtllib "+reference+"\n"), "obj", "text/plain"))
			if err == nil {
				t.Fatal("network material reference was committed")
			}
		})
	}
	if !containsExternalAddress(utf16LE("Texture: //192.0.2.10/material.png")) {
		t.Fatal("UTF16 network reference was accepted")
	}
}

func TestModelIsolationAcceptsOBJIndicesAndInlineData(t *testing.T) {
	for _, content := range []string{
		"v 0 0 0\nv 1 0 0\nv 0 1 0\nvn 0 0 1\nf 1//1 2//1 3//1\n",
		"mtllib materials/local.mtl\nf 1/2/3 2/3/4 3/4/5\n",
		"# // a comment\n# face 1//1\n",
		`{"buffers":[{"uri":"data:application/octet-stream;base64,AA//YWFh/BQ=="}]}`,
	} {
		if containsExternalAddress([]byte(content)) {
			t.Fatalf("local model syntax was rejected: %q", content)
		}
	}
}
