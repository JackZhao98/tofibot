package guest

import "testing"

func TestBrowserVersionAndProcessNamesCoverChromeAndChromium(t *testing.T) {
	for _, output := range []string{"Google Chrome 130.0.1 ", "Chromium 141.0.7390.54 built on Debian GNU/Linux 13"} {
		if !versionIsBrowser(output) {
			t.Fatalf("%q was not recognised as a supported browser", output)
		}
	}
	if versionIsBrowser("bash: not found") {
		t.Fatal("unrelated output was recognised as a browser")
	}
	for _, name := range []string{"chrome", "google-chrome-stable", "chromium"} {
		if !browserProcessNames[name] {
			t.Fatalf("%s is not a browser process name", name)
		}
	}
	if browserBinary() == "" {
		t.Fatal("browserBinary must always name a command")
	}
}
