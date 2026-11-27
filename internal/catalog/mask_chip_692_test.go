package catalog

import "testing"

// #692: the mask chip over the input box words the mask the way its tool does
// (擦除重绘区 / Erase to repaint): erasing, not painting. Both locales.
func TestMaskChipWordsTheMaskAsErasing(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	if got, want := application.Content.Messages["ui.studio.mask.chip_idle"], "擦除需要重绘的区域"; got != want {
		t.Errorf("zh chip_idle = %q, want %q", got, want)
	}
	if got, want := application.Localization.Bundles["en"].Messages["ui.studio.mask.chip_idle"], "Erase the area to repaint"; got != want {
		t.Errorf("en chip_idle = %q, want %q", got, want)
	}
}
