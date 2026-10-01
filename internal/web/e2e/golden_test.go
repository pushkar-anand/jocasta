//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const goldenDir = "testdata/golden"

// A pixel counts as changed when any channel moves by more than
// goldenChannel, which absorbs anti-aliasing that differs between Chrome
// builds. A picture fails when more than goldenShare of its pixels changed:
// a moved control or a wrapped line changes far more than that.
const (
	goldenChannel = 40
	goldenShare   = 0.002
)

// isGolden picks the shots with a stored picture: the home network, as admin,
// on a phone, a tablet and a laptop in light, and the laptop in dark. The
// layout checks cover the rest.
func isGolden(j job) bool {
	if j.a.fixture.name != fixtureNormal.name || j.r.name != admin.name {
		return false
	}

	switch j.v.name {
	case phone.name, tablet.name:
		return j.theme == "light"
	case laptop.name:
		return true
	}

	return false
}

func (s *shot) goldenFile() string {
	return filepath.Join(goldenDir, strings.Join([]string{s.Page, s.State, s.Viewport, s.Theme}, "_")+".png")
}

// compareGolden checks s against its stored picture, or, when update is
// set, stores it if it is new or no longer matches. On a mismatch it writes
// the picture taken and a map of what changed under artifacts, when that is
// set.
func compareGolden(s *shot, update bool, artifacts string) error {
	name := s.goldenFile()

	want, err := os.ReadFile(name) //nolint:gosec // a path built from the shot's own names
	if errors.Is(err, fs.ErrNotExist) {
		if update {
			return os.WriteFile(name, s.golden, 0o600)
		}

		return fmt.Errorf("no stored picture %s; run with E2E_UPDATE=1 to take it", name)
	}

	if err != nil {
		return err
	}

	changed, diff, err := pixelDiff(want, s.golden)
	if err != nil && !update {
		return fmt.Errorf("%s: %w", name, err)
	}

	// A picture within tolerance is kept as stored even when updating, so a
	// change rewrites only the pictures it actually moved.
	if err == nil && changed <= goldenShare {
		return nil
	}

	if update {
		return os.WriteFile(name, s.golden, 0o600)
	}

	if artifacts != "" {
		base := filepath.Join(artifacts, strings.TrimSuffix(filepath.Base(name), ".png"))
		_ = os.WriteFile(base+".actual.png", s.golden, 0o600) //nolint:gosec // under the directory this run was pointed at

		var b bytes.Buffer
		if png.Encode(&b, diff) == nil {
			_ = os.WriteFile(base+".diff.png", b.Bytes(), 0o600) //nolint:gosec // under the directory this run was pointed at
		}
	}

	return fmt.Errorf("%s: %.2f%% of pixels changed; if the change is meant, run with E2E_UPDATE=1", name, changed*100)
}

// pixelDiff returns the share of pixels that changed between two pictures,
// and the new picture with changed pixels painted red.
func pixelDiff(a, b []byte) (float64, image.Image, error) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, nil, err
	}

	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}

	if ia.Bounds() != ib.Bounds() {
		return 1, ib, fmt.Errorf("size changed from %v to %v", ia.Bounds().Size(), ib.Bounds().Size())
	}

	r := ib.Bounds()
	diff := image.NewRGBA(r)
	changed := 0

	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ca, cb := ia.At(x, y), ib.At(x, y)
			if apart(ca, cb) {
				changed++

				diff.Set(x, y, color.RGBA{R: 255, A: 255})

				continue
			}

			diff.Set(x, y, cb)
		}
	}

	return float64(changed) / float64(r.Dx()*r.Dy()), diff, nil
}

func apart(a, b color.Color) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()

	d := func(x, y uint32) bool {
		x, y = x>>8, y>>8
		if x > y {
			return x-y > goldenChannel
		}

		return y-x > goldenChannel
	}

	return d(ar, br) || d(ag, bg) || d(ab, bb)
}
