package catalog_test

import (
	"encoding/json"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"testing"
)

func TestImageParameterBoundaries(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, input string
		valid     bool
	}{
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","size":"2048x1152","n":10}`, true},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","quality":"xhigh"}`, true},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","quality":"max"}`, true},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","size":"1025x1024"}`, false},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","size":"3840x3840"}`, false},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","n":1.5}`, false},
		{"openai/gpt-image-2/text-to-image", `{"prompt":"x","output_format":"jpeg","background":"transparent"}`, true},
		// Seedream 低档尺寸由 forced_when 换成同比例最高档（见下方断言）。
		{"jimeng/seedream-4-5/text-to-image", `{"prompt":"x","size":"4K"}`, true},
		{"jimeng/seedream-4-5/text-to-image", `{"prompt":"x","size":"2K"}`, true},
		{"jimeng/seedream-4-5/text-to-image", `{"prompt":"x","size":"5504x3040"}`, true},
		{"jimeng/seedream-4-5/text-to-image", `{"prompt":"x","size":"1024x1024"}`, false},
		// advised 不再覆盖非法值：仍按声明的合法范围拒绝。
		{"gemini/gemini-3-pro-image/text-to-image", `{"prompt":"x","image_size":"512"}`, false},
		{"gemini/gemini-3-pro-image/text-to-image", `{"prompt":"x","aspect_ratio":"16:9","image_size":"4K"}`, true},
	} {
		t.Run(tc.id+tc.input, func(t *testing.T) {
			o, _ := c.Get(generation.OfferingID(tc.id))
			_, _, err := c.Normalize(o.Capability, o.ID, []byte(tc.input))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	// 合法质量建议值保留，组合 forced_when 仍生效；规范化保持幂等。
	gpt, _ := c.Get("openai/gpt-image-2/text-to-image")
	for _, background := range []string{"transparent", "opaque", "auto"} {
		in := `{"prompt":"x","quality":"low","output_format":"jpeg","background":"` + background + `"}`
		_, first, err := c.Normalize(gpt.Capability, gpt.ID, []byte(in))
		if err != nil {
			t.Fatal(err)
		}
		_, second, err := c.Normalize(gpt.Capability, gpt.ID, first)
		if err != nil {
			t.Fatal(err)
		}
		var a, b map[string]any
		_ = json.Unmarshal(first, &a)
		_ = json.Unmarshal(second, &b)
		if a["output_format"] != "png" || b["output_format"] != "png" || a["quality"] != "low" || b["quality"] != "low" {
			t.Fatalf("background=%s format=%v/%v quality=%v/%v", background, a["output_format"], b["output_format"], a["quality"], b["quality"])
		}
	}
	gemini, _ := c.Get("gemini/gemini-3-pro-image/text-to-image")
	_, fixed, err := c.Normalize(gemini.Capability, gemini.ID, []byte(`{"prompt":"x","image_size":"1K"}`))
	if err != nil {
		t.Fatal(err)
	}
	var geminiValues map[string]any
	_ = json.Unmarshal(fixed, &geminiValues)
	if geminiValues["image_size"] != "1K" {
		t.Fatalf("gemini image_size = %v, want legal advised 1K", geminiValues["image_size"])
	}
	// 每个比例都按该比例的最高分辨率出图；最高档原样保留。#267：由模型自行决定比例的
	// 2K/4K 已下线，旧取值与缺省都落到明确的 1:1 最高档 4096x4096。
	for _, id := range []string{"jimeng/seedream-4-5/text-to-image", "jimeng/seedream-4-5/image-to-image"} {
		seedream, _ := c.Get(generation.OfferingID(id))
		for chosen, want := range map[string]string{
			"2K": "4096x4096", "2048x2048": "4096x4096", "2560x1440": "5504x3040", "1440x2560": "3040x5504",
			"2304x1728": "4704x3520", "1728x2304": "3520x4704",
			"4K": "4096x4096", "4096x4096": "4096x4096", "5504x3040": "5504x3040", "": "4096x4096",
		} {
			input := map[string]any{"prompt": "x"}
			if seedream.Capability == "image-to-image" {
				input["reference_assets"] = []string{"asset"}
			}
			if chosen != "" {
				input["size"] = chosen
			}
			raw, _ := json.Marshal(input)
			_, normalized, err := c.Normalize(seedream.Capability, seedream.ID, raw)
			if err != nil {
				t.Fatalf("%s size %q: %v", id, chosen, err)
			}
			var values map[string]any
			_ = json.Unmarshal(normalized, &values)
			if values["size"] != want {
				t.Fatalf("%s size %q -> %v, want %s", id, chosen, values["size"], want)
			}
		}
	}
	o, _ := c.Get("jimeng/seedream-4-5/image-to-image")
	refs := make([]string, 14)
	for i := range refs {
		refs[i] = "asset"
	}
	for _, mode := range []string{"auto", "disabled"} {
		input, _ := json.Marshal(map[string]any{"prompt": "x", "reference_assets": refs, "sequential_image_generation": mode, "max_images": 2})
		_, _, err := c.Normalize(o.Capability, o.ID, input)
		if (err == nil) != (mode == "disabled") {
			t.Fatalf("combined limit mode=%s error=%v", mode, err)
		}
	}
}

// #267: no output-size parameter offers or defaults to a value that leaves the
// picture's proportions to the model, and a retired value still maps to an
// explicit size for callers and stored pins that send it.
func TestOutputSizeIsAlwaysAnExplicitTarget(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, offering := range append(c.List(generation.TextToImage), c.List(generation.ImageToImage)...) {
		for _, parameter := range offering.Parameters {
			if parameter.Semantic != "output-size" {
				continue
			}
			for _, retired := range []string{"auto", "2K", "4K"} {
				if parameter.Name != "aspect_ratio" && strings.Contains(string(parameter.Default), `"`+retired+`"`) {
					t.Fatalf("%s %s defaults to %s", offering.ID, parameter.Name, retired)
				}
				for _, option := range parameter.Options {
					if option.Value == retired {
						t.Fatalf("%s %s still offers %s", offering.ID, parameter.Name, retired)
					}
				}
			}
		}
	}
	gpt, _ := c.Get("openai/gpt-image-2/text-to-image")
	_, normalized, err := c.Normalize(gpt.Capability, gpt.ID, []byte(`{"prompt":"x","size":"auto"}`))
	if err != nil || !strings.Contains(string(normalized), `"size":"2048x2048"`) {
		t.Fatalf("legacy auto = %s, %v", normalized, err)
	}
}
