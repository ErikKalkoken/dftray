package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDisplayBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0.0 GB"},
		{1_000_000_000, "1.0 GB"},
		{1_500_000_000, "1.5 GB"},
		{999_999_999, "1.0 GB"},
		{49_999_999, "0.0 GB"},
		{36_270_682_112, "36.3 GB"},
	}
	for _, tc := range cases {
		if got := displayBytes(tc.in); got != tc.want {
			t.Errorf("displayBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// encodePNG returns a 3x1 PNG with the given alpha values and a
// color that differs from the theme colors used in the tests.
func encodePNG(t *testing.T, alphas ...uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, len(alphas), 1))
	for x, a := range alphas {
		img.SetNRGBA(x, 0, color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: a})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestThemedPNG(t *testing.T) {
	t.Run("recolors pixels and keeps alpha", func(t *testing.T) {
		in := encodePNG(t, 0, 128, 255)
		out, err := themedPNG(in, colorDarkError)
		if err != nil {
			t.Fatal(err)
		}
		img := decodePNG(t, out)
		for x, wantA := range []uint8{0, 128, 255} {
			got := color.NRGBAModel.Convert(img.At(x, 0)).(color.NRGBA)
			if got.A != wantA {
				t.Errorf("pixel %d: alpha = %d, want %d", x, got.A, wantA)
			}
			if wantA == 0 {
				continue // color of fully transparent pixels doesn't matter
			}
			if got.R != colorDarkError.R || got.G != colorDarkError.G || got.B != colorDarkError.B {
				t.Errorf("pixel %d: color = %v, want %v", x, got, colorDarkError)
			}
		}
	})

	t.Run("combines alpha with semi-transparent color", func(t *testing.T) {
		in := encodePNG(t, 128, 255)
		c := color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 128}
		out, err := themedPNG(in, c)
		if err != nil {
			t.Fatal(err)
		}
		img := decodePNG(t, out)
		for x, wantA := range []uint8{64, 128} {
			got := color.NRGBAModel.Convert(img.At(x, 0)).(color.NRGBA)
			if got.A != wantA {
				t.Errorf("pixel %d: alpha = %d, want %d", x, got.A, wantA)
			}
		}
	})

	t.Run("keeps size of embedded icon", func(t *testing.T) {
		out, err := themedPNG(iconPNGData, colorDarkForeground)
		if err != nil {
			t.Fatal(err)
		}
		want := decodePNG(t, iconPNGData).Bounds()
		if got := decodePNG(t, out).Bounds(); got != want {
			t.Errorf("bounds = %v, want %v", got, want)
		}
	})

	t.Run("returns error for invalid input", func(t *testing.T) {
		if _, err := themedPNG([]byte("not a png"), colorDarkForeground); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestEvaluate(t *testing.T) {
	const limit = 10 * gb
	above := measurement{available: 50 * gb, total: 100 * gb}
	below := measurement{available: 5 * gb, total: 100 * gb}
	atLimit := measurement{available: limit, total: 100 * gb}
	fetchErr := errors.New("statfs failed")

	cases := []struct {
		name         string
		m            measurement
		err          error
		limit        uint64
		margin       uint64
		wasTriggered bool
		want         update
	}{
		{
			name: "above limit",
			m:    above, limit: limit,
			want: update{title: "Available: 50.0 GB (50%)"},
		},
		{
			name: "drops below limit",
			m:    below, limit: limit,
			want: update{
				title:      "Available: 5.0 GB (5%) !!!",
				triggered:  true,
				icon:       iconSetError,
				notifyText: "Below storage limit: 5.0 GB left (limit 10.0 GB)",
			},
		},
		{
			name: "stays below limit",
			m:    below, limit: limit, wasTriggered: true,
			want: update{title: "Available: 5.0 GB (5%) !!!", triggered: true},
		},
		{
			name: "recovers above limit",
			m:    above, limit: limit, wasTriggered: true,
			want: update{title: "Available: 50.0 GB (50%)", icon: iconSetNormal},
		},
		{
			name: "exactly at limit is not below",
			m:    atLimit, limit: limit,
			want: update{title: "Available: 10.0 GB (10%)"},
		},
		{
			name: "stays triggered within margin",
			m:    measurement{available: 10_500_000_000, total: 100 * gb}, limit: limit, margin: 1 * gb, wasTriggered: true,
			want: update{title: "Available: 10.5 GB (10%) !!!", triggered: true},
		},
		{
			name: "clears exactly at limit plus margin",
			m:    measurement{available: 11 * gb, total: 100 * gb}, limit: limit, margin: 1 * gb, wasTriggered: true,
			want: update{title: "Available: 11.0 GB (11%)", icon: iconSetNormal},
		},
		{
			name: "margin does not affect triggering",
			m:    measurement{available: 10_500_000_000, total: 100 * gb}, limit: limit, margin: 1 * gb,
			want: update{title: "Available: 10.5 GB (10%)"},
		},
		{
			name: "zero limit disables alert",
			m:    measurement{available: 0, total: 100 * gb}, limit: 0,
			want: update{title: "Available: 0.0 GB (0%)"},
		},
		{
			name: "error keeps untriggered state",
			err:  fetchErr, limit: limit,
			want: update{title: "Available: ?"},
		},
		{
			name: "error keeps triggered state",
			err:  fetchErr, limit: limit, wasTriggered: true,
			want: update{title: "Available: ?", triggered: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluate(tc.m, tc.err, tc.limit, tc.margin, tc.wasTriggered)
			if got != tc.want {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestEvaluateSequence(t *testing.T) {
	const limit = 10 * gb
	above := measurement{available: 50 * gb, total: 100 * gb}
	below := measurement{available: 5 * gb, total: 100 * gb}
	fetchErr := errors.New("statfs failed")

	steps := []struct {
		m          measurement
		err        error
		wantIcon   iconChange
		wantNotify bool
	}{
		{m: above, wantIcon: iconUnchanged},
		{m: below, wantIcon: iconSetError, wantNotify: true},
		{m: below, wantIcon: iconUnchanged},
		{err: fetchErr, wantIcon: iconUnchanged},
		{m: below, wantIcon: iconUnchanged}, // no re-alert after an error
		{m: above, wantIcon: iconSetNormal},
		{m: below, wantIcon: iconSetError, wantNotify: true}, // re-armed
	}
	triggered := false
	for i, s := range steps {
		u := evaluate(s.m, s.err, limit, 0, triggered)
		triggered = u.triggered
		if u.icon != s.wantIcon {
			t.Errorf("step %d: icon = %v, want %v", i, u.icon, s.wantIcon)
		}
		if gotNotify := u.notifyText != ""; gotNotify != s.wantNotify {
			t.Errorf("step %d: notify = %v, want %v", i, gotNotify, s.wantNotify)
		}
	}
}

func TestEvaluateHysteresisSequence(t *testing.T) {
	const limit = 10 * gb
	const margin = 1 * gb
	at := func(v uint64) measurement {
		return measurement{available: v, total: 100 * gb}
	}

	steps := []struct {
		m          measurement
		wantIcon   iconChange
		wantNotify bool
	}{
		{m: at(10_200_000_000), wantIcon: iconUnchanged},
		{m: at(9_800_000_000), wantIcon: iconSetError, wantNotify: true},
		{m: at(10_300_000_000), wantIcon: iconUnchanged}, // within margin, stays triggered
		{m: at(9_700_000_000), wantIcon: iconUnchanged},  // no re-alert
		{m: at(11_100_000_000), wantIcon: iconSetNormal},
		{m: at(9_900_000_000), wantIcon: iconSetError, wantNotify: true}, // re-armed
	}
	triggered := false
	for i, s := range steps {
		u := evaluate(s.m, nil, limit, margin, triggered)
		triggered = u.triggered
		if u.icon != s.wantIcon {
			t.Errorf("step %d: icon = %v, want %v", i, u.icon, s.wantIcon)
		}
		if gotNotify := u.notifyText != ""; gotNotify != s.wantNotify {
			t.Errorf("step %d: notify = %v, want %v", i, gotNotify, s.wantNotify)
		}
	}
}
