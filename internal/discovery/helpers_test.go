package discovery

import "testing"

func TestFriendlyNameFromInfoFields(t *testing.T) {
	cases := []struct {
		fields []string
		want   string
	}{
		{[]string{"fn=Living Room TV", "md=Chromecast"}, "Living Room TV"},
		{[]string{"md=Chromecast"}, ""},
		{[]string{}, ""},
		{[]string{"fn="}, ""},
	}
	for _, tc := range cases {
		if got := friendlyNameFromInfoFields(tc.fields); got != tc.want {
			t.Errorf("friendlyNameFromInfoFields(%v) = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

func TestModelFromInfoFields(t *testing.T) {
	cases := []struct {
		fields []string
		want   string
	}{
		{[]string{"md=Chromecast Ultra", "fn=Living Room"}, "Chromecast Ultra"},
		{[]string{"fn=TV"}, ""},
	}
	for _, tc := range cases {
		if got := modelFromInfoFields(tc.fields); got != tc.want {
			t.Errorf("modelFromInfoFields(%v) = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

func TestCapabilitiesFromInfoFields(t *testing.T) {
	cases := []struct {
		fields  []string
		wantVal uint32
		wantOK  bool
	}{
		{[]string{"ca=5", "fn=TV"}, 5, true},   // VIDEO_OUT(1) | AUDIO_OUT(4)
		{[]string{"ca=12", "fn=Home"}, 12, true}, // AUDIO_OUT(4) | AUDIO_IN(8) — audio only
		{[]string{"ca=0"}, 0, true},
		{[]string{"fn=TV"}, 0, false},            // absent
		{[]string{"ca=notanumber"}, 0, false},
	}
	for _, tc := range cases {
		got, ok := capabilitiesFromInfoFields(tc.fields)
		if ok != tc.wantOK || got != tc.wantVal {
			t.Errorf("capabilitiesFromInfoFields(%v) = (%d, %v), want (%d, %v)",
				tc.fields, got, ok, tc.wantVal, tc.wantOK)
		}
	}
}

func TestVideoCapabilityFilter(t *testing.T) {
	// ca=5 (VIDEO_OUT | AUDIO_OUT): include
	ca, ok := capabilitiesFromInfoFields([]string{"ca=5"})
	if !ok || (ca&0x01) == 0 {
		t.Error("ca=5 should have VIDEO_OUT bit set")
	}
	// ca=12 (AUDIO_OUT | AUDIO_IN): exclude
	ca, ok = capabilitiesFromInfoFields([]string{"ca=12"})
	if !ok || (ca&0x01) != 0 {
		t.Error("ca=12 should not have VIDEO_OUT bit set")
	}
}
